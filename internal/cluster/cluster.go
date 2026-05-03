// Package cluster integrates Raft consensus with SWIM gossip to provide
// distributed cluster management for the Geryon proxy. It handles node
// discovery, leader election, configuration replication, and backend
// health sharing across cluster nodes.
package cluster

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// NodeState represents the state of a cluster node.
type NodeState string

const (
	NodeStateLeader    NodeState = "leader"
	NodeStateFollower  NodeState = "follower"
	NodeStateCandidate NodeState = "candidate"
	NodeStateDead      NodeState = "dead"
)

// Node represents a cluster node.
type Node struct {
	ID       string            `json:"id"`
	Address  string            `json:"address"`
	State    NodeState         `json:"state"`
	LastSeen time.Time         `json:"last_seen"`
	Meta     map[string]string `json:"meta"`
}

// Config represents cluster configuration.
type Config struct {
	NodeID            string
	ListenAddr        string
	Peers             []string
	Secret            string      // C-2 fix: shared secret for inter-node auth
	TLSConfig         *tls.Config // C-2 fix: TLS config for inter-node encryption
	ElectionTimeout   time.Duration
	HeartbeatInterval time.Duration
	Logger            *logger.Logger

	// OnBackendHealth is called when a backend health update is received from the leader.
	OnBackendHealth func(backendID string, healthy bool, sourceNode string)
}

// Cluster manages distributed consensus and node membership.
type Cluster struct {
	mu sync.RWMutex

	config Config
	nodeID string
	state  NodeState

	// Raft state
	currentTerm   uint64
	votedFor      string
	leaderID      string
	votesReceived map[string]bool
	logEntries    []LogEntry
	commitIndex   uint64

	// Membership
	nodes map[string]*Node
	self  *Node

	// SWIM gossip
	gossip *SwimGossip

	// Channels
	rpcCh      chan RPC
	shutdownCh chan struct{}
	doneCh     chan struct{}
	rpcSem     chan struct{} // H-4 fix: bounded goroutine semaphore

	log *logger.Logger
}

// RPC represents a cluster RPC message.
type RPC struct {
	From      string
	Type      string
	Payload   []byte
	Signature string // C-2 fix: HMAC-SHA256 signature
}

// Maximum RPC payload size (1MB)
const maxRPCPayloadSize = 1 << 20

// Raft RPC types
const (
	RPCVoteRequest     = "VoteRequest"
	RPCVoteResponse    = "VoteResponse"
	RPCAppendEntries   = "AppendEntries"
	RPCHeartbeat       = "Heartbeat"
	RPCInstallSnapshot = "InstallSnapshot"
	RPCJoin            = "Join"
	RPCPingReq         = "PingReq"
	RPCBackendHealth   = "backend_health"
)

// C-2 fix: computeHMAC computes an HMAC-SHA256 signature for cluster messages.
func computeHMAC(secret, rpcType, from string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(rpcType))
	mac.Write([]byte(from))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// SwimGossip implements the SWIM protocol for failure detection.
type SwimGossip struct {
	mu sync.RWMutex

	cluster           *Cluster
	protocolPeriod    time.Duration
	probeTimeout      time.Duration
	numIndirectProbes int

	// Failure suspicion
	suspected map[string]time.Time
	alive     map[string]time.Time
	// CRIT-1 fix: Semaphore to bound concurrent probe goroutines
	probeSem chan struct{}
}

// New creates a new cluster instance.
func New(config Config) *Cluster {
	if config.ElectionTimeout == 0 {
		config.ElectionTimeout = 1 * time.Second
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 150 * time.Millisecond
	}

	c := &Cluster{
		config:        config,
		nodeID:        config.NodeID,
		state:         NodeStateFollower,
		nodes:         make(map[string]*Node),
		votesReceived: make(map[string]bool),
		rpcCh:         make(chan RPC, 100),
		shutdownCh:    make(chan struct{}),
		doneCh:        make(chan struct{}),
		rpcSem:        make(chan struct{}, 100), // H-4 fix: bounded goroutine limit
		log:           config.Logger,
	}

	c.self = &Node{
		ID:       config.NodeID,
		Address:  config.ListenAddr,
		State:    NodeStateFollower,
		LastSeen: time.Now(),
		Meta:     make(map[string]string),
	}

	c.nodes[config.NodeID] = c.self

	// Initialize SWIM gossip
	c.gossip = &SwimGossip{
		cluster:           c,
		protocolPeriod:    1 * time.Second,
		probeTimeout:      500 * time.Millisecond,
		numIndirectProbes: 3,
		suspected:         make(map[string]time.Time),
		alive:             make(map[string]time.Time),
		probeSem:          make(chan struct{}, 10),
	}

	return c
}

