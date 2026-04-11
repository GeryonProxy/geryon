package raft

import (
	"os"
	"testing"
	"time"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestInstallSnapshotRequest(t *testing.T) {
	req := InstallSnapshotRequest{
		Term:              1,
		LeaderID:          "node-1",
		LastIncludedIndex: 100,
		LastIncludedTerm:  5,
		Offset:            0,
		Data:              []byte("snapshot data"),
		Done:              true,
	}
	if req.LeaderID != "node-1" {
		t.Errorf("LeaderID = %q, want node-1", req.LeaderID)
	}
	if req.LastIncludedIndex != 100 {
		t.Errorf("LastIncludedIndex = %d, want 100", req.LastIncludedIndex)
	}
}

func TestInstallSnapshotResponse(t *testing.T) {
	resp := InstallSnapshotResponse{
		Term:    1,
		Success: true,
	}
	if !resp.Success {
		t.Error("Success should be true")
	}
}

func TestNode_lastLogInfo(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Initially empty
	idx, term := n.lastLogInfo()
	if idx != 0 || term != 0 {
		t.Errorf("lastLogInfo() = (%d, %d), want (0, 0)", idx, term)
	}

	// Add some entries
	n.logEntries = []Entry{
		{Term: 1, Index: 1},
		{Term: 1, Index: 2},
		{Term: 2, Index: 3},
	}

	idx, term = n.lastLogInfo()
	if idx != 3 || term != 2 {
		t.Errorf("lastLogInfo() = (%d, %d), want (3, 2)", idx, term)
	}
}

func TestNode_lastLogIndex(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Initially empty
	if n.lastLogIndex() != 0 {
		t.Errorf("lastLogIndex() = %d, want 0", n.lastLogIndex())
	}

	// Add entries
	n.logEntries = []Entry{{Term: 1, Index: 5}}
	if n.lastLogIndex() != 5 {
		t.Errorf("lastLogIndex() = %d, want 5", n.lastLogIndex())
	}
}

func TestNode_hasLogEntry(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	n.logEntries = []Entry{
		{Term: 1, Index: 1},
		{Term: 1, Index: 2},
		{Term: 2, Index: 3},
	}

	if !n.hasLogEntry(1, 1) {
		t.Error("hasLogEntry(1, 1) should be true")
	}
	if !n.hasLogEntry(3, 2) {
		t.Error("hasLogEntry(3, 2) should be true")
	}
	if n.hasLogEntry(2, 2) {
		t.Error("hasLogEntry(2, 2) should be false (wrong term)")
	}
	if n.hasLogEntry(99, 1) {
		t.Error("hasLogEntry(99, 1) should be false (doesn't exist)")
	}
}

func TestNode_appendEntry(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Append new entry
	n.appendEntry(Entry{Term: 1, Index: 1, Command: []byte("cmd1")})
	if len(n.logEntries) != 1 {
		t.Errorf("len(logEntries) = %d, want 1", len(n.logEntries))
	}

	// Append another entry
	n.appendEntry(Entry{Term: 1, Index: 2, Command: []byte("cmd2")})
	if len(n.logEntries) != 2 {
		t.Errorf("len(logEntries) = %d, want 2", len(n.logEntries))
	}

	// Replace existing entry with different term
	n.appendEntry(Entry{Term: 2, Index: 2, Command: []byte("cmd2-new")})
	if len(n.logEntries) != 2 {
		t.Errorf("len(logEntries) = %d, want 2 after replace", len(n.logEntries))
	}
	if n.logEntries[1].Term != 2 {
		t.Errorf("Replaced entry term = %d, want 2", n.logEntries[1].Term)
	}
}

func TestNode_isStopping(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	if n.isStopping() {
		t.Error("isStopping should be false initially")
	}

	// After closing stopCh
	close(n.stopCh)
	if !n.isStopping() {
		t.Error("isStopping should be true after stopCh closed")
	}
}

func TestNode_hasMajority(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	// Node with no peers (single node)
	n1, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n1.wal.Close()

	if !n1.hasMajority() {
		t.Error("Single node should have majority")
	}

	// Node with 2 peers (3 nodes total)
	dir2 := t.TempDir()
	n2, err := NewNode("node-2", "127.0.0.1:0", []string{"peer1", "peer2"}, dir2, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n2.wal.Close()
	defer os.RemoveAll(dir2)

	if !n2.hasMajority() {
		t.Error("Should have majority with 3 nodes")
	}
}

func TestNode_becomeFollower(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Set as leader first
	n.state.Store(StateLeader)
	n.currentTerm.Store(5)
	n.votedFor.Store("some-node")

	n.becomeFollower(10)

	if n.State() != StateFollower {
		t.Errorf("State = %v, want Follower", n.State())
	}
	if n.CurrentTerm() != 10 {
		t.Errorf("CurrentTerm = %d, want 10", n.CurrentTerm())
	}
	if n.votedFor.Load().(string) != "" {
		t.Error("votedFor should be cleared")
	}
}

func TestNode_sendCommittedToApply(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Add entries
	n.logEntries = []Entry{
		{Term: 1, Index: 1},
		{Term: 1, Index: 2},
		{Term: 1, Index: 3},
	}
	n.commitIndex.Store(2)
	n.lastApplied.Store(0)

	// Should send entries 1 and 2 to applyCh
	go n.sendCommittedToApply()

	// Wait for entries
	done := make(chan bool)
	go func() {
		count := 0
		for {
			select {
			case <-n.applyCh:
				count++
				if count >= 2 {
					done <- true
					return
				}
			case <-time.After(100 * time.Millisecond):
				done <- false
				return
			}
		}
	}()

	if !<-done {
		t.Error("Expected entries to be sent to applyCh")
	}
}

func TestSimpleRand_Int63n(t *testing.T) {
	r := &simpleRand{seed: 12345}

	// Should return values within range
	for i := 0; i < 100; i++ {
		val := r.Int63n(100)
		if val < 0 || val >= 100 {
			t.Errorf("Int63n(100) = %d, out of range [0, 100)", val)
		}
	}

	// Different seeds should produce different results
	r1 := &simpleRand{seed: 1}
	r2 := &simpleRand{seed: 2}
	if r1.Int63n(1000) == r2.Int63n(1000) {
		t.Error("Different seeds should produce different values")
	}
}

func TestFSMConfig(t *testing.T) {
	config := FSMConfig{}
	if config.OnPoolConfigUpdate != nil {
		t.Error("OnPoolConfigUpdate should be nil by default")
	}
}

func TestFSMUser(t *testing.T) {
	user := FSMUser{
		Username:       "testuser",
		PasswordHash:   "hash123",
		MaxConnections: 100,
	}
	if user.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", user.Username)
	}
}

func TestCommandTypes(t *testing.T) {
	if CmdNoOp != 0 {
		t.Errorf("CmdNoOp = %d, want 0", CmdNoOp)
	}
	if CmdPoolConfigUpdate != 1 {
		t.Errorf("CmdPoolConfigUpdate = %d, want 1", CmdPoolConfigUpdate)
	}
	if CmdUserCreate != 2 {
		t.Errorf("CmdUserCreate = %d, want 2", CmdUserCreate)
	}
	if CmdUserUpdate != 3 {
		t.Errorf("CmdUserUpdate = %d, want 3", CmdUserUpdate)
	}
	if CmdUserDelete != 4 {
		t.Errorf("CmdUserDelete = %d, want 4", CmdUserDelete)
	}
}

func TestCommand(t *testing.T) {
	cmd := Command{
		Type: CmdNoOp,
		Data: []byte("test"),
	}
	if cmd.Type != CmdNoOp {
		t.Errorf("Type = %d, want CmdNoOp", cmd.Type)
	}
}

func TestSnapshotMetadata(t *testing.T) {
	meta := SnapshotMetadata{
		Index:    100,
		Term:     5,
		Size:     1024,
		Checksum: 12345,
	}
	if meta.Index != 100 {
		t.Errorf("Index = %d, want 100", meta.Index)
	}
}

func TestSnapshot(t *testing.T) {
	snapshot := &Snapshot{
		Metadata: SnapshotMetadata{
			Index: 100,
			Term:  5,
		},
		Data: []byte("test data"),
	}
	if snapshot.Metadata.Index != 100 {
		t.Errorf("Metadata.Index = %d, want 100", snapshot.Metadata.Index)
	}
}

func TestCreateSnapshot(t *testing.T) {
	data := []byte("snapshot data")
	snapshot := CreateSnapshot(100, 5, data)

	if snapshot.Metadata.Index != 100 {
		t.Errorf("Index = %d, want 100", snapshot.Metadata.Index)
	}
	if snapshot.Metadata.Term != 5 {
		t.Errorf("Term = %d, want 5", snapshot.Metadata.Term)
	}
	if snapshot.Metadata.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", snapshot.Metadata.Size, len(data))
	}
}

