package pool

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// TransactionManager manages active transactions and handles timeouts.
type TransactionManager struct {
	mu               sync.RWMutex
	transactions     map[uint64]*TransactionInfo
	timeout          time.Duration
	idleTimeout      time.Duration
	checkInterval    time.Duration
	stopCh           chan struct{}
	log              *logger.Logger
	onAbort          func(sessionID uint64)                  // Callback to abort backend transaction
	onAbortWithConn  func(sessionID uint64, serverConn net.Conn) // Callback with backend connection
}

// TransactionInfo represents information about an active transaction.
type TransactionInfo struct {
	ID              uint64
	SessionID       uint64
	StartTime       time.Time
	LastActivity    time.Time
	ServerConnID    uint64
	QueryCount      atomic.Int32
	Status          TransactionStatus
	AbortFunc       func() // Called by checkTimeouts to abort backend transaction
	mu              sync.RWMutex
}

// TransactionStatus represents the status of a transaction.
type TransactionStatus int

const (
	TxnActive TransactionStatus = iota
	TxnIdle
	TxnAborted
	TxnCommitted
)

// String returns the string representation of TransactionStatus.
func (s TransactionStatus) String() string {
	switch s {
	case TxnActive:
		return "active"
	case TxnIdle:
		return "idle"
	case TxnAborted:
		return "aborted"
	case TxnCommitted:
		return "committed"
	default:
		return "unknown"
	}
}

// NewTransactionManager creates a new transaction manager.
func NewTransactionManager(timeout, idleTimeout, checkInterval time.Duration, log *logger.Logger) *TransactionManager {
	if timeout == 0 {
		timeout = 30 * time.Minute // Default 30 min transaction timeout
	}
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute // Default 5 min idle timeout
	}
	if checkInterval == 0 {
		checkInterval = 30 * time.Second
	}

	tm := &TransactionManager{
		transactions:  make(map[uint64]*TransactionInfo),
		timeout:       timeout,
		idleTimeout:   idleTimeout,
		checkInterval: checkInterval,
		stopCh:        make(chan struct{}),
		log:           log,
	}

	// Start background monitor
	go tm.monitorLoop()

	return tm
}

// OnAbort sets a callback invoked when a transaction is aborted due to timeout.
func (tm *TransactionManager) OnAbort(fn func(sessionID uint64)) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.onAbort = fn
}

// SetOnAbortWithConn sets a callback invoked when a transaction is aborted.
// This variant receives the backend connection so ROLLBACK can be sent.
func (tm *TransactionManager) SetOnAbortWithConn(fn func(sessionID uint64, serverConn net.Conn)) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.onAbortWithConn = fn
}

// Register registers a new transaction.
func (tm *TransactionManager) Register(sessionID, serverConnID uint64, abortFn func()) *TransactionInfo {
	info := &TransactionInfo{
		ID:           generateTxnID(),
		SessionID:    sessionID,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		ServerConnID: serverConnID,
		Status:       TxnActive,
		AbortFunc:    abortFn,
	}

	tm.mu.Lock()
	tm.transactions[info.ID] = info
	tm.mu.Unlock()

	tm.log.Debug("Transaction registered",
		"txn_id", info.ID,
		"session_id", sessionID,
		"server_conn", serverConnID,
	)

	return info
}

// Unregister removes a transaction from tracking.
func (tm *TransactionManager) Unregister(txnID uint64) {
	tm.mu.Lock()
	delete(tm.transactions, txnID)
	tm.mu.Unlock()

	tm.log.Debug("Transaction unregistered", "txn_id", txnID)
}

// Get returns transaction info by ID.
func (tm *TransactionManager) Get(txnID uint64) *TransactionInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.transactions[txnID]
}

// UpdateActivity updates the last activity timestamp.
func (tm *TransactionManager) UpdateActivity(txnID uint64) {
	tm.mu.RLock()
	info, exists := tm.transactions[txnID]
	tm.mu.RUnlock()

	if exists {
		info.mu.Lock()
		info.LastActivity = time.Now()
		info.mu.Unlock()
	}
}

// IncrementQueryCount increments the query counter.
func (tm *TransactionManager) IncrementQueryCount(txnID uint64) {
	tm.mu.RLock()
	info, exists := tm.transactions[txnID]
	tm.mu.RUnlock()

	if exists {
		info.QueryCount.Add(1)
	}
}

// SetStatus sets the transaction status.
func (tm *TransactionManager) SetStatus(txnID uint64, status TransactionStatus) {
	tm.mu.RLock()
	info, exists := tm.transactions[txnID]
	tm.mu.RUnlock()

	if exists {
		info.mu.Lock()
		info.Status = status
		info.mu.Unlock()
	}
}

// monitorLoop monitors transactions for timeouts.
func (tm *TransactionManager) monitorLoop() {
	ticker := time.NewTicker(tm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-tm.stopCh:
			return
		case <-ticker.C:
			tm.checkTimeouts()
		}
	}
}

