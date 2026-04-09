package cluster

import (
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestNodeStateConstants(t *testing.T) {
	if NodeStateLeader != "leader" {
		t.Errorf("NodeStateLeader = %q, want leader", NodeStateLeader)
	}
	if NodeStateFollower != "follower" {
		t.Errorf("NodeStateFollower = %q, want follower", NodeStateFollower)
	}
	if NodeStateCandidate != "candidate" {
		t.Errorf("NodeStateCandidate = %q, want candidate", NodeStateCandidate)
	}
	if NodeStateDead != "dead" {
		t.Errorf("NodeStateDead = %q, want dead", NodeStateDead)
	}
}

func TestRPCConstants(t *testing.T) {
	if RPCVoteRequest != "VoteRequest" {
		t.Errorf("RPCVoteRequest = %q", RPCVoteRequest)
	}
	if RPCVoteResponse != "VoteResponse" {
		t.Errorf("RPCVoteResponse = %q", RPCVoteResponse)
	}
	if RPCAppendEntries != "AppendEntries" {
		t.Errorf("RPCAppendEntries = %q", RPCAppendEntries)
	}
	if RPCHeartbeat != "Heartbeat" {
		t.Errorf("RPCHeartbeat = %q", RPCHeartbeat)
	}
	if RPCInstallSnapshot != "InstallSnapshot" {
		t.Errorf("RPCInstallSnapshot = %q", RPCInstallSnapshot)
	}
}

func TestMaxRPCPayloadSize(t *testing.T) {
	if maxRPCPayloadSize != 1<<20 {
		t.Errorf("maxRPCPayloadSize = %d, want %d", maxRPCPayloadSize, 1<<20)
	}
}

func TestNew(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.nodeID != "node-1" {
		t.Errorf("nodeID = %q, want node-1", c.nodeID)
	}
	if c.state != NodeStateFollower {
		t.Errorf("state = %v, want follower", c.state)
	}
	if c.currentTerm != 0 {
		t.Errorf("currentTerm = %d, want 0", c.currentTerm)
	}
	if len(c.nodes) != 1 {
		t.Errorf("nodes count = %d, want 1", len(c.nodes))
	}
}

func TestNew_Defaults(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	if c.config.ElectionTimeout != 1*time.Second {
		t.Errorf("ElectionTimeout = %v, want 1s", c.config.ElectionTimeout)
	}
	if c.config.HeartbeatInterval != 150*time.Millisecond {
		t.Errorf("HeartbeatInterval = %v, want 150ms", c.config.HeartbeatInterval)
	}
}

func TestNew_WithPeers(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Peers:      []string{"127.0.0.1:9001", "127.0.0.1:9002"},
		Logger:     log,
	})
	if len(c.config.Peers) != 2 {
		t.Errorf("Peers count = %d, want 2", len(c.config.Peers))
	}
}

func TestCluster_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	err := c.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = c.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestCluster_IsLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	if c.IsLeader() {
		t.Error("New cluster should not be leader")
	}
}

func TestCluster_GetLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	leader := c.GetLeader()
	if leader != "" {
		t.Errorf("GetLeader = %q, want empty", leader)
	}
}

func TestCluster_GetState(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	state := c.GetState()
	if state != NodeStateFollower {
		t.Errorf("GetState = %v, want follower", state)
	}
}

func TestCluster_GetNodeCount(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	if c.GetNodeCount() != 1 {
		t.Errorf("GetNodeCount = %d, want 1", c.GetNodeCount())
	}
}

func TestCluster_GetNodes(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	nodes := c.GetNodes()
	if len(nodes) != 1 {
		t.Errorf("GetNodes returned %d nodes, want 1", len(nodes))
	}
	if nodes[0].ID != "node-1" {
		t.Errorf("GetNodes[0].ID = %q, want node-1", nodes[0].ID)
	}
}