// Start starts the cluster services.
func (c *Cluster) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Start RPC listener
	go c.serveRPC()

	// Start Raft
	go c.raftLoop()

	// Start SWIM gossip
	go c.gossip.run()

	// Join peers if specified
	if len(c.config.Peers) > 0 {
		go c.joinPeers()
	}

	c.log.Info("Cluster started", "node_id", c.nodeID, "listen", c.config.ListenAddr)
	return nil
}

// Stop stops the cluster services.
func (c *Cluster) Stop() error {
	close(c.shutdownCh)
	close(c.doneCh)

	c.log.Info("Cluster stopped", "node_id", c.nodeID)
	return nil
}

// serveRPC handles incoming RPC connections.
func (c *Cluster) serveRPC() {
	listener, err := net.Listen("tcp", c.config.ListenAddr)
	if err != nil {
		c.log.Error("Failed to start cluster listener", "error", err)
		return
	}
	defer listener.Close()

	// C-2 fix: Wrap with TLS if configured
	if c.config.TLSConfig != nil {
		listener = tls.NewListener(listener, c.config.TLSConfig)
	}

	go func() {
		<-c.shutdownCh
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			c.log.Error("Failed to accept cluster connection", "error", err)
			continue
		}

		// H-4 fix: bounded goroutine creation
		select {
		case c.rpcSem <- struct{}{}:
		default:
			conn.Close() // Reject if at limit
			continue
		}
		go func() {
			defer func() { <-c.rpcSem }()
			c.handleRPC(conn)
		}()
	}
}

// handleRPC handles a single RPC connection.
func (c *Cluster) handleRPC(conn net.Conn) {
	defer conn.Close()

	// Set read deadline to prevent slowloris
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Read with bounded size
	decoder := json.NewDecoder(io.LimitReader(conn, maxRPCPayloadSize))
	var rpc RPC
	if err := decoder.Decode(&rpc); err != nil {
		c.log.Error("Failed to decode RPC", "error", err)
		return
	}

	// C-2 fix: Verify HMAC signature - reject if secret not configured
	if c.config.Secret == "" {
		c.log.Warn("Rejecting RPC from", "from", rpc.From, "reason", "cluster secret not configured")
		return
	}
	expected := computeHMAC(c.config.Secret, rpc.Type, rpc.From, rpc.Payload)
	if !hmac.Equal([]byte(rpc.Signature), []byte(expected)) {
		c.log.Warn("Invalid HMAC signature on RPC", "from", rpc.From, "type", rpc.Type)
		return
	}

	// Validate RPC type to prevent unknown type processing
	switch rpc.Type {
	case RPCVoteRequest, RPCVoteResponse, RPCAppendEntries, RPCHeartbeat, RPCInstallSnapshot, RPCJoin, RPCPingReq, RPCBackendHealth:
		// Known type, proceed
	default:
		c.log.Debug("Unknown RPC type", "type", rpc.Type)
		return
	}

	// Clear deadline for channel send
	conn.SetReadDeadline(time.Time{})

	c.rpcCh <- rpc
}