// checkTimeouts checks for timed out transactions.
func (tm *TransactionManager) checkTimeouts() {
	now := time.Now()

	tm.mu.RLock()
	timeouts := make([]*TransactionInfo, 0)
	idleTimeouts := make([]*TransactionInfo, 0)

	for _, info := range tm.transactions {
		info.mu.RLock()
		if info.Status == TxnActive {
			// Check transaction timeout
			if now.Sub(info.StartTime) > tm.timeout {
				timeouts = append(timeouts, info)
			} else if now.Sub(info.LastActivity) > tm.idleTimeout {
				// Check idle timeout
				idleTimeouts = append(idleTimeouts, info)
			}
		}
		info.mu.RUnlock()
	}
	tm.mu.RUnlock()

	// Get the abort callback (under read lock to avoid race with OnAbort)
	tm.mu.RLock()
	onAbort := tm.onAbort
	tm.mu.RUnlock()

	// Handle transaction timeouts
	for _, info := range timeouts {
		tm.log.Warn("Transaction timeout",
			"txn_id", info.ID,
			"session_id", info.SessionID,
			"duration", now.Sub(info.StartTime),
		)
		tm.SetStatus(info.ID, TxnAborted)
		// Call per-transaction abort function if set
		info.mu.RLock()
		abortFn := info.AbortFunc
		info.mu.RUnlock()
		if abortFn != nil {
			abortFn()
		}
		if onAbort != nil {
			onAbort(info.SessionID)
		}
	}

	// Handle idle timeouts
	for _, info := range idleTimeouts {
		tm.log.Warn("Transaction idle timeout",
			"txn_id", info.ID,
			"session_id", info.SessionID,
			"idle_time", now.Sub(info.LastActivity),
		)
		tm.SetStatus(info.ID, TxnIdle)
		// Call per-transaction abort function if set
		info.mu.RLock()
		abortFn := info.AbortFunc
		info.mu.RUnlock()
		if abortFn != nil {
			abortFn()
		}
		if onAbort != nil {
			onAbort(info.SessionID)
		}
	}
}

// GetActiveCount returns the number of active transactions.
func (tm *TransactionManager) GetActiveCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	count := 0
	for _, info := range tm.transactions {
		info.mu.RLock()
		if info.Status == TxnActive {
			count++
		}
		info.mu.RUnlock()
	}
	return count
}

// GetStats returns transaction statistics.
func (tm *TransactionManager) GetStats() TransactionStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats := TransactionStats{
		TotalCount: len(tm.transactions),
	}

	for _, info := range tm.transactions {
		info.mu.RLock()
		switch info.Status {
		case TxnActive:
			stats.ActiveCount++
		case TxnIdle:
			stats.IdleCount++
		case TxnAborted:
			stats.AbortedCount++
		case TxnCommitted:
			stats.CommittedCount++
		}
		info.mu.RUnlock()
	}

	return stats
}

// GetActiveTransactions returns a list of all active transactions.
func (tm *TransactionManager) GetActiveTransactions() []*TransactionInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var activeTxns []*TransactionInfo
	now := time.Now()

	for _, info := range tm.transactions {
		info.mu.RLock()
		if info.Status == TxnActive {
			// Create a copy of the transaction info
			txnCopy := &TransactionInfo{
				ID:           info.ID,
				SessionID:    info.SessionID,
				StartTime:    info.StartTime,
				LastActivity: info.LastActivity,
				ServerConnID: info.ServerConnID,
				Status:       info.Status,
			}
			// Copy atomic values
			txnCopy.QueryCount.Store(info.QueryCount.Load())

			// Calculate duration
			duration := now.Sub(info.StartTime)

			activeTxns = append(activeTxns, txnCopy)

			tm.log.Debug("Active transaction",
				"txn_id", info.ID,
				"session_id", info.SessionID,
				"duration", duration,
				"queries", info.QueryCount.Load(),
			)
		}
		info.mu.RUnlock()
	}

	return activeTxns
}

// GetTransactionDetails returns detailed information about a specific transaction.
func (tm *TransactionManager) GetTransactionDetails(txnID uint64) *TransactionInfo {
	tm.mu.RLock()
	info, exists := tm.transactions[txnID]
	tm.mu.RUnlock()

	if !exists {
		return nil
	}

	// Create a copy to return
	info.mu.RLock()
	copy := &TransactionInfo{
		ID:           info.ID,
		SessionID:    info.SessionID,
		StartTime:    info.StartTime,
		LastActivity: info.LastActivity,
		ServerConnID: info.ServerConnID,
		Status:       info.Status,
	}
	copy.QueryCount.Store(info.QueryCount.Load())
	info.mu.RUnlock()

	return copy
}

// Stop stops the transaction manager.
func (tm *TransactionManager) Stop() {
	close(tm.stopCh)
}

// TransactionStats contains transaction statistics.
type TransactionStats struct {
	TotalCount     int `json:"total_count"`
	ActiveCount    int `json:"active_count"`
	IdleCount      int `json:"idle_count"`
	AbortedCount   int `json:"aborted_count"`
	CommittedCount int `json:"committed_count"`
}

// generateTxnID generates a unique transaction ID.
var txnIDCounter atomic.Uint64

func generateTxnID() uint64 {
	return txnIDCounter.Add(1)
}

// ContextWithTransactionTimeout creates a context with transaction timeout.
func ContextWithTransactionTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}