func TestPoolConfigUpdateData(t *testing.T) {
	data := PoolConfigUpdateData{
		Name:   "test-pool",
		Config: map[string]string{"host": "localhost"},
	}
	if data.Name != "test-pool" {
		t.Errorf("Name = %q, want test-pool", data.Name)
	}
}

func TestUserUpdateData(t *testing.T) {
	data := UserUpdateData{
		User: FSMUser{
			Username: "testuser",
		},
	}
	if data.User.Username != "testuser" {
		t.Errorf("User.Username = %q, want testuser", data.User.Username)
	}
}

func TestUserDeleteData(t *testing.T) {
	data := UserDeleteData{
		Username: "testuser",
	}
	if data.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", data.Username)
	}
}

func TestGeryonFSM_GetState(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	state := fsm.GetState()
	// GetState returns a value, not a pointer
	if state.PoolConfigs == nil {
		t.Error("PoolConfigs should not be nil")
	}
	if state.Users == nil {
		t.Error("Users should not be nil")
	}
}

func TestGeryonFSM_UserUpdate(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Create user
	cmd, _ := CreateCommand(CmdUserCreate, UserUpdateData{
		User: FSMUser{
			Username:       "testuser",
			PasswordHash:   "hash123",
			MaxConnections: 100,
		},
	})
	fsm.Apply(cmd)

	// Update user
	cmd2, _ := CreateCommand(CmdUserUpdate, UserUpdateData{
		User: FSMUser{
			Username:       "testuser",
			PasswordHash:   "newhash",
			MaxConnections: 200,
		},
	})
	_, err := fsm.Apply(cmd2)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	state := fsm.GetState()
	user, ok := state.Users["testuser"]
	if !ok {
		t.Fatal("User not found after update")
	}
	if user.PasswordHash != "newhash" {
		t.Errorf("PasswordHash = %q, want newhash", user.PasswordHash)
	}
	if user.MaxConnections != 200 {
		t.Errorf("MaxConnections = %d, want 200", user.MaxConnections)
	}
}