// raftLoop runs the Raft state machine.
func (c *Cluster) raftLoop() {
	electionTimer := time.NewTimer(c.randomElectionTimeout())
	defer electionTimer.Stop()

	heartbeatTicker := time.NewTicker(c.config.HeartbeatInterval)
	defer heartbeatTicker.Stop()
	heartbeatTicker.Stop() // Don't send heartbeats as follower

	for {
		select {
		case <-c.shutdownCh:
			return

		case rpc := <-c.rpcCh:
			c.handleRaftRPC(rpc)

		case <-electionTimer.C:
			if c.state != NodeStateLeader {
				c.startElection()
			}
			electionTimer.Reset(c.randomElectionTimeout())

		case <-heartbeatTicker.C:
			if c.state == NodeStateLeader {
				c.sendHeartbeats()
			}
		}
	}
}

// handleRaftRPC processes Raft RPC messages.
func (c *Cluster) handleRaftRPC(rpc RPC) {
	switch rpc.Type {
	case RPCVoteRequest:
		c.handleVoteRequest(rpc)
	case RPCVoteResponse:
		c.handleVoteResponse(rpc)
	case RPCAppendEntries:
		c.handleAppendEntries(rpc)
	case RPCHeartbeat:
		c.handleHeartbeat(rpc)
	case RPCJoin:
		c.handleJoin(rpc)
	case RPCPingReq:
		c.handlePingReq(rpc)
	case RPCBackendHealth:
		c.handleBackendHealth(rpc)
	}
}

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

