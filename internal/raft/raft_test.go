package raft

import (
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestNodeState_String(t *testing.T) {
	cases := []struct {
		state NodeState
		want  string
	}{
		{StateFollower, "Follower"},
		{StateCandidate, "Candidate"},
		{StateLeader, "Leader"},
		{NodeState(99), "Unknown"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("NodeState(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestMessageTypeConstants(t *testing.T) {
	if MsgVoteRequest != 0 {
		t.Errorf("MsgVoteRequest = %d, want 0", MsgVoteRequest)
	}
	if MsgVoteResponse != 1 {
		t.Errorf("MsgVoteResponse = %d, want 1", MsgVoteResponse)
	}
	if MsgAppendEntries != 2 {
		t.Errorf("MsgAppendEntries = %d, want 2", MsgAppendEntries)
	}
	if MsgAppendEntriesResponse != 3 {
		t.Errorf("MsgAppendEntriesResponse = %d, want 3", MsgAppendEntriesResponse)
	}
}

func TestNewNode(t *testing.T) {
	log, _ := logger.New("debug", "text")
	n := NewNode("node-1", "127.0.0.1:0", []string{}, log)
	if n == nil {
		t.Fatal("NewNode returned nil")
	}
}

func TestNode_ID(t *testing.T) {
	log, _ := logger.New("debug", "text")
	n := NewNode("node-1", "127.0.0.1:0", []string{}, log)
	if n.ID() != "node-1" {
		t.Errorf("ID = %q, want node-1", n.ID())
	}
}

func TestNode_State(t *testing.T) {
	log, _ := logger.New("debug", "text")
	n := NewNode("node-1", "127.0.0.1:0", []string{}, log)
	if n.State() != StateFollower {
		t.Errorf("State = %v, want Follower", n.State())
	}
	if n.IsLeader() {
		t.Error("New node should not be leader")
	}
}

func TestNode_CurrentTerm(t *testing.T) {
	log, _ := logger.New("debug", "text")
	n := NewNode("node-1", "127.0.0.1:0", []string{}, log)
	if n.CurrentTerm() != 0 {
		t.Errorf("CurrentTerm = %d, want 0", n.CurrentTerm())
	}
}

func TestNode_StartStop(t *testing.T) {
	log, _ := logger.New("debug", "text")
	n := NewNode("node-1", "127.0.0.1:0", []string{}, log)

	err := n.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = n.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestEntry(t *testing.T) {
	e := Entry{
		Term:    1,
		Index:   5,
		Command: []byte(`{"op":"set","key":"x","value":"1"}`),
	}
	if e.Term != 1 {
		t.Errorf("Term = %d, want 1", e.Term)
	}
	if e.Index != 5 {
		t.Errorf("Index = %d, want 5", e.Index)
	}
}

func TestMessage(t *testing.T) {
	msg := Message{
		Type: MsgVoteRequest,
		From: "node-1",
		To:   "node-2",
		Term: 1,
	}
	if msg.Type != MsgVoteRequest {
		t.Errorf("Type = %v, want MsgVoteRequest", msg.Type)
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
	if vr.CandidateID != "node-1" {
		t.Errorf("CandidateID = %q, want node-1", vr.CandidateID)
	}
}

func TestVoteResponse(t *testing.T) {
	vresp := VoteResponse{
		Term:        2,
		VoteGranted: true,
	}
	if !vresp.VoteGranted {
		t.Error("Vote should be granted")
	}
}

func TestAppendEntries(t *testing.T) {
	ae := AppendEntries{
		Term:         1,
		LeaderID:     "node-1",
		PrevLogIndex: 0,
		PrevLogTerm:  0,
		Entries:      []Entry{},
		LeaderCommit: 0,
	}
	if ae.LeaderID != "node-1" {
		t.Errorf("LeaderID = %q, want node-1", ae.LeaderID)
	}
}

func TestAppendEntriesResponse(t *testing.T) {
	aer := AppendEntriesResponse{
		Term:    1,
		Success: true,
		Index:   5,
	}
	if !aer.Success {
		t.Error("Should be successful")
	}
}
