package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// TransactionManager manages active transactions and handles timeouts.
type TransactionManager struct {
	mu              sync.RWMutex
	transactions    map[uint64]*TransactionInfo
	timeout         time.Duration
	idleTimeout     time.Duration
	checkInterval   time.Duration
	stopCh          chan struct{}
	log             *logger.Logger
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
func NewTransactionManager(timeout, idleTimeout time.Duration, log *logger.Logger) *TransactionManager {
	if timeout == 0 {
		timeout = 30 * time.Minute // Default 30 min transaction timeout
	}
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute // Default 5 min idle timeout
	}

	tm := &TransactionManager{
		transactions:  make(map[uint64]*TransactionInfo),
		timeout:       timeout,
		idleTimeout:   idleTimeout,
		checkInterval: 30 * time.Second,
		stopCh:        make(chan struct{}),
		log:           log,
	}

	// Start background monitor
	go tm.monitorLoop()

	return tm
}

// Register registers a new transaction.
func (tm *TransactionManager) Register(sessionID, serverConnID uint64) *TransactionInfo {
	info := &TransactionInfo{
		ID:           generateTxnID(),
		SessionID:    sessionID,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		ServerConnID: serverConnID,
		Status:       TxnActive,
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

	// Handle transaction timeouts
	for _, info := range timeouts {
		tm.log.Warn("Transaction timeout",
			"txn_id", info.ID,
			"session_id", info.SessionID,
			"duration", now.Sub(info.StartTime),
		)
		tm.SetStatus(info.ID, TxnAborted)
	}

	// Handle idle timeouts
	for _, info := range idleTimeouts {
		tm.log.Warn("Transaction idle timeout",
			"txn_id", info.ID,
			"session_id", info.SessionID,
			"idle_time", now.Sub(info.LastActivity),
		)
		tm.SetStatus(info.ID, TxnIdle)
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

// DeadlockDetector detects potential deadlocks.
type DeadlockDetector struct {
	mu          sync.RWMutex
	waitGraph   map[uint64]map[uint64]bool // session -> sessions it's waiting for
	timeout     time.Duration
	log         *logger.Logger
}

// NewDeadlockDetector creates a new deadlock detector.
func NewDeadlockDetector(timeout time.Duration, log *logger.Logger) *DeadlockDetector {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &DeadlockDetector{
		waitGraph: make(map[uint64]map[uint64]bool),
		timeout:   timeout,
		log:       log,
	}
}

// AddWait adds a wait relationship.
func (dd *DeadlockDetector) AddWait(sessionID, waitingForSessionID uint64) {
	dd.mu.Lock()
	defer dd.mu.Unlock()

	if _, exists := dd.waitGraph[sessionID]; !exists {
		dd.waitGraph[sessionID] = make(map[uint64]bool)
	}
	dd.waitGraph[sessionID][waitingForSessionID] = true

	// Check for cycle
	if dd.detectCycle() {
		dd.log.Warn("Potential deadlock detected", "session", sessionID)
	}
}

// RemoveWait removes a wait relationship.
func (dd *DeadlockDetector) RemoveWait(sessionID, waitingForSessionID uint64) {
	dd.mu.Lock()
	defer dd.mu.Unlock()

	if waits, exists := dd.waitGraph[sessionID]; exists {
		delete(waits, waitingForSessionID)
		if len(waits) == 0 {
			delete(dd.waitGraph, sessionID)
		}
	}
}

// ClearSession removes all wait relationships for a session.
func (dd *DeadlockDetector) ClearSession(sessionID uint64) {
	dd.mu.Lock()
	defer dd.mu.Unlock()

	delete(dd.waitGraph, sessionID)

	// Remove references from other sessions
	for _, waits := range dd.waitGraph {
		delete(waits, sessionID)
	}
}

// detectCycle detects if there's a cycle in the wait graph.
func (dd *DeadlockDetector) detectCycle() bool {
	visited := make(map[uint64]bool)
	recStack := make(map[uint64]bool)

	for node := range dd.waitGraph {
		if !visited[node] {
			if dd.hasCycleDFS(node, visited, recStack) {
				return true
			}
		}
	}
	return false
}

// hasCycleDFS performs DFS to detect cycle.
func (dd *DeadlockDetector) hasCycleDFS(node uint64, visited, recStack map[uint64]bool) bool {
	visited[node] = true
	recStack[node] = true

	for neighbor := range dd.waitGraph[node] {
		if !visited[neighbor] {
			if dd.hasCycleDFS(neighbor, visited, recStack) {
				return true
			}
		} else if recStack[neighbor] {
			return true
		}
	}

	recStack[node] = false
	return false
}

// GetWaitingSessions returns sessions that a session is waiting for.
func (dd *DeadlockDetector) GetWaitingSessions(sessionID uint64) []uint64 {
	dd.mu.RLock()
	defer dd.mu.RUnlock()

	result := make([]uint64, 0)
	if waits, exists := dd.waitGraph[sessionID]; exists {
		for sid := range waits {
			result = append(result, sid)
		}
	}
	return result
}

// ContextWithTransactionTimeout creates a context with transaction timeout.
func ContextWithTransactionTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}
