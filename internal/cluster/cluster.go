package cluster

import (
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
	ID       string    `json:"id"`
	Address  string    `json:"address"`
	State    NodeState `json:"state"`
	LastSeen time.Time `json:"last_seen"`
	Meta     map[string]string `json:"meta"`
}

// Config represents cluster configuration.
type Config struct {
	NodeID          string
	ListenAddr      string
	Peers           []string
	ElectionTimeout time.Duration
	HeartbeatInterval time.Duration
	Logger          *logger.Logger
}

// Cluster manages distributed consensus and node membership.
type Cluster struct {
	mu sync.RWMutex

	config Config
	nodeID string
	state  NodeState

	// Raft state
	currentTerm uint64
	votedFor    string
	leaderID    string
	votesReceived map[string]bool

	// Membership
	nodes map[string]*Node
	self  *Node

	// SWIM gossip
	gossip *SwimGossip

	// Channels
	rpcCh     chan RPC
	shutdownCh chan struct{}
	doneCh    chan struct{}

	log *logger.Logger
}

// RPC represents a cluster RPC message.
type RPC struct {
	From    string
	Type    string
	Payload []byte
}

// Maximum RPC payload size (1MB)
const maxRPCPayloadSize = 1 << 20

// Raft RPC types
const (
	RPCVoteRequest    = "VoteRequest"
	RPCVoteResponse   = "VoteResponse"
	RPCAppendEntries  = "AppendEntries"
	RPCHeartbeat      = "Heartbeat"
	RPCInstallSnapshot = "InstallSnapshot"
)

// SwimGossip implements the SWIM protocol for failure detection.
type SwimGossip struct {
	mu sync.RWMutex

	cluster    *Cluster
	protocolPeriod time.Duration
	probeTimeout   time.Duration
	numIndirectProbes int

	// Failure suspicion
	suspected map[string]time.Time
	alive     map[string]time.Time
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

		go c.handleRPC(conn)
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

	// Validate RPC type to prevent unknown type processing
	switch rpc.Type {
	case RPCVoteRequest, RPCVoteResponse, RPCAppendEntries, RPCHeartbeat, RPCInstallSnapshot:
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
	}
}

// VoteRequest represents a request for votes.
type VoteRequest struct {
	Term        uint64 `json:"term"`
	CandidateID string `json:"candidate_id"`
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
	Term         uint64 `json:"term"`
	LeaderID     string `json:"leader_id"`
	PrevLogIndex uint64 `json:"prev_log_index"`
	PrevLogTerm  uint64 `json:"prev_log_term"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit uint64 `json:"leader_commit"`
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
	defer c.mu.Unlock()

	var req VoteRequest
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal vote request", "error", err)
		return
	}

	// Reply false if term < currentTerm
	if req.Term < c.currentTerm {
		c.sendRPC(rpc.From, RPCVoteResponse, VoteResponse{
			Term:        c.currentTerm,
			VoteGranted: false,
		})
		return
	}

	// If term > currentTerm, update term and become follower
	if req.Term > c.currentTerm {
		c.currentTerm = req.Term
		c.state = NodeStateFollower
		c.votedFor = ""
	}

	// Check if we can grant vote
	canVote := (c.votedFor == "" || c.votedFor == req.CandidateID)

	if canVote {
		c.votedFor = req.CandidateID
		c.state = NodeStateFollower
	}

	c.sendRPC(rpc.From, RPCVoteResponse, VoteResponse{
		Term:        c.currentTerm,
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

	// Ignore if not a candidate or term is outdated
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

// handleAppendEntries processes append entries.
func (c *Cluster) handleAppendEntries(rpc RPC) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var req AppendEntries
	if err := json.Unmarshal(rpc.Payload, &req); err != nil {
		c.log.Error("Failed to unmarshal append entries", "error", err)
		return
	}

	// Reply false if term < currentTerm
	if req.Term < c.currentTerm {
		// Send response with current term
		return
	}

	// If term >= currentTerm, recognize leader
	if req.Term >= c.currentTerm {
		c.currentTerm = req.Term
		c.state = NodeStateFollower
		c.leaderID = req.LeaderID
		c.votedFor = ""
	}
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

	// Request votes from all other nodes
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

	// Send immediate heartbeat
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

	node, ok := c.nodes[to]
	if !ok {
		return
	}

	conn, err := net.DialTimeout("tcp", node.Address, 5*time.Second)
	if err != nil {
		c.log.Debug("Failed to connect to node", "node", to, "error", err)
		return
	}
	defer conn.Close()

	// Set write deadline to prevent hanging on slow nodes
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	rpc := RPC{
		From:    c.nodeID,
		Type:    rpcType,
		Payload: data,
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

// joinPeers attempts to join the configured peers.
func (c *Cluster) joinPeers() {
	time.Sleep(1 * time.Second) // Give time for server to start

	for _, peer := range c.config.Peers {
		c.log.Info("Attempting to join peer", "peer", peer)
		// Implementation would send a join request
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

	// Select random node to probe
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

	// Probe the target
	go s.probe(target)
}

// probe sends a direct ping to a node.
func (s *SwimGossip) probe(target *Node) {
	// Send ping with deadline
	conn, err := net.DialTimeout("tcp", target.Address, s.probeTimeout)
	if err != nil {
		// Node might be failed, start indirect probing
		s.indirectProbe(target)
		return
	}
	defer conn.Close()

	// Set read deadline for the probe
	conn.SetReadDeadline(time.Now().Add(s.probeTimeout))

	// Node responded, mark as alive - hold mutex for entire update
	s.mu.Lock()
	s.alive[target.ID] = time.Now()
	delete(s.suspected, target.ID)
	target.LastSeen = time.Now()
	target.State = NodeStateFollower // M-3 fix: update under lock
	s.mu.Unlock()
}

// indirectProbe asks other nodes to probe a suspected failed node.
func (s *SwimGossip) indirectProbe(target *Node) {
	s.mu.Lock()
	s.suspected[target.ID] = time.Now()
	s.mu.Unlock()

	// Ask k random nodes to probe the target
	nodes := s.cluster.GetNodes()
	asked := 0

	for _, n := range nodes {
		if n.ID != s.cluster.nodeID && n.ID != target.ID && asked < s.numIndirectProbes {
			// Send indirect ping request
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
