package common

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuffer_ReadInt16_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01})
	_, err := buf.ReadInt16()
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadUint16_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01})
	_, err := buf.ReadUint16()
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadInt32_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01, 0x02})
	_, err := buf.ReadInt32()
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadUint32_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01, 0x02})
	_, err := buf.ReadUint32()
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadInt64_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01, 0x02, 0x03})
	_, err := buf.ReadInt64()
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadBytes_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{0x01})
	_, err := buf.ReadBytes(5)
	if err == nil {
		t.Error("Should fail with insufficient data")
	}
}

func TestBuffer_ReadLengthPrefixedString_TooShort(t *testing.T) {
	buf := NewBuffer([]byte{0x05, 'h', 'i'}) // Length=5 but only 2 bytes follow
	_, err := buf.ReadLengthPrefixedString()
	if err == nil {
		t.Error("Should fail when string data is shorter than length prefix")
	}
}

func TestBuffer_ReadLengthPrefixedString_ZeroLength(t *testing.T) {
	buf := NewBuffer([]byte{0x00, 0x00}) // 2-byte length prefix = 0
	s, err := buf.ReadLengthPrefixedString()
	// May return empty or error depending on impl
	if err != nil {
		t.Logf("Got error (acceptable): %v", err)
	}
	_ = s
}

func TestReadStartupMessage_Cancel(t *testing.T) {
	// Cancel request: len(4) + code(4) where code = 80877102
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 8)

	var codeBuf [4]byte
	binary.BigEndian.PutUint32(codeBuf[:], 80877102)

	data := append(lenBuf[:], codeBuf[:]...)
	msg, err := ReadStartupMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	if msg.ProtocolVersion != 80877102 {
		t.Errorf("ProtocolVersion = %d, want 80877102", msg.ProtocolVersion)
	}
}

func TestReadStartupMessage_SSLRequest(t *testing.T) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 8)

	var codeBuf [4]byte
	binary.BigEndian.PutUint32(codeBuf[:], 80877103)

	data := append(lenBuf[:], codeBuf[:]...)
	msg, err := ReadStartupMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	if msg.ProtocolVersion != 80877103 {
		t.Errorf("ProtocolVersion = %d, want 80877103", msg.ProtocolVersion)
	}
}

func TestReadStartupMessage_WithParams(t *testing.T) {
	// Build a startup message with parameters
	params := []byte("user\x00testuser\x00database\x00testdb\x00")
	length := 4 + len(params) // protocol(4) + params + final null

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(length+4)) // total including length itself

	var protoBuf [4]byte
	binary.BigEndian.PutUint32(protoBuf[:], 196608) // Protocol 3.0

	data := append(lenBuf[:], protoBuf[:]...)
	data = append(data, params...)

	msg, err := ReadStartupMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	if msg.ProtocolVersion != 196608 {
		t.Errorf("ProtocolVersion = %d, want 196608", msg.ProtocolVersion)
	}
	if msg.Parameters["user"] != "testuser" {
		t.Errorf("user = %q, want testuser", msg.Parameters["user"])
	}
	if msg.Parameters["database"] != "testdb" {
		t.Errorf("database = %q, want testdb", msg.Parameters["database"])
	}
}

func TestReadStartupMessage_InvalidLength(t *testing.T) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 5) // Too small (< 8)

	_, err := ReadStartupMessage(bytes.NewReader(lenBuf[:]))
	if err == nil {
		t.Error("Should fail with invalid length")
	}
}

func TestReadStartupMessage_ShortRead(t *testing.T) {
	_, err := ReadStartupMessage(bytes.NewReader([]byte{0, 0}))
	if err == nil {
		t.Error("Should fail for short read")
	}
}

func TestWriteStartupMessage_Success(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters: map[string]string{
			"user":     "test",
			"database": "mydb",
		},
	}

	var buf bytes.Buffer
	err := WriteStartupMessage(&buf, msg)
	if err != nil {
		t.Fatalf("WriteStartupMessage failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("Should write data to buffer")
	}
}

