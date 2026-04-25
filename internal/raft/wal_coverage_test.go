package raft

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/GeryonProxy/geryon/internal/logger"
)

// --- WAL open with bad magic (version check) ---

func TestWAL_Open_BadVersion_Cov(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Create file with valid magic but wrong version
	file, _ := os.Create(walPath)
	binary.Write(file, binary.BigEndian, walMagic)
	binary.Write(file, binary.BigEndian, uint16(99)) // wrong version
	file.Close()

	wal, err := NewWAL(walPath, false)
	if wal != nil {
		wal.Close()
	}
	if err == nil {
		t.Error("Should fail for wrong WAL version")
	}
}

// --- WAL Truncate to beginning (keep all) ---

func TestWAL_TruncateKeepAll(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal.log"), false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		wal.Append(Entry{Term: 1, Index: uint64(i + 1), Command: []byte(`{"type":1}`)})
	}

	// Truncate keeping entries from index 1 (= keep all)
	err = wal.Truncate(1)
	if err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	entries, err := wal.ReadEntries(0)
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries after truncate(1), got %d", len(entries))
	}
	wal.Close()
}

// --- WAL Append with sync enabled ---

func TestWAL_Append_WithSync(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal_sync.log"), true)
	if err != nil {
		t.Fatalf("NewWAL with sync failed: %v", err)
	}

	err = wal.Append(Entry{Term: 1, Index: 1, Command: []byte(`{"type":1}`)})
	if err != nil {
		t.Fatalf("Append with sync failed: %v", err)
	}

	wal.Close()
}

// --- WAL AppendBatch with sync ---

func TestWAL_AppendBatch_WithSync(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal_batch_sync.log"), true)
	if err != nil {
		t.Fatalf("NewWAL with sync failed: %v", err)
	}

	entries := []Entry{
		{Term: 1, Index: 1, Command: []byte(`{"type":1}`)},
		{Term: 1, Index: 2, Command: []byte(`{"type":1}`)},
	}
	err = wal.AppendBatch(entries)
	if err != nil {
		t.Fatalf("AppendBatch with sync failed: %v", err)
	}
	wal.Close()
}

// --- WAL AppendBatch empty slice ---

func TestWAL_AppendBatch_EmptyCov(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal_empty.log"), false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	err = wal.AppendBatch(nil)
	if err != nil {
		t.Errorf("AppendBatch(nil) should return nil, got: %v", err)
	}
	wal.Close()
}

// --- WAL ReadEntries with seek error (closed file) ---

func TestWAL_ReadEntries_AfterClose(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal.log"), false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	wal.Append(Entry{Term: 1, Index: 1, Command: []byte(`{"type":1}`)})
	wal.Close()

	// ReadEntries on closed file should fail
	_, err = wal.ReadEntries(0)
	if err == nil {
		t.Error("ReadEntries should fail on closed WAL")
	}
}

// --- WAL LastIndex/LastTerm after append ---

func TestWAL_LastIndexTerm(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewWAL(filepath.Join(dir, "wal.log"), false)
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if wal.LastIndex() != 0 {
		t.Errorf("LastIndex = %d, want 0", wal.LastIndex())
	}
	if wal.LastTerm() != 0 {
		t.Errorf("LastTerm = %d, want 0", wal.LastTerm())
	}

	wal.Append(Entry{Term: 3, Index: 5, Command: []byte(`{"type":1}`)})

	if wal.LastIndex() != 5 {
		t.Errorf("LastIndex = %d, want 5", wal.LastIndex())
	}
	if wal.LastTerm() != 3 {
		t.Errorf("LastTerm = %d, want 3", wal.LastTerm())
	}
	wal.Close()
}

// --- WAL Close when file is nil ---

func TestWAL_Close_NilFileCov(t *testing.T) {
	w := &WAL{}
	err := w.Close()
	if err != nil {
		t.Errorf("Close on nil file should succeed, got: %v", err)
	}
}

// --- WAL logf when logger is nil ---

func TestWAL_Logf_NilLogger(t *testing.T) {
	w := &WAL{}
	// Should not panic
	w.logf("test %s", "message")
}

// --- SnapshotStore Save error path (use nil snapshot instead, covered elsewhere) ---

func TestSnapshotStore_Save_NilSnapshotCov(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSnapshotStore(dir, 3)

	err := store.Save(nil)
	if err == nil {
		t.Error("Save should fail for nil snapshot")
	}
}

// --- SnapshotStore LoadAt not found ---

func TestSnapshotStore_LoadAt_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSnapshotStore(dir, 3)

	_, err := store.LoadAt(99)
	if err == nil {
		t.Error("LoadAt should fail for non-existent index")
	}
}

// --- SnapshotStore List empty ---

func TestSnapshotStore_ListEmptyCov(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSnapshotStore(dir, 3)

	list, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List should return empty, got %d", len(list))
	}
}

// --- SnapshotStore Delete not found ---

func TestSnapshotStore_DeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSnapshotStore(dir, 3)

	err := store.Delete(999)
	if err == nil {
		t.Error("Delete should fail for non-existent snapshot")
	}
}

// --- SnapshotInstaller with successful FSM restore ---

type okFSM struct{}

func (f *okFSM) Apply(command Command) (interface{}, error) { return nil, nil }
func (f *okFSM) Restore(data []byte) error                  { return nil }
func (f *okFSM) Snapshot() ([]byte, error)                  { return []byte("snap"), nil }

func TestSnapshotInstaller_Install_Success(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSnapshotStore(dir, 3)

	fsm := &okFSM{}
	installer := NewSnapshotInstaller(store, fsm)

	data := []byte("test data")
	snapshot := &Snapshot{
		Metadata: SnapshotMetadata{
			Index:    1,
			Term:     1,
			Checksum: 0, // Not checked during install
		},
		Data: data,
	}

	err := installer.Install(snapshot)
	if err != nil {
		t.Errorf("Install failed: %v", err)
	}
}

// --- Node Propose as non-leader ---

func TestNode_Propose_NotLeaderCov(t *testing.T) {
	log, _ := logger.New("error", "text")
	dir := t.TempDir()
	n, err := NewNode("node-1", "127.0.0.1:0", []string{}, dir, "", nil, nil, log)
	if err != nil {
		t.Fatalf("NewNode failed: %v", err)
	}
	defer n.CloseWAL()

	cmd, _ := CreateCommand(CmdNoOp, nil)
	_, err = n.Propose(cmd)
	if err == nil {
		t.Error("Propose should fail when not leader")
	}
}