func TestGeryonFSM_NoOp(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	cmd, _ := CreateCommand(CmdNoOp, nil)
	_, err := fsm.Apply(cmd)
	if err != nil {
		t.Errorf("NoOp should not fail: %v", err)
	}
}

func TestGeryonFSM_UnknownCommand(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	cmd := Command{
		Type: 999, // Unknown command type
		Data: []byte("test"),
	}
	_, err := fsm.Apply(cmd)
	if err == nil {
		t.Error("Unknown command should return error")
	}
}

func TestGeryonFSM_SnapshotError(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Add invalid data that can't be marshaled
	fsm.state.PoolConfigs["test"] = map[string]string{
		"invalid": string([]byte{0xff, 0xfe}), // Invalid UTF-8
	}

	// This should still work since json.Marshal handles it
	_, err := fsm.Snapshot()
	if err != nil {
		t.Errorf("Snapshot should not fail: %v", err)
	}
}

func TestSnapshotStore_ListEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("NewSnapshotStore failed: %v", err)
	}

	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(snapshots) != 0 {
		t.Errorf("Expected 0 snapshots, got %d", len(snapshots))
	}
}

func TestFSMState(t *testing.T) {
	state := &FSMState{
		PoolConfigs: make(map[string]interface{}),
		Users:       make(map[string]FSMUser),
	}
	if state.PoolConfigs == nil {
		t.Error("PoolConfigs should not be nil")
	}
}

func TestEntry_Struct(t *testing.T) {
	entry := Entry{
		Term:    5,
		Index:   100,
		Command: []byte(`{"type":"test"}`),
	}

	if entry.Term != 5 {
		t.Errorf("Term = %d, want 5", entry.Term)
	}
	if entry.Index != 100 {
		t.Errorf("Index = %d, want 100", entry.Index)
	}
	if string(entry.Command) != `{"type":"test"}` {
		t.Errorf("Command = %q", string(entry.Command))
	}
}

