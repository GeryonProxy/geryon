// Package raft implements the Raft consensus algorithm from scratch for
// configuration replication and leader election across Geryon proxy cluster
// nodes. It includes log replication, snapshot support, and a write-ahead
// log for durability.
package raft

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// Global random number generator, seeded with crypto/rand on init.
var globalRand = newGlobalRand()

func newGlobalRand() *simpleRand {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		b = []byte{0, 0, 0, 0, 0, 0, 0, 1} // Fallback
	}
	return &simpleRand{seed: binary.LittleEndian.Uint64(b)}
}

// NodeState represents the state of a Raft node.
type NodeState int

const (
	StateFollower NodeState = iota
	StateCandidate
	StateLeader
)

func (s NodeState) String() string {
	switch s {
	case StateFollower:
		return "Follower"
	case StateCandidate:
		return "Candidate"
	case StateLeader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// Entry represents a log entry.
type Entry struct {
	Term    uint64          `json:"term"`
	Index   uint64          `json:"index"`
	Command json.RawMessage `json:"command"`
}

// Node represents a Raft node.
type Node struct {
	id          string
	state       atomic.Value // NodeState
	currentTerm atomic.Uint64
	votedFor    atomic.Value // string
	leaderID    atomic.Value // string - tracks the current leader
	logEntries  []Entry
	logMu       sync.RWMutex

	// Volatile state
	commitIndex atomic.Uint64
	lastApplied atomic.Uint64
	nextIndex   map[string]uint64
	matchIndex  map[string]uint64
	volatileMu  sync.RWMutex

	// Election state (reset each term)
	votesReceived map[string]bool // peer ID -> voted for us
	votesMu       sync.Mutex

	// Configuration
	peers      []string
	listenAddr string
	listener   net.Listener
	connSem    chan struct{} // Bounded goroutine semaphore (H-4 fix)
	secret     string        // C-2 fix: shared secret for inter-node auth
	tlsConfig  *tls.Config   // C-2 fix: TLS config for inter-node encryption
	dataDir    string

	// Timing
	electionTimeout   time.Duration
	heartbeatInterval time.Duration
	electionTimer     *time.Timer
	heartbeatTicker   *time.Ticker

	// Channels
	stopCh  chan struct{}
	msgCh   chan Message
	applyCh chan Entry // Channel for committed entries to apply

	// Components
	wal               *WAL
	fsm               FSM
	snapshotStore     *SnapshotStore
	lastSnapshotIndex atomic.Uint64
	lastSnapshotTerm  atomic.Uint64

	// Logger
	logger *logger.Logger
}

// Message represents a Raft message.
type Message struct {
	Type      MessageType `json:"type"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Term      uint64      `json:"term"`
	Data      []byte      `json:"data"`
	Signature string      `json:"signature"` // C-2 fix: HMAC-SHA256 signature
}

// MessageType represents the type of Raft message.
type MessageType int

const (
	MsgVoteRequest MessageType = iota
	MsgVoteResponse
	MsgAppendEntries
	MsgAppendEntriesResponse
	MsgInstallSnapshot
	MsgInstallSnapshotResponse
)

// Maximum raft message payload size (1MB)
const maxRaftMessageSize = 1 << 20

// VoteRequest represents a request for votes.
type VoteRequest struct {
	Term         uint64 `json:"term"`
	CandidateID  string `json:"candidate_id"`
	LastLogIndex uint64 `json:"last_log_index"`
	LastLogTerm  uint64 `json:"last_log_term"`
}

// VoteResponse represents a vote response.
type VoteResponse struct {
	Term        uint64 `json:"term"`
	VoteGranted bool   `json:"vote_granted"`
}

// AppendEntries represents a request to append entries.
type AppendEntries struct {
	Term         uint64  `json:"term"`
	LeaderID     string  `json:"leader_id"`
	PrevLogIndex uint64  `json:"prev_log_index"`
	PrevLogTerm  uint64  `json:"prev_log_term"`
	Entries      []Entry `json:"entries"`
	LeaderCommit uint64  `json:"leader_commit"`
}

// AppendEntriesResponse represents a response to append entries.
type AppendEntriesResponse struct {
	Term    uint64 `json:"term"`
	Success bool   `json:"success"`
	Index   uint64 `json:"index"`
}

// InstallSnapshotRequest represents a request to install a snapshot.
type InstallSnapshotRequest struct {
	Term              uint64 `json:"term"`
	LeaderID          string `json:"leader_id"`
	LastIncludedIndex uint64 `json:"last_included_index"`
	LastIncludedTerm  uint64 `json:"last_included_term"`
	Offset            uint64 `json:"offset"`
	Data              []byte `json:"data"`
	Done              bool   `json:"done"`
}

// InstallSnapshotResponse represents a response to install snapshot.
type InstallSnapshotResponse struct {
	Term    uint64 `json:"term"`
	Success bool   `json:"success"`
}

// NewNode creates a new Raft node.
// C-2 fix: tlsConfig enables TLS for inter-node communication. Pass nil for plaintext.
func NewNode(id, listenAddr string, peers []string, dataDir string, secret string, tlsConfig *tls.Config, fsm FSM, log *logger.Logger) (*Node, error) {
	n := &Node{
		id:                id,
		listenAddr:        listenAddr,
		peers:             peers,
		secret:            secret, // C-2 fix
		tlsConfig:         tlsConfig,
		dataDir:           dataDir,
		electionTimeout:   1 * time.Second,
		heartbeatInterval: 100 * time.Millisecond,
		logEntries:        make([]Entry, 0),
		nextIndex:         make(map[string]uint64),
		matchIndex:        make(map[string]uint64),
		votesReceived:     make(map[string]bool),
		stopCh:            make(chan struct{}),
		msgCh:             make(chan Message, 100),
		applyCh:           make(chan Entry, 100),
		fsm:               fsm,
		logger:            log,
	}
	n.state.Store(StateFollower)
	n.votedFor.Store("")
	n.leaderID.Store("")

	// Initialize WAL
	walPath := dataDir + "/raft.log"
	wal, err := NewWAL(walPath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAL: %w", err)
	}
	n.wal = wal

	// Load existing log entries from WAL
	entries, err := wal.ReadEntries(1)
	if err != nil {
		wal.Close()
		return nil, fmt.Errorf("failed to read WAL: %w", err)
	}
	n.logEntries = entries

	// Initialize snapshot store
	snapshotDir := dataDir + "/snapshots"
	snapshotStore, err := NewSnapshotStore(snapshotDir, 3)
	if err != nil {
		wal.Close()
		return nil, fmt.Errorf("failed to create snapshot store: %w", err)
	}
	n.snapshotStore = snapshotStore

	// Try to load latest snapshot
	if snapshot, err := snapshotStore.Load(); err == nil {
		n.lastSnapshotIndex.Store(snapshot.Metadata.Index)
		n.lastSnapshotTerm.Store(snapshot.Metadata.Term)
		// Restore FSM from snapshot
		if fsm != nil {
			if err := fsm.Restore(snapshot.Data); err != nil {
				log.Error("Failed to restore FSM from snapshot", "error", err)
			}
		}
	}

	return n, nil
}

// ID returns the node ID.
func (n *Node) ID() string {
	return n.id
}

// State returns the current state.
func (n *Node) State() NodeState {
	return n.state.Load().(NodeState)
}

// SetStateForTest sets the node state. For use in tests only.
func (n *Node) SetStateForTest(state NodeState) {
	n.state.Store(state)
}

// CloseWAL closes the underlying WAL. For cleanup in tests.
func (n *Node) CloseWAL() {
	if n.wal != nil {
		n.wal.Close()
	}
}

// IsLeader returns true if this node is the leader.
func (n *Node) IsLeader() bool {
	return n.State() == StateLeader
}

// GetLeaderID returns the current leader's node ID, or empty string if unknown.
func (n *Node) GetLeaderID() string {
	return n.leaderID.Load().(string)
}

// CurrentTerm returns the current term.
func (n *Node) CurrentTerm() uint64 {
	return n.currentTerm.Load()
}

// Start starts the Raft node.
func (n *Node) Start() error {
	// Start TCP listener
	listener, err := net.Listen("tcp", n.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	// C-2 fix: Wrap with TLS if configured
	if n.tlsConfig != nil {
		listener = tls.NewListener(listener, n.tlsConfig)
	}

	n.listener = listener
	n.connSem = make(chan struct{}, 100) // H-4 fix: bounded goroutine limit

	n.logger.Info("Raft node starting",
		"id", n.id,
		"listen", n.listenAddr,
		"peers", n.peers,
		"tls", n.tlsConfig != nil,
	)

	// Start message handler
	go n.run()

	// Start accepting connections
	go n.acceptLoop()

	return nil
}

// Stop stops the Raft node.
func (n *Node) Stop() error {
	close(n.stopCh)
	if n.listener != nil {
		n.listener.Close()
	}
	return nil
}

// run is the main event loop.
func (n *Node) run() {
	// Start election timer
	n.resetElectionTimer()

	// Start apply committed goroutine
	go n.ApplyCommitted()

	for {
		select {
		case <-n.stopCh:
			return
		case msg := <-n.msgCh:
			n.handleMessage(msg)
		case <-n.electionTimer.C:
			n.onElectionTimeout()
		}
	}
}

// acceptLoop accepts incoming connections.
func (n *Node) acceptLoop() {
	for {
		conn, err := n.listener.Accept()
		if err != nil {
			if n.isStopping() {
				return
			}
			n.logger.Error("Failed to accept connection", "error", err)
			continue
		}

		// H-4 fix: bounded goroutine creation
		select {
		case n.connSem <- struct{}{}:
		default:
			conn.Close() // Reject if at limit
			continue
		}
		go func() {
			defer func() { <-n.connSem }()
			n.handleConnection(conn)
		}()
	}
}

// handleConnection handles a single connection.
func (n *Node) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Set read deadline to prevent slowloris
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	decoder := json.NewDecoder(io.LimitReader(conn, maxRaftMessageSize))
	for {
		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			return
		}

		// C-2 fix: verify HMAC signature if secret is configured
		// Must reconstruct the same JSON envelope that sendMessage signed
		if n.secret != "" {
			envelope, _ := json.Marshal(map[string]interface{}{
				"type": msg.Type,
				"from": msg.From,
				"to":   msg.To,
				"term": msg.Term,
				"data": string(msg.Data),
			})
			if !verifyHMAC(msg.Signature, n.secret, envelope) {
				n.logger.Warn("Rejected Raft message: invalid HMAC signature")
				return
			}
		}

		// Reset deadline for next read
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		n.msgCh <- msg
	}
}

// handleMessage processes a Raft message.
func (n *Node) handleMessage(msg Message) {
	// Check term
	if msg.Term > n.currentTerm.Load() {
		n.becomeFollower(msg.Term)
	}

	switch msg.Type {
	case MsgVoteRequest:
		n.handleVoteRequest(msg)
	case MsgVoteResponse:
		n.handleVoteResponse(msg)
	case MsgAppendEntries:
		n.handleAppendEntries(msg)
	case MsgAppendEntriesResponse:
		n.handleAppendEntriesResponse(msg)
	case MsgInstallSnapshot:
		n.handleInstallSnapshot(msg)
	}
}

// handleVoteRequest handles a vote request.
func (n *Node) handleVoteRequest(msg Message) {
	var req VoteRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		n.logger.Error("Failed to unmarshal vote request", "error", err)
		return
	}

	voteGranted := false

	if req.Term >= n.currentTerm.Load() {
		// Check if we can vote for this candidate
		votedFor := n.votedFor.Load().(string)
		if votedFor == "" || votedFor == req.CandidateID {
			// Check if candidate's log is at least as up-to-date
			lastLogIndex, lastLogTerm := n.lastLogInfo()
			if req.LastLogTerm > lastLogTerm ||
				(req.LastLogTerm == lastLogTerm && req.LastLogIndex >= lastLogIndex) {
				voteGranted = true
				n.votedFor.Store(req.CandidateID)
				n.resetElectionTimer()
			}
		}
	}

	// Send response
	resp := VoteResponse{
		Term:        n.currentTerm.Load(),
		VoteGranted: voteGranted,
	}
	n.sendMessage(msg.From, MsgVoteResponse, resp)
}

// handleVoteResponse handles a vote response.
func (n *Node) handleVoteResponse(msg Message) {
	if n.State() != StateCandidate {
		return
	}

	var resp VoteResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		n.logger.Error("Failed to unmarshal vote response", "error", err)
		return
	}

	if resp.Term > n.currentTerm.Load() {
		n.becomeFollower(resp.Term)
		return
	}

	if resp.VoteGranted {
		// Track vote
		n.votesMu.Lock()
		n.votesReceived[msg.From] = true
		voteCount := len(n.votesReceived) + 1 // +1 for our own vote
		total := len(n.peers) + 1
		n.votesMu.Unlock()

		// Check if we have majority
		if voteCount > total/2 {
			n.becomeLeader()
		}
	}
}

// handleAppendEntries handles append entries request.
func (n *Node) handleAppendEntries(msg Message) {
	var req AppendEntries
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		n.logger.Error("Failed to unmarshal append entries", "error", err)
		return
	}

	success := false
	if req.Term >= n.currentTerm.Load() {
		n.resetElectionTimer()
		n.votedFor.Store(req.LeaderID)
		n.leaderID.Store(req.LeaderID)

		// Check if previous log entry matches
		if req.PrevLogIndex == 0 || n.hasLogEntry(req.PrevLogIndex, req.PrevLogTerm) {
			success = true

			// Append new entries, tracking which are actually new
			var newEntries []Entry
			for _, entry := range req.Entries {
				if n.appendEntry(entry) {
					newEntries = append(newEntries, entry)
				}
			}

			// Persist only genuinely new entries to WAL
			if n.wal != nil && len(newEntries) > 0 {
				if err := n.wal.AppendBatch(newEntries); err != nil {
					n.logger.Error("Failed to persist entries to WAL", "error", err)
				}
			}

			// Update commit index
			if req.LeaderCommit > n.commitIndex.Load() {
				lastIndex, _ := n.lastLogInfo()
				if req.LeaderCommit > lastIndex {
					n.commitIndex.Store(lastIndex)
				} else {
					n.commitIndex.Store(req.LeaderCommit)
				}

				// Send newly committed entries to applyCh
				n.sendCommittedToApply()
			}
		}
	}

	resp := AppendEntriesResponse{
		Term:    n.currentTerm.Load(),
		Success: success,
		Index:   n.lastLogIndex(),
	}
	n.sendMessage(msg.From, MsgAppendEntriesResponse, resp)
}

// handleAppendEntriesResponse handles append entries response.
func (n *Node) handleAppendEntriesResponse(msg Message) {
	if n.State() != StateLeader {
		return
	}

	var resp AppendEntriesResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		n.logger.Error("Failed to unmarshal append entries response", "error", err)
		return
	}

	if resp.Term > n.currentTerm.Load() {
		n.becomeFollower(resp.Term)
		return
	}

	n.volatileMu.Lock()
	if resp.Success {
		n.matchIndex[msg.From] = resp.Index
		n.nextIndex[msg.From] = resp.Index + 1

		// Check if we can advance commit index
		n.advanceCommitIndex()
	} else {
		// Decrement next index and retry
		if n.nextIndex[msg.From] > 1 {
			n.nextIndex[msg.From]--
		}
	}
	n.volatileMu.Unlock()
}

// advanceCommitIndex advances the commit index if a majority of nodes have replicated an entry.
func (n *Node) advanceCommitIndex() {
	commitIdx := n.commitIndex.Load()
	currentTerm := n.currentTerm.Load()

	// Find the highest index that is replicated on a majority
	matchIndices := make([]uint64, 0, len(n.peers)+1)
	matchIndices = append(matchIndices, n.lastLogIndex()) // Leader always has its own entries
	for _, idx := range n.matchIndex {
		matchIndices = append(matchIndices, idx)
	}

	// Sort in descending order
	sort.Slice(matchIndices, func(i, j int) bool {
		return matchIndices[i] > matchIndices[j]
	})

	// Majority index
	majorityIdx := len(matchIndices) / 2
	if majorityIdx >= len(matchIndices) {
		return
	}

	newCommitIdx := matchIndices[majorityIdx]
	if newCommitIdx <= commitIdx {
		return
	}

	// Check if entry at newCommitIdx is from current term
	n.logMu.RLock()
	var entryTerm uint64
	for _, entry := range n.logEntries {
		if entry.Index == newCommitIdx {
			entryTerm = entry.Term
			break
		}
	}
	n.logMu.RUnlock()

	// Only advance if entry is from current term (Raft safety property)
	if entryTerm == currentTerm {
		n.commitIndex.Store(newCommitIdx)
		n.sendCommittedToApply()
	}
}

// becomeFollower transitions to follower state.
func (n *Node) becomeFollower(term uint64) {
	n.currentTerm.Store(term)
	n.state.Store(StateFollower)
	n.votedFor.Store("")
	n.leaderID.Store("") // Clear leader until we hear from the new one
	n.votesMu.Lock()
	n.votesReceived = make(map[string]bool)
	n.votesMu.Unlock()
	n.stopHeartbeat()
	n.resetElectionTimer()

	n.logger.Info("Became follower", "term", term)
}

// becomeCandidate transitions to candidate state.
func (n *Node) becomeCandidate() {
	n.currentTerm.Add(1)
	n.state.Store(StateCandidate)
	n.votedFor.Store(n.id)
	n.leaderID.Store("") // Clear leader since we're starting a new election

	// Reset vote tracking for this election
	n.votesMu.Lock()
	n.votesReceived = make(map[string]bool)
	n.votesMu.Unlock()

	n.logger.Info("Became candidate", "term", n.currentTerm.Load())

	// Request votes from all peers
	lastIndex, lastTerm := n.lastLogInfo()
	req := VoteRequest{
		Term:         n.currentTerm.Load(),
		CandidateID:  n.id,
		LastLogIndex: lastIndex,
		LastLogTerm:  lastTerm,
	}

	for _, peer := range n.peers {
		n.sendMessage(peer, MsgVoteRequest, req)
	}

	n.resetElectionTimer()
}

// becomeLeader transitions to leader state.
func (n *Node) becomeLeader() {
	n.state.Store(StateLeader)

	n.volatileMu.Lock()
	lastIndex := n.lastLogIndex()
	for _, peer := range n.peers {
		n.nextIndex[peer] = lastIndex + 1
		n.matchIndex[peer] = 0
	}
	n.volatileMu.Unlock()

	n.logger.Info("Became leader", "term", n.currentTerm.Load())

	// Start sending heartbeats
	n.startHeartbeat()
}

// onElectionTimeout handles election timeout.
func (n *Node) onElectionTimeout() {
	if n.State() == StateLeader {
		return
	}

	n.logger.Info("Election timeout, starting election")
	n.becomeCandidate()
}

// resetElectionTimer resets the election timer.
func (n *Node) resetElectionTimer() {
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}
	// Randomize timeout: 150-300ms
	timeout := n.electionTimeout + time.Duration(getRand().Int63n(int64(n.electionTimeout)))
	n.electionTimer = time.NewTimer(timeout)
}

// startHeartbeat starts sending heartbeats.
func (n *Node) startHeartbeat() {
	n.heartbeatTicker = time.NewTicker(n.heartbeatInterval)

	go func() {
		for {
			select {
			case <-n.stopCh:
				return
			case <-n.heartbeatTicker.C:
				n.sendHeartbeats()
			}
		}
	}()
}

// stopHeartbeat stops sending heartbeats.
func (n *Node) stopHeartbeat() {
	if n.heartbeatTicker != nil {
		n.heartbeatTicker.Stop()
		n.heartbeatTicker = nil
	}
}

// sendHeartbeats sends heartbeat messages to all peers.
func (n *Node) sendHeartbeats() {
	if n.State() != StateLeader {
		return
	}

	prevIndex, prevTerm := n.lastLogInfo()
	req := AppendEntries{
		Term:         n.currentTerm.Load(),
		LeaderID:     n.id,
		PrevLogIndex: prevIndex,
		PrevLogTerm:  prevTerm,
		Entries:      []Entry{}, // Empty for heartbeat
		LeaderCommit: n.commitIndex.Load(),
	}

	for _, peer := range n.peers {
		n.sendMessage(peer, MsgAppendEntries, req)
	}
}

// sendMessage sends a message to a peer.
func (n *Node) sendMessage(to string, msgType MessageType, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		n.logger.Error("Failed to marshal message", "error", err)
		return
	}

	// C-2 fix: Sign message with HMAC-SHA256
	// Capture term once to avoid mismatch between msg.Term and HMAC payload
	term := n.currentTerm.Load()
	msg := Message{
		Type: msgType,
		From: n.id,
		To:   to,
		Term: term,
		Data: jsonData,
	}

	if n.secret != "" {
		msgData, _ := json.Marshal(map[string]interface{}{
			"type": msgType,
			"from": n.id,
			"to":   to,
			"term": term,
			"data": string(jsonData),
		})
		msg.Signature = computeHMAC(n.secret, msgData)
	}

	// Connect and send
	// C-2 fix: Use TLS dial if configured
	var conn net.Conn
	if n.tlsConfig != nil {
		conn, err = tls.Dial("tcp", to, n.tlsConfig)
	} else {
		conn, err = net.Dial("tcp", to)
	}
	if err != nil {
		n.logger.Debug("Failed to connect to peer", "peer", to, "error", err)
		return
	}
	defer conn.Close()

	// Set write deadline to prevent hanging on slow peers
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		n.logger.Debug("Failed to send message", "peer", to, "error", err)
	}
}

// lastLogInfo returns the last log index and term.
func (n *Node) lastLogInfo() (uint64, uint64) {
	n.logMu.RLock()
	defer n.logMu.RUnlock()

	if len(n.logEntries) == 0 {
		return 0, 0
	}

	last := n.logEntries[len(n.logEntries)-1]
	return last.Index, last.Term
}

// lastLogIndex returns the last log index.
func (n *Node) lastLogIndex() uint64 {
	idx, _ := n.lastLogInfo()
	return idx
}

// hasLogEntry checks if a log entry exists.
func (n *Node) hasLogEntry(index, term uint64) bool {
	n.logMu.RLock()
	defer n.logMu.RUnlock()

	for _, entry := range n.logEntries {
		if entry.Index == index && entry.Term == term {
			return true
		}
	}
	return false
}

// appendEntry appends an entry to the log.
// Returns true if the entry was newly added (not already present).
func (n *Node) appendEntry(entry Entry) bool {
	n.logMu.Lock()
	defer n.logMu.Unlock()

	// Check if entry already exists
	for i, e := range n.logEntries {
		if e.Index == entry.Index {
			// Replace if terms differ
			if e.Term != entry.Term {
				n.logEntries = n.logEntries[:i]
				n.logEntries = append(n.logEntries, entry)
				return true
			}
			return false
		}
	}

	// Append new entry
	n.logEntries = append(n.logEntries, entry)
	return true
}

// hasMajority checks if we have a majority of votes.
func (n *Node) hasMajority() bool {
	n.votesMu.Lock()
	defer n.votesMu.Unlock()
	total := len(n.peers) + 1
	votes := len(n.votesReceived) + 1 // +1 for our own vote
	return votes > total/2
}

// isStopping checks if the node is stopping.
func (n *Node) isStopping() bool {
	select {
	case <-n.stopCh:
		return true
	default:
		return false
	}
}

// Propose proposes a command to be committed by Raft.
// Only the leader can propose commands.
func (n *Node) Propose(command Command) (uint64, error) {
	if n.State() != StateLeader {
		return 0, fmt.Errorf("not leader")
	}

	// Create log entry
	data, err := json.Marshal(command)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal command: %w", err)
	}

	n.logMu.Lock()
	var lastIndex uint64
	if len(n.logEntries) > 0 {
		lastIndex = n.logEntries[len(n.logEntries)-1].Index
	}
	entry := Entry{
		Term:    n.currentTerm.Load(),
		Index:   lastIndex + 1,
		Command: data,
	}
	n.logEntries = append(n.logEntries, entry)
	n.logMu.Unlock()

	// Persist to WAL
	if err := n.wal.Append(entry); err != nil {
		return 0, fmt.Errorf("failed to append to WAL: %w", err)
	}

	// Replicate to followers
	n.sendAppendEntriesToAll()

	return entry.Index, nil
}

// sendAppendEntriesToAll sends AppendEntries to all peers.
func (n *Node) sendAppendEntriesToAll() {
	n.logMu.RLock()
	lastIndex, _ := n.lastLogInfo()
	n.logMu.RUnlock()

	for _, peer := range n.peers {
		n.volatileMu.RLock()
		nextIdx := n.nextIndex[peer]
		n.volatileMu.RUnlock()

		// Calculate previous index and term
		prevIndex := nextIdx - 1
		prevTerm := uint64(0)
		if prevIndex > 0 {
			n.logMu.RLock()
			for _, entry := range n.logEntries {
				if entry.Index == prevIndex {
					prevTerm = entry.Term
					break
				}
			}
			n.logMu.RUnlock()
		}

		// Get entries to send
		var entries []Entry
		n.logMu.RLock()
		for _, entry := range n.logEntries {
			if entry.Index >= nextIdx && entry.Index <= lastIndex {
				entries = append(entries, entry)
			}
		}
		n.logMu.RUnlock()

		req := AppendEntries{
			Term:         n.currentTerm.Load(),
			LeaderID:     n.id,
			PrevLogIndex: prevIndex,
			PrevLogTerm:  prevTerm,
			Entries:      entries,
			LeaderCommit: n.commitIndex.Load(),
		}

		n.sendMessage(peer, MsgAppendEntries, req)
	}
}

// ApplyCommitted applies committed entries to the FSM.
func (n *Node) ApplyCommitted() {
	for entry := range n.applyCh {
		if n.fsm != nil {
			var cmd Command
			if err := json.Unmarshal(entry.Command, &cmd); err != nil {
				n.logger.Error("Failed to unmarshal command", "error", err)
				continue
			}

			if _, err := n.fsm.Apply(cmd); err != nil {
				n.logger.Error("Failed to apply command", "error", err, "index", entry.Index)
			}
		}

		n.lastApplied.Store(entry.Index)

		// Check if we should take a snapshot
		n.maybeSnapshot()
	}
}

// maybeSnapshot checks if we should take a snapshot.
func (n *Node) maybeSnapshot() {
	lastApplied := n.lastApplied.Load()
	lastSnapshot := n.lastSnapshotIndex.Load()

	// Take snapshot every 1000 entries
	if lastApplied-lastSnapshot < 1000 {
		return
	}

	if n.fsm == nil {
		return
	}

	// Get snapshot from FSM
	data, err := n.fsm.Snapshot()
	if err != nil {
		n.logger.Error("Failed to create snapshot", "error", err)
		return
	}

	n.logMu.RLock()
	var lastTerm uint64
	for _, entry := range n.logEntries {
		if entry.Index == lastApplied {
			lastTerm = entry.Term
			break
		}
	}
	n.logMu.RUnlock()

	snapshot := CreateSnapshot(lastApplied, lastTerm, data)

	if err := n.snapshotStore.Save(snapshot); err != nil {
		n.logger.Error("Failed to save snapshot", "error", err)
		return
	}

	n.lastSnapshotIndex.Store(lastApplied)
	n.lastSnapshotTerm.Store(lastTerm)

	// Truncate WAL
	if err := n.wal.Truncate(lastApplied + 1); err != nil {
		n.logger.Error("Failed to truncate WAL", "error", err)
	}

	n.logger.Info("Snapshot created",
		"index", lastApplied,
		"term", lastTerm,
		"size", snapshot.Metadata.Size,
	)
}

// InstallSnapshot installs a snapshot on this node.
func (n *Node) InstallSnapshot(snapshot *Snapshot) error {
	if n.fsm == nil {
		return fmt.Errorf("no FSM configured")
	}

	// Save snapshot
	if err := n.snapshotStore.Save(snapshot); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	// Restore FSM
	if err := n.fsm.Restore(snapshot.Data); err != nil {
		return fmt.Errorf("failed to restore FSM: %w", err)
	}

	// Update state
	n.lastSnapshotIndex.Store(snapshot.Metadata.Index)
	n.lastSnapshotTerm.Store(snapshot.Metadata.Term)
	n.lastApplied.Store(snapshot.Metadata.Index)

	// Replace log with single entry at snapshot index
	n.logMu.Lock()
	n.logEntries = []Entry{{
		Term:  snapshot.Metadata.Term,
		Index: snapshot.Metadata.Index,
	}}
	n.logMu.Unlock()

	// Truncate WAL
	if err := n.wal.Truncate(snapshot.Metadata.Index + 1); err != nil {
		n.logger.Error("Failed to truncate WAL after snapshot", "error", err)
	}

	return nil
}

// GetSnapshot returns the latest snapshot.
func (n *Node) GetSnapshot() (*Snapshot, error) {
	return n.snapshotStore.Load()
}

// sendCommittedToApply sends committed entries to the apply channel.
func (n *Node) sendCommittedToApply() {
	commitIdx := n.commitIndex.Load()
	lastApplied := n.lastApplied.Load()

	if commitIdx <= lastApplied {
		return
	}

	n.logMu.RLock()
	entries := make([]Entry, 0)
	for _, entry := range n.logEntries {
		if entry.Index > lastApplied && entry.Index <= commitIdx {
			entries = append(entries, entry)
		}
	}
	n.logMu.RUnlock()

	for _, entry := range entries {
		select {
		case n.applyCh <- entry:
		default:
			n.logger.Warn("Apply channel full, dropping entry", "index", entry.Index)
		}
	}
}

// handleInstallSnapshot handles install snapshot request.
func (n *Node) handleInstallSnapshot(msg Message) {
	var req InstallSnapshotRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		n.logger.Error("Failed to unmarshal install snapshot", "error", err)
		return
	}

	if req.Term < n.currentTerm.Load() {
		// Reject stale snapshot
		resp := InstallSnapshotResponse{
			Term:    n.currentTerm.Load(),
			Success: false,
		}
		n.sendMessage(msg.From, MsgInstallSnapshotResponse, resp)
		return
	}

	// Reset election timer
	n.resetElectionTimer()
	n.votedFor.Store(req.LeaderID)

	// Create snapshot from request data
	snapshot := &Snapshot{
		Metadata: SnapshotMetadata{
			Index:    req.LastIncludedIndex,
			Term:     req.LastIncludedTerm,
			Size:     int64(len(req.Data)),
			Checksum: crc32.ChecksumIEEE(req.Data),
		},
		Data: req.Data,
	}

	// Install snapshot
	if err := n.InstallSnapshot(snapshot); err != nil {
		n.logger.Error("Failed to install snapshot", "error", err)
		resp := InstallSnapshotResponse{
			Term:    n.currentTerm.Load(),
			Success: false,
		}
		n.sendMessage(msg.From, MsgInstallSnapshotResponse, resp)
		return
	}

	resp := InstallSnapshotResponse{
		Term:    n.currentTerm.Load(),
		Success: true,
	}
	n.sendMessage(msg.From, MsgInstallSnapshotResponse, resp)
}

type simpleRand struct {
	mu   sync.Mutex
	seed uint64
}

func getRand() *simpleRand {
	return globalRand
}

func (r *simpleRand) Int63n(n int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Simple LCG
	r.seed = r.seed*6364136223846793005 + 1
	return int64(uint64(r.seed) % uint64(n))
}

// computeHMAC computes HMAC-SHA256 of data using the given secret.
func computeHMAC(secret string, data []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyHMAC verifies an HMAC-SHA256 signature.
func verifyHMAC(signature, secret string, data []byte) bool {
	expected := computeHMAC(secret, data)
	return hmac.Equal([]byte(signature), []byte(expected))
}