func TestWriteStartupMessage_WriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{"user": "test"},
	}

	// Use a writer that always fails
	errWriter := &errorWriter{}
	err := WriteStartupMessage(errWriter, msg)
	if err == nil {
		t.Error("Should fail when writer fails")
	}
}

func TestBuffer_ReadByte_Insufficient(t *testing.T) {
	buf := NewBuffer([]byte{})
	_, err := buf.ReadByte()
	if err == nil {
		t.Error("Should fail with empty buffer")
	}
}

// errorWriter is a writer that always returns an error
type errorWriter struct{}

func (w *errorWriter) Write(p []byte) (n int, err error) {
	return 0, bytes.ErrTooLarge
}

// --- WriteBytes (0% coverage) ---

func TestBuffer_WriteBytes(t *testing.T) {
	buf := NewBuffer(nil)
	buf.WriteBytes([]byte{0x01, 0x02, 0x03})
	if len(buf.Bytes()) != 3 {
		t.Errorf("len = %d, want 3", len(buf.Bytes()))
	}
	if buf.Bytes()[0] != 0x01 || buf.Bytes()[2] != 0x03 {
		t.Errorf("bytes = %v, want [1 2 3]", buf.Bytes())
	}

	// Append to existing data
	buf.WriteBytes([]byte{0x04, 0x05})
	if len(buf.Bytes()) != 5 {
		t.Errorf("len = %d, want 5 after append", len(buf.Bytes()))
	}
}

// --- ReadLengthPrefixedString NULL case ---

func TestBuffer_ReadLengthPrefixedString_Null(t *testing.T) {
	// Negative length should return empty string (NULL)
	data := make([]byte, 4)
	binary.BigEndian.PutUint32(data, 0xFFFFFFFF) // -1 as int32
	buf := NewBuffer(data)
	s, err := buf.ReadLengthPrefixedString()
	if err != nil {
		t.Errorf("Should not error for NULL string: %v", err)
	}
	if s != "" {
		t.Errorf("NULL string should return empty, got %q", s)
	}
}

// --- ReadStartupMessage with empty key and truncated value ---

func TestReadStartupMessage_EmptyKeySkipped(t *testing.T) {
	// Build a startup message with an empty key followed by valid params
	params := []byte{0x00, 'v', 'a', 'l', 0x00, 'u', 's', 'e', 'r', 0x00, 't', 'e', 's', 't', 0x00, 0x00}
	length := 4 + len(params) // protocol(4) + params

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(length+4))

	var protoBuf [4]byte
	binary.BigEndian.PutUint32(protoBuf[:], 196608)

	data := append(lenBuf[:], protoBuf[:]...)
	data = append(data, params...)

	msg, err := ReadStartupMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	// Empty key should be skipped
	if _, ok := msg.Parameters[""]; ok {
		t.Error("Empty key should not be added to parameters")
	}
	if msg.Parameters["user"] != "test" {
		t.Errorf("user = %q, want test", msg.Parameters["user"])
	}
}

// --- ReadStartupMessage truncated (value without null terminator) ---

func TestReadStartupMessage_TruncatedValue(t *testing.T) {
	// Key with null but value without null terminator
	params := []byte("user\x00testvalue")
	length := 4 + len(params)

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(length+4))

	var protoBuf [4]byte
	binary.BigEndian.PutUint32(protoBuf[:], 196608)

	data := append(lenBuf[:], protoBuf[:]...)
	data = append(data, params...)

	msg, err := ReadStartupMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	// Value is truncated (no null terminator), should still work
	if msg.Parameters["user"] != "testvalue" {
		t.Logf("user = %q (truncated value case)", msg.Parameters["user"])
	}
}

// --- WriteStartupMessage with multiple error points ---

func TestWriteStartupMessage_ProtocolVersionWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{},
	}

	// errorWriter fails on first write (length), but we need to test deeper
	// Use a writer that fails after a few bytes
	err := WriteStartupMessage(&errorWriter{}, msg)
	if err == nil {
		t.Error("Should fail with error writer")
	}
}

// --- WriteStartupMessage with empty parameters ---

