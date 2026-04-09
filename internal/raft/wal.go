package raft

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// WAL (Write Ahead Log) provides durable storage for Raft log entries.
type WAL struct {
	mu         sync.RWMutex
	file       *os.File
	writer     *bufio.Writer
	path       string
	sync       bool // fsync after each write
	lastIndex  uint64
	lastTerm   uint64
}

// WALHeader is written at the start of the WAL file.
type WALHeader struct {
	Magic   uint32
	Version uint32
}

const (
	walMagic   = 0x57414C00 // "WAL\0"
	walVersion = 1
)

// LogRecord represents a single log entry in the WAL.
type LogRecord struct {
	Checksum uint32
	Length   uint32
	Data     []byte
}

// NewWAL creates a new WAL at the given path.
func NewWAL(path string, sync bool) (*WAL, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	w := &WAL{
		path: path,
		sync: sync,
	}

	// Open or create the WAL file
	if err := w.open(); err != nil {
		return nil, err
	}

	return w, nil
}

// open opens the WAL file, creating it if necessary.
func (w *WAL) open() error {
	// Try to open existing file
	file, err := os.OpenFile(w.path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open WAL: %w", err)
	}

	w.file = file
	w.writer = bufio.NewWriter(file)

	// Check if file is new or existing
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to stat WAL: %w", err)
	}

	if stat.Size() == 0 {
		// New file - write header
		if err := w.writeHeader(); err != nil {
			file.Close()
			return fmt.Errorf("failed to write WAL header: %w", err)
		}
	} else {
		// Existing file - verify header and recover entries
		if err := w.recover(); err != nil {
			file.Close()
			return fmt.Errorf("failed to recover WAL: %w", err)
		}
	}

	return nil
}

// writeHeader writes the WAL header.
func (w *WAL) writeHeader() error {
	header := WALHeader{
		Magic:   walMagic,
		Version: walVersion,
	}

	if err := binary.Write(w.writer, binary.BigEndian, header); err != nil {
		return err
	}

	return w.writer.Flush()
}

// recover reads the WAL and recovers the last index/term.
func (w *WAL) recover() error {
	// Read and verify header
	var header WALHeader
	if err := binary.Read(w.file, binary.BigEndian, &header); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	if header.Magic != walMagic {
		return fmt.Errorf("invalid WAL magic: %x", header.Magic)
	}

	if header.Version != walVersion {
		return fmt.Errorf("unsupported WAL version: %d", header.Version)
	}

	// Read all entries
	reader := bufio.NewReader(w.file)
	for {
		entry, err := w.readEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			// Corrupted entry - truncate here
			w.logf("WAL entry corrupted, truncating: %v", err)
			break
		}

		// Update last index/term
		if entry.Index > w.lastIndex {
			w.lastIndex = entry.Index
			w.lastTerm = entry.Term
		}
	}

	return nil
}

// readEntry reads a single entry from the WAL.
func (w *WAL) readEntry(reader *bufio.Reader) (Entry, error) {
	var record LogRecord

	// Read checksum
	if err := binary.Read(reader, binary.BigEndian, &record.Checksum); err != nil {
		return Entry{}, err
	}

	// Read length
	if err := binary.Read(reader, binary.BigEndian, &record.Length); err != nil {
		return Entry{}, err
	}

	// Read data
	record.Data = make([]byte, record.Length)
	if _, err := io.ReadFull(reader, record.Data); err != nil {
		return Entry{}, err
	}

	// Verify checksum
	checksum := crc32.ChecksumIEEE(record.Data)
	if checksum != record.Checksum {
		return Entry{}, fmt.Errorf("checksum mismatch: %x != %x", checksum, record.Checksum)
	}

	// Unmarshal entry
	var entry Entry
	if err := json.Unmarshal(record.Data, &entry); err != nil {
		return Entry{}, fmt.Errorf("failed to unmarshal entry: %w", err)
	}

	return entry, nil
}