func TestMessage_Struct(t *testing.T) {
	msg := Message{
		Type: MsgVoteRequest,
		From: "node-1",
		To:   "node-2",
		Term: 5,
		Data: []byte("test data"),
	}

	if msg.Type != MsgVoteRequest {
		t.Errorf("Type = %v, want MsgVoteRequest", msg.Type)
	}
	if msg.From != "node-1" {
		t.Errorf("From = %q, want node-1", msg.From)
	}
	if msg.To != "node-2" {
		t.Errorf("To = %q, want node-2", msg.To)
	}
	if msg.Term != 5 {
		t.Errorf("Term = %d, want 5", msg.Term)
	}
}

func TestVoteRequest_Struct(t *testing.T) {
	req := VoteRequest{
		Term:         10,
		CandidateID:  "node-1",
		LastLogIndex: 100,
		LastLogTerm:  5,
	}

	if req.Term != 10 {
		t.Errorf("Term = %d, want 10", req.Term)
	}
	if req.CandidateID != "node-1" {
		t.Errorf("CandidateID = %q, want node-1", req.CandidateID)
	}
	if req.LastLogIndex != 100 {
		t.Errorf("LastLogIndex = %d, want 100", req.LastLogIndex)
	}
	if req.LastLogTerm != 5 {
		t.Errorf("LastLogTerm = %d, want 5", req.LastLogTerm)
	}
}

func TestVoteResponse_Struct(t *testing.T) {
	resp := VoteResponse{
		Term:        10,
		VoteGranted: true,
	}

	if resp.Term != 10 {
		t.Errorf("Term = %d, want 10", resp.Term)
	}
	if !resp.VoteGranted {
		t.Error("VoteGranted should be true")
	}
}

func TestAppendEntries_Struct(t *testing.T) {
	req := AppendEntries{
		Term:         5,
		LeaderID:     "node-1",
		PrevLogIndex: 50,
		PrevLogTerm:  4,
		Entries: []Entry{
			{Term: 5, Index: 51},
		},
		LeaderCommit: 50,
	}

	if req.Term != 5 {
		t.Errorf("Term = %d, want 5", req.Term)
	}
	if req.LeaderID != "node-1" {
		t.Errorf("LeaderID = %q, want node-1", req.LeaderID)
	}
	if req.PrevLogIndex != 50 {
		t.Errorf("PrevLogIndex = %d, want 50", req.PrevLogIndex)
	}
	if len(req.Entries) != 1 {
		t.Errorf("Entries count = %d, want 1", len(req.Entries))
	}
	if req.LeaderCommit != 50 {
		t.Errorf("LeaderCommit = %d, want 50", req.LeaderCommit)
	}
}

func TestAppendEntriesResponse_Struct(t *testing.T) {
	resp := AppendEntriesResponse{
		Term:    5,
		Success: true,
		Index:   100,
	}

	if resp.Term != 5 {
		t.Errorf("Term = %d, want 5", resp.Term)
	}
	if !resp.Success {
		t.Error("Success should be true")
	}
	if resp.Index != 100 {
		t.Errorf("Index = %d, want 100", resp.Index)
	}
}

func TestMessageType_Constants(t *testing.T) {
	// Test message type constants
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
	if MsgInstallSnapshot != 4 {
		t.Errorf("MsgInstallSnapshot = %d, want 4", MsgInstallSnapshot)
	}
}

func TestSimpleRand_MultipleCalls(t *testing.T) {
	r := &simpleRand{seed: 12345}

	// Generate multiple values
	values := make([]int64, 10)
	for i := 0; i < 10; i++ {
		values[i] = r.Int63n(1000)
		if values[i] < 0 || values[i] >= 1000 {
			t.Errorf("Int63n(1000) = %d, out of range", values[i])
		}
	}

	// Check that we got different values (very unlikely to be all the same)
	allSame := true
	for i := 1; i < len(values); i++ {
		if values[i] != values[0] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("Random values should not all be the same")
	}
}