// AppendEntries represents a log replication request.
type AppendEntries struct {
	Term         uint64     `json:"term"`
	LeaderID     string     `json:"leader_id"`
	PrevLogIndex uint64     `json:"prev_log_index"`
	PrevLogTerm  uint64     `json:"prev_log_term"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit uint64     `json:"leader_commit"`
}

// LogEntry represents a single log entry.
type LogEntry struct {
	Index uint64 `json:"index"`
	Term  uint64 `json:"term"`
	Data  []byte `json:"data"`
}

// handleVoteRequest processes a vote request.
func (c *Cluster) handleVoteRequest(rpc RPC) {
	c.mu.Lock()

	var req VoteRequest
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal vote request", "error", err)
		c.mu.Unlock()
		return
	}

	if req.Term < c.currentTerm {
		term := c.currentTerm
		c.mu.Unlock()
		c.sendRPC(rpc.From, RPCVoteResponse, VoteResponse{
			Term:        term,
			VoteGranted: false,
		})
		return
	}

	if req.Term > c.currentTerm {
		c.currentTerm = req.Term
		c.state = NodeStateFollower
		c.votedFor = ""
	}

	canVote := (c.votedFor == "" || c.votedFor == req.CandidateID)

	if canVote {
		c.votedFor = req.CandidateID
		c.state = NodeStateFollower
	}

	term := c.currentTerm
	c.mu.Unlock()
	c.sendRPC(rpc.From, RPCVoteResponse, VoteResponse{
		Term:        term,
		VoteGranted: canVote,
	})
}

// handleVoteResponse processes a vote response.
func (c *Cluster) handleVoteResponse(rpc RPC) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var resp VoteResponse
	if err := json.Unmarshal(rpc.Payload, &resp); err != nil {
		c.log.Error("Failed to unmarshal vote response", "error", err)
		return
	}

	if c.state != NodeStateCandidate || resp.Term < c.currentTerm {
		return
	}

	if resp.Term > c.currentTerm {
		c.currentTerm = resp.Term
		c.state = NodeStateFollower
		c.votedFor = ""
		return
	}

	if resp.VoteGranted {
		c.votesReceived[rpc.From] = true
		if len(c.votesReceived) >= c.quorum() {
			c.becomeLeader()
		}
	}
}

// handleAppendEntries processes append entries with full log replication.
func (c *Cluster) handleAppendEntries(rpc RPC) {
	c.mu.Lock()

	var req AppendEntries
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal append entries", "error", err)
		c.mu.Unlock()
		return
	}

	resp := VoteResponse{
		Term:        c.currentTerm,
		VoteGranted: false,
	}

	if req.Term < c.currentTerm {
		c.mu.Unlock()
		c.sendRPC(rpc.From, RPCVoteResponse, resp)
		return
	}

	c.currentTerm = req.Term
	c.state = NodeStateFollower
	c.leaderID = req.LeaderID
	c.votedFor = ""

	if req.PrevLogIndex > 0 {
		if int(req.PrevLogIndex) > len(c.logEntries) {
			c.mu.Unlock()
			c.sendRPC(rpc.From, RPCVoteResponse, resp)
			return
		}
		if len(c.logEntries) > 0 && c.logEntries[req.PrevLogIndex-1].Term != req.PrevLogTerm {
			c.logEntries = c.logEntries[:req.PrevLogIndex-1]
			c.mu.Unlock()
			c.sendRPC(rpc.From, RPCVoteResponse, resp)
			return
		}
	}

	for _, entry := range req.Entries {
		if int(entry.Index) <= len(c.logEntries) {
			if int(entry.Index) == len(c.logEntries) {
				c.logEntries = append(c.logEntries, entry)
			} else if c.logEntries[entry.Index-1].Term != entry.Term {
				c.logEntries = c.logEntries[:entry.Index-1]
				c.logEntries = append(c.logEntries, entry)
			}
		}
	}

	if req.LeaderCommit > c.commitIndex {
		if req.LeaderCommit > uint64(len(c.logEntries)) {
			c.commitIndex = uint64(len(c.logEntries))
		} else {
			c.commitIndex = req.LeaderCommit
		}
	}

	resp.Term = c.currentTerm
	resp.VoteGranted = true

	c.mu.Unlock()
	c.sendRPC(rpc.From, RPCVoteResponse, resp)
}

// handleHeartbeat processes heartbeat messages.
func (c *Cluster) handleHeartbeat(rpc RPC) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var heartbeat struct {
		Term     uint64 `json:"term"`
		LeaderID string `json:"leader_id"`
	}
	if err := json.Unmarshal(rpc.Payload, &heartbeat); err != nil {
		c.log.Error("Failed to unmarshal heartbeat", "error", err)
		return
	}

	if heartbeat.Term >= c.currentTerm {
		c.currentTerm = heartbeat.Term
		c.state = NodeStateFollower
		c.leaderID = heartbeat.LeaderID
		c.votedFor = ""
		c.self.LastSeen = time.Now()
	}
}

// startElection starts a new election.
func (c *Cluster) startElection() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state = NodeStateCandidate
	c.currentTerm++
	c.votedFor = c.nodeID
	c.votesReceived = map[string]bool{c.nodeID: true}

	c.log.Info("Starting election", "term", c.currentTerm, "node", c.nodeID)

	req := VoteRequest{
		Term:        c.currentTerm,
		CandidateID: c.nodeID,
	}

	for id := range c.nodes {
		if id != c.nodeID {
			c.sendRPC(id, RPCVoteRequest, req)
		}
	}
}

// becomeLeader transitions to leader state.
func (c *Cluster) becomeLeader() {
	c.state = NodeStateLeader
	c.leaderID = c.nodeID
	c.self.State = NodeStateLeader

	c.log.Info("Became leader", "term", c.currentTerm, "node", c.nodeID)

	c.sendHeartbeats()
}

// sendHeartbeats sends heartbeat to all followers.
func (c *Cluster) sendHeartbeats() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	heartbeat := struct {
		Term     uint64 `json:"term"`
		LeaderID string `json:"leader_id"`
	}{
		Term:     c.currentTerm,
		LeaderID: c.nodeID,
	}

	for id := range c.nodes {
		if id != c.nodeID {
			c.sendRPC(id, RPCHeartbeat, heartbeat)
		}
	}
}

// sendRPC sends an RPC message to a node.
func (c *Cluster) sendRPC(to string, rpcType string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		c.log.Error("Failed to marshal RPC payload", "error", err)
		return
	}

	c.mu.RLock()
	node, ok := c.nodes[to]
	c.mu.RUnlock()
	if !ok {
		return
	}

	var conn net.Conn
	if c.config.TLSConfig != nil {
		conn, err = tls.Dial("tcp", node.Address, c.config.TLSConfig)
	} else {
		conn, err = net.DialTimeout("tcp", node.Address, 5*time.Second)
	}
	if err != nil {
		c.log.Debug("Failed to connect to node", "node", to, "error", err)
		return
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	rpc := RPC{
		From:    c.nodeID,
		Type:    rpcType,
		Payload: data,
	}

	if c.config.Secret != "" {
		rpc.Signature = computeHMAC(c.config.Secret, rpcType, c.nodeID, data)
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(rpc); err != nil {
		c.log.Debug("Failed to send RPC", "node", to, "error", err)
	}
}

// randomElectionTimeout returns a randomized election timeout.
func (c *Cluster) randomElectionTimeout() time.Duration {
	return c.config.ElectionTimeout + time.Duration(time.Now().UnixNano()%100)*time.Millisecond
}

// quorum returns the number of votes needed for a quorum.
func (c *Cluster) quorum() int {
	return (len(c.nodes) / 2) + 1
}

// joinPeers sends join requests to configured peers.
func (c *Cluster) joinPeers() {
	time.Sleep(1 * time.Second)

	for _, peer := range c.config.Peers {
		c.log.Info("Attempting to join peer", "peer", peer)
		joinReq := struct {
			NodeID  string `json:"node_id"`
			Address string `json:"address"`
		}{
			NodeID:  c.nodeID,
			Address: c.config.ListenAddr,
		}
		c.sendRPC(peer, RPCJoin, joinReq)
	}
}

// IsLeader returns true if this node is the leader.
func (c *Cluster) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == NodeStateLeader
}

// GetLeader returns the current leader ID.
func (c *Cluster) GetLeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.leaderID
}

// GetTerm returns the current Raft term.
func (c *Cluster) GetTerm() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentTerm
}

// GetNodes returns all known nodes.
func (c *Cluster) GetNodes() []*Node {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nodes := make([]*Node, 0, len(c.nodes))
	for _, node := range c.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// AddNode adds a node to the cluster.
func (c *Cluster) AddNode(node *Node) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nodes[node.ID]; !exists {
		c.nodes[node.ID] = node
		c.log.Info("Node joined cluster", "node_id", node.ID, "address", node.Address)
	}
}

// handleJoin processes a join request from a new node.
func (c *Cluster) handleJoin(rpc RPC) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var req struct {
		NodeID  string `json:"node_id"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal join request", "error", err)
		return
	}

	if req.NodeID == "" || req.Address == "" {
		c.log.Warn("Invalid join request: missing node_id or address")
		return
	}

	if _, exists := c.nodes[req.NodeID]; exists {
		c.log.Debug("Node already in cluster", "node_id", req.NodeID)
		return
	}

	c.nodes[req.NodeID] = &Node{
		ID:       req.NodeID,
		Address:  req.Address,
		State:    NodeStateFollower,
		LastSeen: time.Now(),
	}
	c.log.Info("Node joined cluster", "node_id", req.NodeID, "address", req.Address)

	if c.state == NodeStateLeader {
		c.sendHeartbeats()
	}
}