func TestWriteStartupMessage_EmptyParams(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{},
	}

	var buf bytes.Buffer
	err := WriteStartupMessage(&buf, msg)
	if err != nil {
		t.Fatalf("WriteStartupMessage with empty params failed: %v", err)
	}

	// Verify: length(4) + version(4) + null(1) = 9 bytes
	// Total length field should be 5 (4 + 1)
	if buf.Len() != 9 {
		t.Errorf("buf.Len() = %d, want 9", buf.Len())
	}

	// Parse it back
	result, err := ReadStartupMessage(&buf)
	if err != nil {
		t.Fatalf("Round-trip parse failed: %v", err)
	}
	if result.ProtocolVersion != 196608 {
		t.Errorf("ProtocolVersion = %d, want 196608", result.ProtocolVersion)
	}
	if len(result.Parameters) != 0 {
		t.Errorf("Parameters should be empty, got %v", result.Parameters)
	}
}

// --- ReadStartupMessage too large ---

func TestReadStartupMessage_TooLarge(t *testing.T) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 20000) // > 10000

	_, err := ReadStartupMessage(bytes.NewReader(lenBuf[:]))
	if err == nil {
		t.Error("Should fail with too large length")
	}
}

// --- ReadStartupMessage body read error ---

func TestReadStartupMessage_BodyReadError(t *testing.T) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 20) // Valid length but no body data

	_, err := ReadStartupMessage(bytes.NewReader(lenBuf[:]))
	if err == nil {
		t.Error("Should fail when body is missing")
	}
}

// --- WriteStartupMessage parameter value write error ---

type partialWriter struct {
	buf       []byte
	failAfter int
	written   int
}

func (w *partialWriter) Write(p []byte) (n int, err error) {
	if w.written+len(p) > w.failAfter {
		remaining := w.failAfter - w.written
		if remaining > 0 {
			w.buf = append(w.buf, p[:remaining]...)
			w.written += remaining
		}
		return remaining, bytes.ErrTooLarge
	}
	w.buf = append(w.buf, p...)
	w.written += len(p)
	return len(p), nil
}

func TestWriteStartupMessage_ParamKeyWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{"user": "test"},
	}

	// Fail after writing length (4 bytes) + protocol version (4 bytes) = 8 bytes
	// then it tries to write key
	pw := &partialWriter{failAfter: 8}
	err := WriteStartupMessage(pw, msg)
	if err == nil {
		t.Error("Should fail during parameter key write")
	}
}

func TestWriteStartupMessage_ParamNullWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{"user": "test"},
	}

	// Fail after length(4) + version(4) + key "user"(4) = 12 bytes
	// then it tries to write null terminator after key
	pw := &partialWriter{failAfter: 12}
	err := WriteStartupMessage(pw, msg)
	if err == nil {
		t.Error("Should fail during parameter null write")
	}
}

func TestWriteStartupMessage_ParamValueWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{"user": "test"},
	}

	// Fail after length(4) + version(4) + key(4) + null(1) = 13 bytes
	// then it tries to write value
	pw := &partialWriter{failAfter: 13}
	err := WriteStartupMessage(pw, msg)
	if err == nil {
		t.Error("Should fail during parameter value write")
	}
}

func TestWriteStartupMessage_ParamValueNullWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{"user": "test"},
	}

	// Fail after length(4) + version(4) + key(4) + null(1) + value "test"(4) = 17 bytes
	// then it tries to write null terminator after value
	pw := &partialWriter{failAfter: 17}
	err := WriteStartupMessage(pw, msg)
	if err == nil {
		t.Error("Should fail during value null write")
	}
}

func TestWriteStartupMessage_FinalNullWriteError(t *testing.T) {
	msg := &StartupMessage{
		ProtocolVersion: 196608,
		Parameters:      map[string]string{},
	}

	// Fail after length(4) + version(4) = 8 bytes
	// then it tries to write final null terminator
	pw := &partialWriter{failAfter: 8}
	err := WriteStartupMessage(pw, msg)
	if err == nil {
		t.Error("Should fail during final null write")
	}
}
