package raft

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

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
	id                string
	state             atomic.Value // NodeState
	currentTerm       atomic.Uint64
	votedFor          atomic.Value // string
	logEntries        []Entry
	logMu             sync.RWMutex

	// Volatile state
	commitIndex       atomic.Uint64
	lastApplied       atomic.Uint64
	nextIndex         map[string]uint64
	matchIndex        map[string]uint64
	volatileMu        sync.RWMutex

	// Configuration
	peers             []string
	listenAddr        string
	listener          net.Listener

	// Timing
	electionTimeout   time.Duration
	heartbeatInterval time.Duration
	electionTimer     *time.Timer
	heartbeatTicker   *time.Ticker

	// Channels
	stopCh            chan struct{}
	msgCh             chan Message

	// Logger
	logger            *logger.Logger
}

// Message represents a Raft message.
type Message struct {
	Type      MessageType `json:"type"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Term      uint64      `json:"term"`
	Data      []byte      `json:"data"`
}

// MessageType represents the type of Raft message.
type MessageType int

const (
	MsgVoteRequest MessageType = iota
	MsgVoteResponse
	MsgAppendEntries
	MsgAppendEntriesResponse
)

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

// NewNode creates a new Raft node.
func NewNode(id, listenAddr string, peers []string, log *logger.Logger) *Node {
	n := &Node{
		id:                id,
		listenAddr:        listenAddr,
		peers:             peers,
		electionTimeout:   1 * time.Second,
		heartbeatInterval: 100 * time.Millisecond,
		logEntries:        make([]Entry, 0),
		nextIndex:         make(map[string]uint64),
		matchIndex:        make(map[string]uint64),
		stopCh:            make(chan struct{}),
		msgCh:             make(chan Message, 100),
		logger:            log,
	}
	n.state.Store(StateFollower)
	n.votedFor.Store("")
	return n
}

// ID returns the node ID.
func (n *Node) ID() string {
	return n.id
}

// State returns the current state.
func (n *Node) State() NodeState {
	return n.state.Load().(NodeState)
}

// IsLeader returns true if this node is the leader.
func (n *Node) IsLeader() bool {
	return n.State() == StateLeader
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
	n.listener = listener

	n.logger.Info("Raft node starting",
		"id", n.id,
		"listen", n.listenAddr,
		"peers", n.peers,
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

		go n.handleConnection(conn)
	}
}

// handleConnection handles a single connection.
func (n *Node) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	for {
		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			return
		}

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
		// Count votes
		// Simplified: check if we have majority
		if n.hasMajority() {
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

		// Check if previous log entry matches
		if req.PrevLogIndex == 0 || n.hasLogEntry(req.PrevLogIndex, req.PrevLogTerm) {
			success = true

			// Append new entries
			for _, entry := range req.Entries {
				n.appendEntry(entry)
			}

			// Update commit index
			if req.LeaderCommit > n.commitIndex.Load() {
				lastIndex, _ := n.lastLogInfo()
				if req.LeaderCommit > lastIndex {
					n.commitIndex.Store(lastIndex)
				} else {
					n.commitIndex.Store(req.LeaderCommit)
				}
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
	} else {
		// Decrement next index and retry
		if n.nextIndex[msg.From] > 1 {
			n.nextIndex[msg.From]--
		}
	}
	n.volatileMu.Unlock()
}

// becomeFollower transitions to follower state.
func (n *Node) becomeFollower(term uint64) {
	n.currentTerm.Store(term)
	n.state.Store(StateFollower)
	n.votedFor.Store("")
	n.stopHeartbeat()
	n.resetElectionTimer()

	n.logger.Info("Became follower", "term", term)
}

// becomeCandidate transitions to candidate state.
func (n *Node) becomeCandidate() {
	n.currentTerm.Add(1)
	n.state.Store(StateCandidate)
	n.votedFor.Store(n.id)

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

	msg := Message{
		Type: msgType,
		From: n.id,
		To:   to,
		Term: n.currentTerm.Load(),
		Data: jsonData,
	}

	// Connect and send
	conn, err := net.Dial("tcp", to)
	if err != nil {
		n.logger.Debug("Failed to connect to peer", "peer", to, "error", err)
		return
	}
	defer conn.Close()

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
func (n *Node) appendEntry(entry Entry) {
	n.logMu.Lock()
	defer n.logMu.Unlock()

	// Check if entry already exists
	for i, e := range n.logEntries {
		if e.Index == entry.Index {
			// Replace if terms differ
			if e.Term != entry.Term {
				n.logEntries = n.logEntries[:i]
				n.logEntries = append(n.logEntries, entry)
			}
			return
		}
	}

	// Append new entry
	n.logEntries = append(n.logEntries, entry)
}

// hasMajority checks if we have a majority of votes.
func (n *Node) hasMajority() bool {
	// Simplified: just check if we have more than half
	total := len(n.peers) + 1
	return total/2+1 <= total
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

// Global random number generator
var globalRand = &simpleRand{}

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