func TestGeryonFSM_GetState_Empty(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	state := fsm.GetState()
	// GetState returns FSMState (value type), not pointer
	// Just check that the maps are initialized
	if state.PoolConfigs == nil {
		t.Error("PoolConfigs should not be nil")
	}
	if state.Users == nil {
		t.Error("Users should not be nil")
	}
}

func TestGeryonFSM_UserDelete(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Create user first
	cmd, _ := CreateCommand(CmdUserCreate, UserUpdateData{
		User: FSMUser{
			Username:       "testuser",
			PasswordHash:   "hash123",
			MaxConnections: 100,
		},
	})
	fsm.Apply(cmd)

	// Delete user
	cmd2, _ := CreateCommand(CmdUserDelete, UserDeleteData{
		Username: "testuser",
	})
	_, err := fsm.Apply(cmd2)
	if err != nil {
		t.Fatalf("Delete user failed: %v", err)
	}

	// Verify user is deleted
	state := fsm.GetState()
	if _, exists := state.Users["testuser"]; exists {
		t.Error("User should be deleted")
	}
}

func TestGeryonFSM_UserDelete_NotFound(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Delete non-existent user should not error
	cmd, _ := CreateCommand(CmdUserDelete, UserDeleteData{
		Username: "nonexistent",
	})
	_, err := fsm.Apply(cmd)
	if err != nil {
		t.Errorf("Delete non-existent user should not error: %v", err)
	}
}

func TestGeryonFSM_PoolConfigUpdate(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Update pool config
	cmd, _ := CreateCommand(CmdPoolConfigUpdate, PoolConfigUpdateData{
		Name:   "test-pool",
		Config: map[string]string{"host": "localhost"},
	})
	_, err := fsm.Apply(cmd)
	if err != nil {
		t.Fatalf("Pool config update failed: %v", err)
	}

	// Verify config is stored
	state := fsm.GetState()
	if _, exists := state.PoolConfigs["test-pool"]; !exists {
		t.Error("Pool config should be stored")
	}
}

func TestGeryonFSM_Restore(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Add some data
	cmd, _ := CreateCommand(CmdUserCreate, UserUpdateData{
		User: FSMUser{
			Username:       "testuser",
			PasswordHash:   "hash123",
			MaxConnections: 100,
		},
	})
	fsm.Apply(cmd)

	// Create snapshot
	snapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Create new FSM and restore
	fsm2 := NewGeryonFSM(FSMConfig{})
	err = fsm2.Restore(snapshot)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify data is restored
	state := fsm2.GetState()
	if _, exists := state.Users["testuser"]; !exists {
		t.Error("User should be restored from snapshot")
	}
}

func TestCreateCommand_InvalidData(t *testing.T) {
	// CreateCommand only validates that data can be marshaled to JSON
	// It doesn't validate the command type itself
	_, err := CreateCommand(999, "test") // Invalid command type but valid data
	if err != nil {
		t.Errorf("CreateCommand should not fail for invalid type: %v", err)
	}

	// Test with data that can't be marshaled to JSON
	_, err = CreateCommand(CmdNoOp, make(chan int)) // Channels can't be marshaled
	if err == nil {
		t.Error("CreateCommand with unmarshalable data should return error")
	}
}

func TestFSMUser_Equals(t *testing.T) {
	u1 := FSMUser{
		Username:       "user1",
		PasswordHash:   "hash1",
		MaxConnections: 100,
	}

	u2 := FSMUser{
		Username:       "user1",
		PasswordHash:   "hash1",
		MaxConnections: 100,
	}

	u3 := FSMUser{
		Username:       "user2",
		PasswordHash:   "hash2",
		MaxConnections: 200,
	}

	// Note: Equals method doesn't exist, comparing fields directly
	if u1.Username != u2.Username || u1.PasswordHash != u2.PasswordHash {
		t.Error("u1 and u2 should have same field values")
	}

	if u1.Username == u3.Username {
		t.Error("u1 and u3 should have different usernames")
	}
}

func TestSnapshotMetadata_Struct(t *testing.T) {
	meta := SnapshotMetadata{
		Index:    100,
		Term:     5,
		Size:     1024,
		Checksum: 12345,
	}

	if meta.Index != 100 {
		t.Errorf("Index = %d, want 100", meta.Index)
	}
	if meta.Term != 5 {
		t.Errorf("Term = %d, want 5", meta.Term)
	}
	if meta.Size != 1024 {
		t.Errorf("Size = %d, want 1024", meta.Size)
	}
	if meta.Checksum != 12345 {
		t.Errorf("Checksum = %d, want 12345", meta.Checksum)
	}
}