// handlePingReq processes an indirect ping request from another node.
func (c *Cluster) handlePingReq(rpc RPC) {
	var req struct {
		TargetID   string `json:"target_id"`
		TargetAddr string `json:"target_addr"`
	}
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal ping request", "error", err)
		return
	}

	conn, err := net.DialTimeout("tcp", req.TargetAddr, 3*time.Second)
	if err != nil {
		c.log.Debug("Indirect probe failed", "target", req.TargetID, "error", err)
		return
	}
	conn.Close()

	c.mu.RLock()
	if node, ok := c.nodes[req.TargetID]; ok {
		node.LastSeen = time.Now()
		node.State = NodeStateFollower
	}
	c.mu.RUnlock()
}

// BackendHealthUpdate represents a backend health update received from the leader.
type BackendHealthUpdate struct {
	BackendID string `json:"backend_id"`
	Healthy   bool   `json:"healthy"`
	Timestamp int64  `json:"timestamp"`
}

func (c *Cluster) handleBackendHealth(rpc RPC) {
	var update BackendHealthUpdate
	if err := json.Unmarshal(rpc.Payload, &update); err != nil {
		c.log.Error("Failed to unmarshal backend health update", "error", err)
		return
	}

	c.log.Debug("Received backend health update",
		"backend", update.BackendID,
		"healthy", update.Healthy,
		"from", rpc.From,
	)

	if c.config.OnBackendHealth != nil {
		c.config.OnBackendHealth(update.BackendID, update.Healthy, rpc.From)
	}
}