// Append appends an entry to the WAL.
func (w *WAL) Append(entry Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Marshal entry
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	// Calculate checksum
	checksum := crc32.ChecksumIEEE(data)

	// Write checksum
	if err := binary.Write(w.writer, binary.BigEndian, checksum); err != nil {
		return fmt.Errorf("failed to write checksum: %w", err)
	}

	// Write length
	length := uint32(len(data))
	if err := binary.Write(w.writer, binary.BigEndian, length); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}

	// Write data
	if _, err := w.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	// Flush to OS
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush WAL: %w", err)
	}

	// Sync to disk if enabled
	if w.sync {
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("failed to sync WAL: %w", err)
		}
	}

	// Update last index/term
	if entry.Index > w.lastIndex {
		w.lastIndex = entry.Index
		w.lastTerm = entry.Term
	}

	return nil
}

// AppendBatch appends multiple entries atomically.
func (w *WAL) AppendBatch(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Write all entries
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal entry: %w", err)
		}

		checksum := crc32.ChecksumIEEE(data)
		length := uint32(len(data))

		if err := binary.Write(w.writer, binary.BigEndian, checksum); err != nil {
			return err
		}
		if err := binary.Write(w.writer, binary.BigEndian, length); err != nil {
			return err
		}
		if _, err := w.writer.Write(data); err != nil {
			return err
		}
	}

	// Flush and sync once for the batch
	if err := w.writer.Flush(); err != nil {
		return err
	}

	if w.sync {
		if err := w.file.Sync(); err != nil {
			return err
		}
	}

	// Update last index/term
	for _, entry := range entries {
		if entry.Index > w.lastIndex {
			w.lastIndex = entry.Index
			w.lastTerm = entry.Term
		}
	}

	return nil
}

// ReadEntries reads entries from the WAL starting at the given index.
func (w *WAL) ReadEntries(startIndex uint64) ([]Entry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// For simplicity, we'll read from the beginning and filter
	// In production, maintain an index for efficient seeking
	entries := make([]Entry, 0)

	// Seek past header
	if _, err := w.file.Seek(8, 0); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(w.file)
	for {
		entry, err := w.readEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if entry.Index >= startIndex {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// Truncate truncates the WAL, removing entries before the given index.
func (w *WAL) Truncate(beforeIndex uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Read all entries we want to keep (inline because we already hold lock)
	entries := make([]Entry, 0)

	// Seek past header
	if _, err := w.file.Seek(8, 0); err != nil {
		return err
	}

	reader := bufio.NewReader(w.file)
	for {
		entry, err := w.readEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if entry.Index >= beforeIndex {
			entries = append(entries, entry)
		}
	}

	// Close current file
	w.writer.Flush()
	w.file.Close()

	// Create new file
	tmpPath := w.path + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp WAL: %w", err)
	}

	// Write header
	writer := bufio.NewWriter(tmpFile)
	header := WALHeader{Magic: walMagic, Version: walVersion}
	if err := binary.Write(writer, binary.BigEndian, header); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}

	// Write kept entries
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		checksum := crc32.ChecksumIEEE(data)
		length := uint32(len(data))

		binary.Write(writer, binary.BigEndian, checksum)
		binary.Write(writer, binary.BigEndian, length)
		writer.Write(data)
	}

	writer.Flush()
	tmpFile.Close()

	// Replace old file with new
	if err := os.Rename(tmpPath, w.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to replace WAL: %w", err)
	}

	// Reopen
	return w.open()
}

// LastIndex returns the last log index in the WAL.
func (w *WAL) LastIndex() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastIndex
}

// LastTerm returns the last log term in the WAL.
func (w *WAL) LastTerm() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastTerm
}

// Close closes the WAL.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		w.writer.Flush()
	}
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// logf logs a message (for debugging).
func (w *WAL) logf(format string, args ...interface{}) {
	// In production, use proper logging
	// For now, silently ignore
	_ = format
	_ = args
}

// FileSize returns the current file size.
func (w *WAL) FileSize() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	stat, err := w.file.Stat()
	if err != nil {
		return 0
	}
	return stat.Size()
}
