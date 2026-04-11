package cluster

import (
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestCluster_randomElectionTimeout(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:          "node-1",
		ListenAddr:      "127.0.0.1:0",
		ElectionTimeout: 1 * time.Second,
		Logger:          log,
	})

	timeout := c.randomElectionTimeout()
	// Should be >= ElectionTimeout
	if timeout < c.config.ElectionTimeout {
		t.Errorf("randomElectionTimeout() = %v, want >= %v", timeout, c.config.ElectionTimeout)
	}

	// Should be <= ElectionTimeout + 100ms
	maxTimeout := c.config.ElectionTimeout + 100*time.Millisecond
	if timeout > maxTimeout {
		t.Errorf("randomElectionTimeout() = %v, want <= %v", timeout, maxTimeout)
	}
}

func TestCluster_becomeLeader(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Initially follower
	if c.state != NodeStateFollower {
		t.Error("Initial state should be follower")
	}

	// Become leader
	c.becomeLeader()

	if c.state != NodeStateLeader {
		t.Errorf("state = %v, want leader", c.state)
	}
	if c.leaderID != "node-1" {
		t.Errorf("leaderID = %q, want node-1", c.leaderID)
	}
	if c.GetLeader() != "node-1" {
		t.Errorf("GetLeader() = %q, want node-1", c.GetLeader())
	}
	if !c.IsLeader() {
		t.Error("IsLeader() should be true")
	}
}

func TestCluster_startElection(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	initialTerm := c.currentTerm
	c.startElection()

	if c.state != NodeStateCandidate {
		t.Errorf("state = %v, want candidate", c.state)
	}
	if c.currentTerm != initialTerm+1 {
		t.Errorf("currentTerm = %d, want %d", c.currentTerm, initialTerm+1)
	}
	if c.votedFor != "node-1" {
		t.Errorf("votedFor = %q, want node-1", c.votedFor)
	}
	if len(c.votesReceived) != 1 {
		t.Errorf("votesReceived count = %d, want 1", len(c.votesReceived))
	}
	if !c.votesReceived["node-1"] {
		t.Error("votesReceived should contain self")
	}
}

func TestCluster_handleVoteRequest_LowerTerm(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.currentTerm = 5

	// Vote request with lower term should be rejected
	rpc := RPC{
		From:    "node-2",
		Type:    RPCVoteRequest,
		Payload: []byte(`{"term":3,"candidate_id":"node-2"}`),
	}

	c.handleVoteRequest(rpc)
	// Should not grant vote (we can't easily verify the response, but it shouldn't panic)
}

func TestCluster_handleVoteRequest_HigherTerm(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.currentTerm = 1

	// Vote request with higher term should update term and grant vote
	rpc := RPC{
		From:    "node-2",
		Type:    RPCVoteRequest,
		Payload: []byte(`{"term":5,"candidate_id":"node-2"}`),
	}

	c.handleVoteRequest(rpc)

	if c.currentTerm != 5 {
		t.Errorf("currentTerm = %d, want 5", c.currentTerm)
	}
	if c.state != NodeStateFollower {
		t.Errorf("state = %v, want follower", c.state)
	}
}

func TestCluster_handleVoteResponse_HigherTerm(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.state = NodeStateCandidate
	c.currentTerm = 3

	// Vote response with higher term should revert to follower
	rpc := RPC{
		From:    "node-2",
		Type:    RPCVoteResponse,
		Payload: []byte(`{"term":5,"vote_granted":false}`),
	}

	c.handleVoteResponse(rpc)

	if c.currentTerm != 5 {
		t.Errorf("currentTerm = %d, want 5", c.currentTerm)
	}
	if c.state != NodeStateFollower {
		t.Errorf("state = %v, want follower", c.state)
	}
}

func TestCluster_handleAppendEntries_LowerTerm(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.currentTerm = 5

	// Append entries with lower term should be ignored
	rpc := RPC{
		From:    "node-2",
		Type:    RPCAppendEntries,
		Payload: []byte(`{"term":3,"leader_id":"node-2"}`),
	}

	c.handleAppendEntries(rpc)
	// Should not update state
	if c.currentTerm != 5 {
		t.Error("currentTerm should not change with lower term AE")
	}
}