func TestCluster_AddNode(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.AddNode(&Node{
		ID:      "node-2",
		Address: "127.0.0.1:9002",
		State:   NodeStateFollower,
	})

	if c.GetNodeCount() != 2 {
		t.Errorf("GetNodeCount = %d, want 2", c.GetNodeCount())
	}

	// Duplicate add should not increase count
	c.AddNode(&Node{
		ID:      "node-2",
		Address: "127.0.0.1:9002",
		State:   NodeStateFollower,
	})
	if c.GetNodeCount() != 2 {
		t.Errorf("GetNodeCount after duplicate add = %d, want 2", c.GetNodeCount())
	}
}

func TestCluster_String(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	s := c.String()
	if s == "" {
		t.Error("String() returned empty")
	}
	// Should contain node ID and state
	if len(s) < 20 {
		t.Errorf("String() too short: %q", s)
	}
}

func TestCluster_Quorum(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// 1 node -> quorum = 1
	if c.quorum() != 1 {
		t.Errorf("quorum = %d, want 1", c.quorum())
	}

	c.AddNode(&Node{ID: "node-2", Address: "127.0.0.1:9002", State: NodeStateFollower})
	// 2 nodes -> quorum = 2
	if c.quorum() != 2 {
		t.Errorf("quorum = %d, want 2", c.quorum())
	}

	c.AddNode(&Node{ID: "node-3", Address: "127.0.0.1:9003", State: NodeStateFollower})
	// 3 nodes -> quorum = 2
	if c.quorum() != 2 {
		t.Errorf("quorum = %d, want 2", c.quorum())
	}
}

func TestCluster_BroadcastMessage(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	// With no other nodes, should not error
	err := c.BroadcastMessage("test", []byte("hello"))
	if err != nil {
		t.Errorf("BroadcastMessage failed: %v", err)
	}
}

func TestCluster_ShareBackendHealth_NotLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	// Not leader, should silently return
	c.ShareBackendHealth("backend-1", true)
}

func TestNode(t *testing.T) {
	n := &Node{
		ID:       "node-1",
		Address:  "127.0.0.1:7946",
		State:    NodeStateFollower,
		LastSeen: time.Now(),
		Meta:     map[string]string{"role": "primary"},
	}
	if n.ID != "node-1" {
		t.Errorf("ID = %q, want node-1", n.ID)
	}
	if n.Meta["role"] != "primary" {
		t.Errorf("Meta[role] = %q, want primary", n.Meta["role"])
	}
}

func TestRPC(t *testing.T) {
	rpc := RPC{
		From:    "node-1",
		Type:    RPCVoteRequest,
		Payload: []byte(`{"term":1}`),
	}
	if rpc.Type != RPCVoteRequest {
		t.Errorf("Type = %q, want VoteRequest", rpc.Type)
	}
}

func TestVoteRequest(t *testing.T) {
	vr := VoteRequest{
		Term:         2,
		CandidateID:  "node-1",
		LastLogIndex: 10,
		LastLogTerm:  2,
	}
	if vr.Term != 2 {
		t.Errorf("Term = %d, want 2", vr.Term)
	}
}

func TestVoteResponse(t *testing.T) {
	vr := VoteResponse{
		Term:        2,
		VoteGranted: true,
	}
	if !vr.VoteGranted {
		t.Error("Vote should be granted")
	}
}

func TestAppendEntries(t *testing.T) {
	ae := AppendEntries{
		Term:         1,
		LeaderID:     "node-1",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      []LogEntry{},
		LeaderCommit: 0,
	}
	if ae.LeaderID != "node-1" {
		t.Errorf("LeaderID = %q, want node-1", ae.LeaderID)
	}
}

func TestLogEntry(t *testing.T) {
	e := LogEntry{
		Index: 5,
		Term:  1,
		Data:  []byte("test"),
	}
	if e.Index != 5 {
		t.Errorf("Index = %d, want 5", e.Index)
	}
}

func TestSwimGossip(t *testing.T) {
	log, _ := logger.New("debug", "text")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})
	if c.gossip == nil {
		t.Fatal("gossip is nil")
	}
	if c.gossip.protocolPeriod != 1*time.Second {
		t.Errorf("gossip period = %v, want 1s", c.gossip.protocolPeriod)
	}
	if c.gossip.numIndirectProbes != 3 {
		t.Errorf("numIndirectProbes = %d, want 3", c.gossip.numIndirectProbes)
	}
}