// run runs the SWIM gossip protocol.
func (s *SwimGossip) run() {
	ticker := time.NewTicker(s.protocolPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-s.cluster.shutdownCh:
			return
		case <-ticker.C:
			s.protocolRound()
		}
	}
}

// protocolRound performs one round of the SWIM protocol.
func (s *SwimGossip) protocolRound() {
	s.mu.Lock()
	defer s.mu.Unlock()

	nodes := s.cluster.GetNodes()
	if len(nodes) <= 1 {
		return
	}

	var target *Node
	for _, n := range nodes {
		if n.ID != s.cluster.nodeID && n.State != NodeStateDead {
			if _, suspected := s.suspected[n.ID]; !suspected {
				target = n
				break
			}
		}
	}

	if target == nil {
		return
	}

	go s.probe(target)
}

// probe sends a direct ping to a node with retry.
func (s *SwimGossip) probe(target *Node) {
	select {
	case s.probeSem <- struct{}{}:
	default:
		return
	}
	defer func() { <-s.probeSem }()

	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(50<<attempt) * time.Millisecond
			if backoff > s.probeTimeout {
				backoff = s.probeTimeout
			}
			time.Sleep(backoff)
		}

		conn, err := net.DialTimeout("tcp", target.Address, s.probeTimeout)
		if err != nil {
			continue
		}

		conn.SetReadDeadline(time.Now().Add(s.probeTimeout))

		s.mu.Lock()
		s.alive[target.ID] = time.Now()
		delete(s.suspected, target.ID)
		target.LastSeen = time.Now()
		target.State = NodeStateFollower
		s.mu.Unlock()
		conn.Close()
		return
	}

	s.indirectProbe(target)
}

// indirectProbe asks other nodes to probe a suspected failed node.
func (s *SwimGossip) indirectProbe(target *Node) {
	s.mu.Lock()
	s.suspected[target.ID] = time.Now()
	s.mu.Unlock()

	nodes := s.cluster.GetNodes()
	asked := 0

	for _, n := range nodes {
		if n.ID != s.cluster.nodeID && n.ID != target.ID && asked < s.numIndirectProbes {
			pingReq := struct {
				TargetID   string `json:"target_id"`
				TargetAddr string `json:"target_addr"`
			}{
				TargetID:   target.ID,
				TargetAddr: target.Address,
			}
			s.cluster.sendRPC(n.ID, RPCPingReq, pingReq)
			asked++
		}
	}
}

// BroadcastMessage broadcasts a message to all nodes.
func (c *Cluster) BroadcastMessage(msgType string, data []byte) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for id := range c.nodes {
		if id != c.nodeID {
			c.sendRPC(id, msgType, data)
		}
	}

	return nil
}

// GetState returns the current node state.
func (c *Cluster) GetState() NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// StateString returns the current node state as a plain string.
func (c *Cluster) StateString() string {
	return string(c.GetState())
}

// GetNodeCount returns the number of nodes in the cluster.
func (c *Cluster) GetNodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

// ShareBackendHealth shares backend health information with the cluster.
func (c *Cluster) ShareBackendHealth(backendID string, healthy bool) {
	if !c.IsLeader() {
		return
	}

	msg := struct {
		BackendID string `json:"backend_id"`
		Healthy   bool   `json:"healthy"`
		Timestamp int64  `json:"timestamp"`
	}{
		BackendID: backendID,
		Healthy:   healthy,
		Timestamp: time.Now().Unix(),
	}

	data, _ := json.Marshal(msg)
	c.BroadcastMessage("backend_health", data)
}

// String returns a string representation of the cluster state.
func (c *Cluster) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return fmt.Sprintf("Cluster{node=%s, state=%s, term=%d, nodes=%d}",
		c.nodeID, c.state, c.currentTerm, len(c.nodes))
}