func TestCluster_handleHeartbeat(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	c.state = NodeStateCandidate
	c.currentTerm = 3

	// Heartbeat with higher term should revert to follower
	rpc := RPC{
		From:    "node-2",
		Type:    RPCHeartbeat,
		Payload: []byte(`{"term":5,"leader_id":"node-2"}`),
	}

	c.handleHeartbeat(rpc)

	if c.currentTerm != 5 {
		t.Errorf("currentTerm = %d, want 5", c.currentTerm)
	}
	if c.state != NodeStateFollower {
		t.Errorf("state = %v, want follower", c.state)
	}
	if c.leaderID != "node-2" {
		t.Errorf("leaderID = %q, want node-2", c.leaderID)
	}
}

func TestCluster_handleRaftRPC_UnknownType(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Unknown RPC type should not panic
	rpc := RPC{
		From:    "node-2",
		Type:    "UnknownType",
		Payload: []byte(`{}`),
	}

	c.handleRaftRPC(rpc)
	// Should not panic
}

func TestCluster_sendRPC_NonExistentNode(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Sending to non-existent node should not panic
	c.sendRPC("non-existent", RPCHeartbeat, struct{ Term uint64 }{Term: 1})
	// Should not panic
}

func TestSwimGossip_protocolRound(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// With only self, should return early
	c.gossip.protocolRound()

	// Add another node
	c.AddNode(&Node{
		ID:      "node-2",
		Address: "127.0.0.1:9002",
		State:   NodeStateFollower,
	})

	// Should not panic
	c.gossip.protocolRound()
}

func TestSwimGossip_probe(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Add a node to probe
	node := &Node{
		ID:      "node-2",
		Address: "127.0.0.1:1", // Invalid port that will fail
		State:   NodeStateFollower,
	}
	c.AddNode(node)

	// Probe should attempt connection and fail (indirect probe)
	c.gossip.probe(node)
	// Should not panic - probe fails but handles gracefully
}

func TestSwimGossip_indirectProbe(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Add nodes for indirect probing
	c.AddNode(&Node{
		ID:      "node-2",
		Address: "127.0.0.1:9002",
		State:   NodeStateFollower,
	})
	c.AddNode(&Node{
		ID:      "node-3",
		Address: "127.0.0.1:9003",
		State:   NodeStateFollower,
	})

	target := &Node{
		ID:      "node-4",
		Address: "127.0.0.1:9004",
		State:   NodeStateFollower,
	}
	c.AddNode(target)

	// Indirect probe should mark as suspected
	c.gossip.indirectProbe(target)

	if _, ok := c.gossip.suspected[target.ID]; !ok {
		t.Error("Target should be marked as suspected after indirect probe")
	}
}

func TestCluster_GetNodes_Copy(t *testing.T) {
	log, _ := logger.New("error", "json")
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

	nodes1 := c.GetNodes()
	nodes2 := c.GetNodes()

	// Should return independent copies
	if len(nodes1) != len(nodes2) {
		t.Error("GetNodes should return same number of nodes")
	}

	// Both should contain the same data
	hasNode2 := false
	for _, n := range nodes1 {
		if n.ID == "node-2" {
			hasNode2 = true
			break
		}
	}
	if !hasNode2 {
		t.Error("GetNodes should return node-2")
	}
}

func TestCluster_ShareBackendHealth_AsLeader(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	// Become leader
	c.becomeLeader()

	// Should not panic when sharing as leader
	c.ShareBackendHealth("backend-1", true)
	c.ShareBackendHealth("backend-2", false)
}

func TestVoteRequest_Fields(t *testing.T) {
	vr := VoteRequest{
		Term:         10,
		CandidateID:  "candidate-1",
		LastLogIndex: 100,
		LastLogTerm:  5,
	}

	if vr.CandidateID != "candidate-1" {
		t.Errorf("CandidateID = %q, want candidate-1", vr.CandidateID)
	}
	if vr.LastLogIndex != 100 {
		t.Errorf("LastLogIndex = %d, want 100", vr.LastLogIndex)
	}
	if vr.LastLogTerm != 5 {
		t.Errorf("LastLogTerm = %d, want 5", vr.LastLogTerm)
	}
}

