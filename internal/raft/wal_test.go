package raft

import (
	"os"
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

func TestWAL_CreateAndAppend(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.log"

	wal, err := NewWAL(walPath, false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	entry := Entry{
		Term:    1,
		Index:   1,
		Command: []byte(`{"type": 1, "data": "test"}`),
	}

	if err := wal.Append(entry); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if wal.LastIndex() != 1 {
		t.Errorf("LastIndex = %d, want 1", wal.LastIndex())
	}
}

func TestWAL_ReadEntries(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.log"

	wal, err := NewWAL(walPath, false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	entries := []Entry{
		{Term: 1, Index: 1, Command: []byte(`{"cmd":1}`)},
		{Term: 1, Index: 2, Command: []byte(`{"cmd":2}`)},
		{Term: 2, Index: 3, Command: []byte(`{"cmd":3}`)},
	}

	for _, entry := range entries {
		if err := wal.Append(entry); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	wal.Close()

	// Reopen and read
	wal2, err := NewWAL(walPath, false)
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	readEntries, err := wal2.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}

	if len(readEntries) != 3 {
		t.Errorf("Read %d entries, want 3", len(readEntries))
	}
}

func TestWAL_Truncate(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.log"

	wal, err := NewWAL(walPath, false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	for i := uint64(1); i <= 5; i++ {
		entry := Entry{Term: 1, Index: i, Command: []byte(`{"cmd":1}`)}
		if err := wal.Append(entry); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	wal.Close()

	// Reopen and truncate
	wal2, err := NewWAL(walPath, false)
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	if err := wal2.Truncate(3); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	readEntries, err := wal2.ReadEntries(1)
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}

	if len(readEntries) != 3 {
		t.Errorf("After truncate, read %d entries, want 3", len(readEntries))
	}
}

func TestGeryonFSM_CreateAndApply(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Test pool config update
	cmd, err := CreateCommand(CmdPoolConfigUpdate, PoolConfigUpdateData{
		Name:   "test-pool",
		Config: map[string]string{"host": "localhost"},
	})
	if err != nil {
		t.Fatalf("CreateCommand failed: %v", err)
	}

	_, err = fsm.Apply(cmd)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	state := fsm.GetState()
	if _, ok := state.PoolConfigs["test-pool"]; !ok {
		t.Error("Pool config not found after apply")
	}
}

func TestGeryonFSM_UserCRUD(t *testing.T) {
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

	state := fsm.GetState()
	if user, ok := state.Users["testuser"]; !ok {
		t.Error("User not found after create")
	} else if user.PasswordHash != "hash123" {
		t.Errorf("User password hash = %q, want hash123", user.PasswordHash)
	}

	// Delete user
	cmd, _ = CreateCommand(CmdUserDelete, UserDeleteData{Username: "testuser"})
	fsm.Apply(cmd)

	state = fsm.GetState()
	if _, ok := state.Users["testuser"]; ok {
		t.Error("User still exists after delete")
	}
}

func TestGeryonFSM_SnapshotAndRestore(t *testing.T) {
	fsm := NewGeryonFSM(FSMConfig{})

	// Add some state
	cmd1, _ := CreateCommand(CmdPoolConfigUpdate, PoolConfigUpdateData{
		Name:   "pool1",
		Config: map[string]string{"host": "localhost"},
	})
	fsm.Apply(cmd1)

	cmd2, _ := CreateCommand(CmdUserCreate, UserUpdateData{
		User: FSMUser{Username: "user1", PasswordHash: "hash1"},
	})
	fsm.Apply(cmd2)

	// Create snapshot
	snapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Create new FSM and restore
	fsm2 := NewGeryonFSM(FSMConfig{})
	if err := fsm2.Restore(snapshot); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	state := fsm2.GetState()
	if _, ok := state.PoolConfigs["pool1"]; !ok {
		t.Error("Pool config not found after restore")
	}
	if _, ok := state.Users["user1"]; !ok {
		t.Error("User not found after restore")
	}
}

func TestSnapshotStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSnapshotStore(dir, 3)
	if err != nil {
		t.Fatalf("NewSnapshotStore failed: %v", err)
	}

	data := []byte(`{"test": "data"}`)
	snapshot := CreateSnapshot(100, 5, data)

	if err := store.Save(snapshot); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Metadata.Index != 100 {
		t.Errorf("Loaded index = %d, want 100", loaded.Metadata.Index)
	}
	if loaded.Metadata.Term != 5 {
		t.Errorf("Loaded term = %d, want 5", loaded.Metadata.Term)
	}
}

func TestSnapshotStore_CleanupOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSnapshotStore(dir, 2) // Keep only 2 snapshots
	if err != nil {
		t.Fatalf("NewSnapshotStore failed: %v", err)
	}

	// Create 4 snapshots
	for i := uint64(1); i <= 4; i++ {
		data := []byte(`{"index": ` + string(rune('0'+i)) + `}`)
		snapshot := CreateSnapshot(i*100, 1, data)
		if err := store.Save(snapshot); err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	// List snapshots
	snapshots, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(snapshots) != 2 {
		t.Errorf("Expected 2 snapshots after cleanup, got %d", len(snapshots))
	}
}

func TestCreateCommand(t *testing.T) {
	cmd, err := CreateCommand(CmdNoOp, nil)
	if err != nil {
		t.Fatalf("CreateCommand failed: %v", err)
	}
	if cmd.Type != CmdNoOp {
		t.Errorf("Command type = %d, want CmdNoOp", cmd.Type)
	}
}

func TestNodeWithFSM(t *testing.T) {
	log, _ := logger.New("debug", "text")
	fsm := NewGeryonFSM(FSMConfig{})
	dir := t.TempDir()

	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, "", nil, fsm, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.wal.Close()
	defer os.RemoveAll(dir)

	if n.fsm != fsm {
		t.Error("FSM not set correctly")
	}
	if n.snapshotStore == nil {
		t.Error("Snapshot store not initialized")
	}
}
