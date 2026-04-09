package raft

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SnapshotMetadata contains metadata about a snapshot.
type SnapshotMetadata struct {
	Index     uint64    `json:"index"`
	Term      uint64    `json:"term"`
	Timestamp time.Time `json:"timestamp"`
	Size      int64     `json:"size"`
	Checksum  uint32    `json:"checksum"`
}

// Snapshot represents a point-in-time snapshot of the state machine.
type Snapshot struct {
	Metadata SnapshotMetadata `json:"metadata"`
	Data     []byte           `json:"data"`
}

// SnapshotStore handles snapshot persistence.
type SnapshotStore struct {
	mu       sync.RWMutex
	dir      string
	maxCount int // Maximum number of snapshots to keep
}

// NewSnapshotStore creates a new snapshot store.
func NewSnapshotStore(dir string, maxCount int) (*SnapshotStore, error) {
	if maxCount < 1 {
		maxCount = 3 // Default to keeping 3 snapshots
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	return &SnapshotStore{
		dir:      dir,
		maxCount: maxCount,
	}, nil
}

// Save saves a snapshot to disk.
func (s *SnapshotStore) Save(snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate filename based on index and term
	filename := fmt.Sprintf("snapshot-%d-%d.json.gz", snapshot.Metadata.Index, snapshot.Metadata.Term)
	path := filepath.Join(s.dir, filename)

	// Compress and write snapshot
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create snapshot file: %w", err)
	}
	defer file.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	// Write metadata
	metadataJSON, err := json.Marshal(snapshot.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Write length-prefixed metadata
	if err := writeLengthPrefixed(gzWriter, metadataJSON); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Write data
	if err := writeLengthPrefixed(gzWriter, snapshot.Data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	// Flush gzip
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to flush gzip: %w", err)
	}

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat snapshot: %w", err)
	}

	snapshot.Metadata.Size = stat.Size()

	// Clean up old snapshots
	return s.cleanupOldSnapshots()
}

// Load loads the most recent snapshot from disk.
func (s *SnapshotStore) Load() (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// List all snapshot files
	files, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no snapshots found")
	}

	// Sort by index (descending) and take the most recent
	sort.Slice(files, func(i, j int) bool {
		return files[i].Index > files[j].Index
	})

	mostRecent := files[0]
	return s.loadSnapshotFile(mostRecent.Path)
}

// LoadAt loads a snapshot at a specific index.
func (s *SnapshotStore) LoadAt(index uint64) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	files, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if file.Index == index {
			return s.loadSnapshotFile(file.Path)
		}
	}

	return nil, fmt.Errorf("snapshot not found at index %d", index)
}

// List returns a list of available snapshots.
func (s *SnapshotStore) List() ([]SnapshotMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	files, err := s.listSnapshots()
	if err != nil {
		return nil, err
	}

	metadata := make([]SnapshotMetadata, len(files))
	for i, file := range files {
		metadata[i] = SnapshotMetadata{
			Index:     file.Index,
			Term:      file.Term,
			Timestamp: file.Timestamp,
			Size:      file.Size,
		}
	}

	return metadata, nil
}

// Delete deletes a snapshot at the given index.
func (s *SnapshotStore) Delete(index uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := s.listSnapshots()
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.Index == index {
			return os.Remove(file.Path)
		}
	}

	return fmt.Errorf("snapshot not found at index %d", index)
}

// snapshotFile represents a snapshot file on disk.
type snapshotFile struct {
	Path      string
	Index     uint64
	Term      uint64
	Timestamp time.Time
	Size      int64
}

// listSnapshots lists all snapshot files in the directory.
func (s *SnapshotStore) listSnapshots() ([]snapshotFile, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	var files []snapshotFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasPrefix(name, "snapshot-") || !strings.HasSuffix(name, ".json.gz") {
			continue
		}

		// Parse filename: snapshot-{index}-{term}.json.gz
		var index, term uint64
		if _, err := fmt.Sscanf(name, "snapshot-%d-%d.json.gz", &index, &term); err != nil {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, snapshotFile{
			Path:      filepath.Join(s.dir, name),
			Index:     index,
			Term:      term,
			Timestamp: info.ModTime(),
			Size:      info.Size(),
		})
	}

	return files, nil
}

// loadSnapshotFile loads a specific snapshot file.
func (s *SnapshotStore) loadSnapshotFile(path string) (*Snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Read metadata
	metadataJSON, err := readLengthPrefixed(gzReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata SnapshotMetadata
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	// Read data
	data, err := readLengthPrefixed(gzReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Verify checksum
	checksum := crc32.ChecksumIEEE(data)
	if checksum != metadata.Checksum {
		return nil, fmt.Errorf("checksum mismatch: expected %x, got %x", metadata.Checksum, checksum)
	}

	return &Snapshot{
		Metadata: metadata,
		Data:     data,
	}, nil
}

// cleanupOldSnapshots removes old snapshots, keeping only maxCount.
func (s *SnapshotStore) cleanupOldSnapshots() error {
	files, err := s.listSnapshots()
	if err != nil {
		return err
	}

	if len(files) <= s.maxCount {
		return nil
	}

	// Sort by timestamp (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Timestamp.Before(files[j].Timestamp)
	})

	// Delete oldest snapshots
	toDelete := len(files) - s.maxCount
	for i := 0; i < toDelete; i++ {
		if err := os.Remove(files[i].Path); err != nil {
			// Log but continue
			continue
		}
	}

	return nil
}

// writeLengthPrefixed writes data with a length prefix.
func writeLengthPrefixed(w io.Writer, data []byte) error {
	length := uint32(len(data))
	if err := writeUint32(w, length); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// readLengthPrefixed reads data with a length prefix.
func readLengthPrefixed(r io.Reader) ([]byte, error) {
	length, err := readUint32(r)
	if err != nil {
		return nil, err
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	return data, nil
}

// writeUint32 writes a uint32 in big-endian format.
func writeUint32(w io.Writer, v uint32) error {
	buf := make([]byte, 4)
	buf[0] = byte(v >> 24)
	buf[1] = byte(v >> 16)
	buf[2] = byte(v >> 8)
	buf[3] = byte(v)
	_, err := w.Write(buf)
	return err
}

// readUint32 reads a uint32 in big-endian format.
func readUint32(r io.Reader) (uint32, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3]), nil
}

// CreateSnapshot creates a new snapshot from the given state.
func CreateSnapshot(index, term uint64, data []byte) *Snapshot {
	checksum := crc32.ChecksumIEEE(data)

	return &Snapshot{
		Metadata: SnapshotMetadata{
			Index:     index,
			Term:      term,
			Timestamp: time.Now(),
			Size:      int64(len(data)),
			Checksum:  checksum,
		},
		Data: data,
	}
}

// SnapshotInstaller handles installing snapshots on follower nodes.
type SnapshotInstaller struct {
	store *SnapshotStore
	fsm   FSM
}

// NewSnapshotInstaller creates a new snapshot installer.
func NewSnapshotInstaller(store *SnapshotStore, fsm FSM) *SnapshotInstaller {
	return &SnapshotInstaller{
		store: store,
		fsm:   fsm,
	}
}

// Install installs a snapshot and restores the FSM.
func (si *SnapshotInstaller) Install(snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}

	// Save snapshot to disk
	if err := si.store.Save(snapshot); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	// Restore FSM from snapshot
	if err := si.fsm.Restore(snapshot.Data); err != nil {
		return fmt.Errorf("failed to restore FSM: %w", err)
	}

	return nil
}