func TestVoteResponse_Fields(t *testing.T) {
	vr := VoteResponse{
		Term:        10,
		VoteGranted: false,
	}

	if vr.Term != 10 {
		t.Errorf("Term = %d, want 10", vr.Term)
	}
	if vr.VoteGranted {
		t.Error("VoteGranted should be false")
	}
}

func TestAppendEntries_Fields(t *testing.T) {
	ae := AppendEntries{
		Term:         5,
		LeaderID:     "leader-1",
		PrevLogIndex: 50,
		PrevLogTerm:  4,
		Entries: []LogEntry{
			{Index: 51, Term: 5, Data: []byte("entry1")},
		},
		LeaderCommit: 50,
	}

	if ae.Term != 5 {
		t.Errorf("Term = %d, want 5", ae.Term)
	}
	if ae.LeaderID != "leader-1" {
		t.Errorf("LeaderID = %q, want leader-1", ae.LeaderID)
	}
	if ae.PrevLogIndex != 50 {
		t.Errorf("PrevLogIndex = %d, want 50", ae.PrevLogIndex)
	}
	if ae.PrevLogTerm != 4 {
		t.Errorf("PrevLogTerm = %d, want 4", ae.PrevLogTerm)
	}
	if len(ae.Entries) != 1 {
		t.Errorf("Entries count = %d, want 1", len(ae.Entries))
	}
	if ae.LeaderCommit != 50 {
		t.Errorf("LeaderCommit = %d, want 50", ae.LeaderCommit)
	}
}

func TestLogEntry_Fields(t *testing.T) {
	entry := LogEntry{
		Index: 100,
		Term:  5,
		Data:  []byte("test data"),
	}

	if entry.Index != 100 {
		t.Errorf("Index = %d, want 100", entry.Index)
	}
	if entry.Term != 5 {
		t.Errorf("Term = %d, want 5", entry.Term)
	}
	if string(entry.Data) != "test data" {
		t.Errorf("Data = %q, want test data", string(entry.Data))
	}
}

func TestSwimGossip_Fields(t *testing.T) {
	log, _ := logger.New("error", "json")
	c := New(Config{
		NodeID:     "node-1",
		ListenAddr: "127.0.0.1:0",
		Logger:     log,
	})

	if c.gossip.cluster != c {
		t.Error("gossip.cluster should point to parent cluster")
	}
	if c.gossip.protocolPeriod != 1*time.Second {
		t.Errorf("protocolPeriod = %v, want 1s", c.gossip.protocolPeriod)
	}
	if c.gossip.probeTimeout != 500*time.Millisecond {
		t.Errorf("probeTimeout = %v, want 500ms", c.gossip.probeTimeout)
	}
	if c.gossip.numIndirectProbes != 3 {
		t.Errorf("numIndirectProbes = %d, want 3", c.gossip.numIndirectProbes)
	}
}

func TestConfig_Fields(t *testing.T) {
	log, _ := logger.New("error", "json")
	cfg := Config{
		NodeID:            "node-1",
		ListenAddr:        "127.0.0.1:7946",
		Peers:             []string{"127.0.0.1:7947"},
		ElectionTimeout:   2 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
		Logger:            log,
	}

	if cfg.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want node-1", cfg.NodeID)
	}
	if cfg.ListenAddr != "127.0.0.1:7946" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:7946", cfg.ListenAddr)
	}
	if len(cfg.Peers) != 1 {
		t.Errorf("Peers count = %d, want 1", len(cfg.Peers))
	}
	if cfg.ElectionTimeout != 2*time.Second {
		t.Errorf("ElectionTimeout = %v, want 2s", cfg.ElectionTimeout)
	}
	if cfg.HeartbeatInterval != 200*time.Millisecond {
		t.Errorf("HeartbeatInterval = %v, want 200ms", cfg.HeartbeatInterval)
	}
}