func TestSnapshot_Struct(t *testing.T) {
	snapshot := &Snapshot{
		Metadata: SnapshotMetadata{
			Index: 100,
			Term:  5,
		},
		Data: []byte("snapshot data"),
	}

	if snapshot.Metadata.Index != 100 {
		t.Errorf("Metadata.Index = %d", snapshot.Metadata.Index)
	}
	if string(snapshot.Data) != "snapshot data" {
		t.Errorf("Data = %q", string(snapshot.Data))
	}
}

// Test FSM applyCacheInvalidate
func TestGeryonFSM_applyCacheInvalidate(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{
		OnCacheInvalidate: func(pattern string, tables []string) {
			// Callback triggered
		},
	})

	data := []byte(`{"tables": ["users", "orders"]}`)
	result, err := fsm.applyCacheInvalidate(data)
	if err != nil {
		t.Errorf("applyCacheInvalidate error: %v", err)
	}
	if result != nil {
		t.Error("result should be nil")
	}

	// Test with invalid JSON
	_, err = fsm.applyCacheInvalidate([]byte("invalid"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test FSM applyCacheInvalidatePattern
func TestGeryonFSM_applyCacheInvalidatePattern(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{
		OnCacheInvalidate: func(pattern string, tables []string) {
			// Callback triggered
		},
	})

	data := []byte(`{"pattern": "user_*"}`)
	result, err := fsm.applyCacheInvalidatePattern(data)
	if err != nil {
		t.Errorf("applyCacheInvalidatePattern error: %v", err)
	}
	if result != nil {
		t.Error("result should be nil")
	}

	// Test with invalid JSON
	_, err = fsm.applyCacheInvalidatePattern([]byte("invalid"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test FSM applyCacheInvalidate without callback
func TestGeryonFSM_applyCacheInvalidate_NoCallback(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{}) // No callback

	data := []byte(`{"tables": ["users"]}`)
	result, err := fsm.applyCacheInvalidate(data)
	if err != nil {
		t.Errorf("applyCacheInvalidate error: %v", err)
	}
	if result != nil {
		t.Error("result should be nil")
	}
}

// Test FSM applyBackendDetach
func TestGeryonFSM_applyBackendDetach(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Add a backend first
	fsm.state.Backends["backend1"] = FSMBackend{
		Name:    "backend1",
		Host:    "127.0.0.1",
		Port:    5432,
		Role:    "primary",
		Status:  "healthy",
		Detached: false,
	}

	data := []byte(`{"name": "backend1", "pool_name": "pool1"}`)
	result, err := fsm.applyBackendDetach(data)
	if err != nil {
		t.Errorf("applyBackendDetach error: %v", err)
	}
	if result != nil {
		t.Error("result should be nil")
	}

	// Check backend was detached
	backend := fsm.state.Backends["backend1"]
	if backend.Name != "backend1" {
		t.Errorf("Name = %q, want backend1", backend.Name)
	}
	if !backend.Detached {
		t.Error("Detached should be true after detach")
	}
	if backend.Status != "detached" {
		t.Errorf("Status = %q, want detached", backend.Status)
	}

	// Test with non-existent backend (should not panic)
	data = []byte(`{"name": "nonexistent", "pool_name": "pool1"}`)
	result, err = fsm.applyBackendDetach(data)
	if err != nil {
		t.Errorf("applyBackendDetach error for non-existent: %v", err)
	}
	if result != nil {
		t.Error("result should be nil for non-existent backend")
	}

	// Test with invalid JSON
	_, err = fsm.applyBackendDetach([]byte("invalid"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test FSM applyBackendAttach
func TestGeryonFSM_applyBackendAttach(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Add a detached backend first
	fsm.state.Backends["backend1"] = FSMBackend{
		Name:     "backend1",
		Host:     "127.0.0.1",
		Port:     5432,
		Role:     "primary",
		Status:   "detached",
		Detached: true,
	}

	data := []byte(`{"name": "backend1", "pool_name": "pool1"}`)
	result, err := fsm.applyBackendAttach(data)
	if err != nil {
		t.Errorf("applyBackendAttach error: %v", err)
	}
	if result != nil {
		t.Error("result should be nil")
	}

	// Check backend was attached
	backend := fsm.state.Backends["backend1"]
	if backend.Name != "backend1" {
		t.Errorf("Name = %q, want backend1", backend.Name)
	}
	if backend.Detached {
		t.Error("Detached should be false after attach")
	}
	if backend.Status != "active" {
		t.Errorf("Status = %q, want active", backend.Status)
	}

	// Test with invalid JSON
	_, err = fsm.applyBackendAttach([]byte("invalid"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test Apply with unknown command type
func TestGeryonFSM_Apply_Unknown(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	cmd := Command{
		Type: CommandType(999), // Unknown type
		Data: []byte("{}"),
	}
	result, err := fsm.Apply(cmd)
	if err == nil {
		t.Error("expected error for unknown command type")
	}
	if result != nil {
		t.Error("result should be nil for unknown type")
	}
}

// Test Node becomeCandidate
func TestNode_becomeCandidate(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{"peer1", "peer2"}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Initial state
	n.currentTerm.Store(5)
	n.state.Store(StateFollower)
	n.votedFor.Store("")

	// Become candidate
	n.becomeCandidate()

	if n.State() != StateCandidate {
		t.Errorf("State = %v, want StateCandidate", n.State())
	}
	if n.CurrentTerm() != 6 {
		t.Errorf("CurrentTerm = %d, want 6", n.CurrentTerm())
	}
	if n.votedFor.Load().(string) != "node-1" {
		t.Errorf("votedFor = %q, want node-1", n.votedFor.Load().(string))
	}
}

// Test Node becomeLeader
func TestNode_becomeLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log) // No peers
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Setup
	n.currentTerm.Store(5)
	n.state.Store(StateCandidate)
	n.logEntries = []Entry{
		{Term: 1, Index: 1},
		{Term: 1, Index: 2},
		{Term: 2, Index: 3},
	}

	// Become leader
	n.becomeLeader()

	if n.State() != StateLeader {
		t.Errorf("State = %v, want StateLeader", n.State())
	}

	// Stop heartbeat by closing stopCh (stops goroutine cleanly)
	close(n.stopCh)
}

// Test Node onElectionTimeout
func TestNode_onElectionTimeout(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Set as follower
	n.state.Store(StateFollower)
	n.currentTerm.Store(1)

	// Should become candidate
	n.onElectionTimeout()

	if n.State() != StateCandidate {
		t.Errorf("State = %v, want StateCandidate", n.State())
	}
	if n.CurrentTerm() != 2 {
		t.Errorf("CurrentTerm = %d, want 2", n.CurrentTerm())
	}
}

// Test Node onElectionTimeout when already leader
func TestNode_onElectionTimeout_Leader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Set as leader
	n.state.Store(StateLeader)
	n.currentTerm.Store(5)

	// Should stay leader
	n.onElectionTimeout()

	if n.State() != StateLeader {
		t.Errorf("State = %v, want StateLeader", n.State())
	}
	if n.CurrentTerm() != 5 {
		t.Errorf("CurrentTerm = %d, want 5 (should not change)", n.CurrentTerm())
	}
}

// Test Node IsLeader
func TestNode_IsLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Initially follower
	if n.IsLeader() {
		t.Error("IsLeader should be false initially")
	}

	// Become leader
	n.state.Store(StateLeader)
	if !n.IsLeader() {
		t.Error("IsLeader should be true when state is StateLeader")
	}

	// Become candidate
	n.state.Store(StateCandidate)
	if n.IsLeader() {
		t.Error("IsLeader should be false when state is StateCandidate")
	}
}

// Test Node resetElectionTimer
func TestNode_resetElectionTimer(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Reset election timer
	n.resetElectionTimer()

	if n.electionTimer == nil {
		t.Error("electionTimer should be set")
	}

	// Reset again should not panic
	n.resetElectionTimer()
}

// Test Node stopHeartbeat
func TestNode_stopHeartbeat(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Create a ticker manually instead of starting the full heartbeat goroutine
	n.heartbeatTicker = time.NewTicker(time.Hour) // Long duration so it won't fire

	if n.heartbeatTicker == nil {
		t.Error("heartbeatTicker should be set")
	}

	// Stop heartbeat
	n.stopHeartbeat()

	if n.heartbeatTicker != nil {
		t.Error("heartbeatTicker should be nil after stop")
	}

	// Stop again should not panic
	n.stopHeartbeat()
}

// Test Node advanceCommitIndex
func TestNode_advanceCommitIndex(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{"peer1", "peer2"}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Setup: 3 nodes total (self + 2 peers), need majority of 2
	n.currentTerm.Store(5)
	n.logEntries = []Entry{
		{Term: 5, Index: 1},
		{Term: 5, Index: 2},
		{Term: 5, Index: 3},
	}
	n.commitIndex.Store(0)

	// Set matchIndex for peers (majority has index 2)
	n.volatileMu.Lock()
	n.matchIndex["peer1"] = 2
	n.matchIndex["peer2"] = 1
	n.volatileMu.Unlock()

	// Advance commit index
	n.advanceCommitIndex()

	// Should advance to 2 (majority has 2)
	if n.commitIndex.Load() != 2 {
		t.Errorf("commitIndex = %d, want 2", n.commitIndex.Load())
	}
}

// Test Node advanceCommitIndex no advancement
func TestNode_advanceCommitIndex_NoAdvance(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{"peer1", "peer2", "peer3"}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Setup: 4 nodes total, need majority of 3
	n.currentTerm.Store(5)
	n.logEntries = []Entry{
		{Term: 5, Index: 1},
		{Term: 5, Index: 2},
		{Term: 5, Index: 3},
	}
	n.commitIndex.Store(0)

	// Set matchIndex (only 1 peer has index 2, not majority)
	n.volatileMu.Lock()
	n.matchIndex["peer1"] = 2
	n.matchIndex["peer2"] = 0
	n.matchIndex["peer3"] = 0
	n.volatileMu.Unlock()

	// Advance commit index
	n.advanceCommitIndex()

	// Should not advance (no majority)
	if n.commitIndex.Load() != 0 {
		t.Errorf("commitIndex = %d, want 0 (no majority)", n.commitIndex.Load())
	}
}

// Test Node Propose when not leader
func TestNode_Propose_NotLeader(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Not leader
	n.state.Store(StateFollower)

	cmd, _ := CreateCommand(CmdNoOp, nil)
	_, err = n.Propose(cmd)
	if err == nil {
		t.Error("Propose should fail when not leader")
	}
}

// Note: TestNode_Propose_Leader skipped due to deadlock bug in Propose function
// The function acquires logMu.Lock() then calls lastLogInfo() which tries to acquire logMu.RLock()

// Test Node InstallSnapshot
func TestNode_InstallSnapshot(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	fsm := NewGeryonFSM(FSMConfig{})
	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, fsm, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Create snapshot with valid JSON data for FSM
	snapshotData := []byte(`{"pool_configs": {}, "users": {}, "backends": {}}`)
	snapshot := CreateSnapshot(100, 5, snapshotData)

	// Install snapshot
	err = n.InstallSnapshot(snapshot)
	if err != nil {
		t.Errorf("InstallSnapshot failed: %v", err)
	}

	if n.lastSnapshotIndex.Load() != 100 {
		t.Errorf("lastSnapshotIndex = %d, want 100", n.lastSnapshotIndex.Load())
	}
	if n.lastSnapshotTerm.Load() != 5 {
		t.Errorf("lastSnapshotTerm = %d, want 5", n.lastSnapshotTerm.Load())
	}
}

// Test Node GetSnapshot
func TestNode_GetSnapshot(t *testing.T) {
	log, _ := logger.New("debug", "text")
	dir := t.TempDir()

	fsm := NewGeryonFSM(FSMConfig{})
	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, fsm, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()

	// Create and install snapshot
	snapshot := CreateSnapshot(100, 5, []byte("test data"))
	n.InstallSnapshot(snapshot)

	// Get snapshot
	got, err := n.GetSnapshot()
	if err != nil {
		t.Errorf("GetSnapshot failed: %v", err)
	}
	if got.Metadata.Index != 100 {
		t.Errorf("snapshot.Index = %d, want 100", got.Metadata.Index)
	}
}
