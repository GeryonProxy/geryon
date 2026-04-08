package common

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Direction indicates message flow direction.
type Direction uint8

const (
	Frontend Direction = iota // Client → Proxy
	Backend                   // Proxy → Server
)

// Protocol identifies the database protocol.
type Protocol uint8

const (
	ProtocolPostgreSQL Protocol = iota
	ProtocolMySQL
	ProtocolMSSQL
)

// Message represents a database wire protocol message.
type Message struct {
	Type      byte      // Protocol-specific message type byte
	Length    int32     // Total message length (including self)
	Payload   []byte    // Raw message payload
	Raw       []byte    // Complete raw message (header + payload)
	Direction Direction // Frontend or Backend
}

// Codec is the interface each protocol body must implement.
type Codec interface {
	// ReadMessage reads one complete message from the connection.
	ReadMessage(r io.Reader) (*Message, error)

	// WriteMessage writes one complete message to the connection.
	WriteMessage(w io.Writer, msg *Message) error

	// IsStartup returns true if this is a startup/handshake message.
	IsStartup(msg *Message) bool

	// IsTerminate returns true if this is a termination message.
	IsTerminate(msg *Message) bool

	// IsQuery returns true if this is a query message.
	IsQuery(msg *Message) bool

	// IsTransactionBegin returns true if message starts a transaction.
	IsTransactionBegin(msg *Message) bool

	// IsTransactionEnd returns true if message ends a transaction.
	IsTransactionEnd(msg *Message) bool

	// IsPrepare returns true if this is a prepare statement message.
	IsPrepare(msg *Message) bool

	// IsExecute returns true if this is an execute prepared stmt message.
	IsExecute(msg *Message) bool

	// ExtractQuery extracts the SQL query string from a query message.
	ExtractQuery(msg *Message) (string, error)

	// GenerateResetSequence returns messages to reset server state.
	GenerateResetSequence() []*Message

	// Protocol returns the protocol identifier.
	Protocol() Protocol
}

// Buffer provides read/write helpers for protocol messages.
type Buffer struct {
	buf []byte
	pos int
}

// NewBuffer creates a new buffer with the given data.
func NewBuffer(data []byte) *Buffer {
	return &Buffer{buf: data, pos: 0}
}

// Reset resets the buffer position.
func (b *Buffer) Reset() {
	b.pos = 0
}

// Bytes returns the underlying buffer.
func (b *Buffer) Bytes() []byte {
	return b.buf
}

// Pos returns the current position.
func (b *Buffer) Pos() int {
	return b.pos
}

// Len returns the buffer length.
func (b *Buffer) Len() int {
	return len(b.buf)
}

// Remaining returns the number of bytes remaining.
func (b *Buffer) Remaining() int {
	return len(b.buf) - b.pos
}

// ReadByte reads a single byte.
func (b *Buffer) ReadByte() (byte, error) {
	if b.pos >= len(b.buf) {
		return 0, io.EOF
	}
	v := b.buf[b.pos]
	b.pos++
	return v, nil
}

// ReadInt16 reads a 16-bit integer (big-endian).
func (b *Buffer) ReadInt16() (int16, error) {
	if b.pos+2 > len(b.buf) {
		return 0, io.EOF
	}
	v := int16(binary.BigEndian.Uint16(b.buf[b.pos:]))
	b.pos += 2
	return v, nil
}

// ReadUint16 reads a 16-bit unsigned integer (big-endian).
func (b *Buffer) ReadUint16() (uint16, error) {
	if b.pos+2 > len(b.buf) {
		return 0, io.EOF
	}
	v := binary.BigEndian.Uint16(b.buf[b.pos:])
	b.pos += 2
	return v, nil
}

// ReadInt32 reads a 32-bit integer (big-endian).
func (b *Buffer) ReadInt32() (int32, error) {
	if b.pos+4 > len(b.buf) {
		return 0, io.EOF
	}
	v := int32(binary.BigEndian.Uint32(b.buf[b.pos:]))
	b.pos += 4
	return v, nil
}

// ReadUint32 reads a 32-bit unsigned integer (big-endian).
func (b *Buffer) ReadUint32() (uint32, error) {
	if b.pos+4 > len(b.buf) {
		return 0, io.EOF
	}
	v := binary.BigEndian.Uint32(b.buf[b.pos:])
	b.pos += 4
	return v, nil
}

// ReadInt64 reads a 64-bit integer (big-endian).
func (b *Buffer) ReadInt64() (int64, error) {
	if b.pos+8 > len(b.buf) {
		return 0, io.EOF
	}
	v := int64(binary.BigEndian.Uint64(b.buf[b.pos:]))
	b.pos += 8
	return v, nil
}

// ReadBytes reads n bytes.
func (b *Buffer) ReadBytes(n int) ([]byte, error) {
	if b.pos+n > len(b.buf) {
		return nil, io.EOF
	}
	v := b.buf[b.pos : b.pos+n]
	b.pos += n
	return v, nil
}

// ReadString reads a null-terminated string.
func (b *Buffer) ReadString() (string, error) {
	start := b.pos
	for b.pos < len(b.buf) {
		if b.buf[b.pos] == 0 {
			v := string(b.buf[start:b.pos])
			b.pos++ // skip null terminator
			return v, nil
		}
		b.pos++
	}
	return "", io.EOF
}

// ReadLengthPrefixedString reads a string prefixed with its length (int32).
func (b *Buffer) ReadLengthPrefixedString() (string, error) {
	length, err := b.ReadInt32()
	if err != nil {
		return "", err
	}
	if length < 0 {
		return "", nil // NULL string
	}
	bytes, err := b.ReadBytes(int(length))
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// WriteByte writes a single byte.
func (b *Buffer) WriteByte(v byte) error {
	b.buf = append(b.buf, v)
	return nil
}

// WriteInt16 writes a 16-bit integer (big-endian).
func (b *Buffer) WriteInt16(v int16) {
	b.buf = binary.BigEndian.AppendUint16(b.buf, uint16(v))
}

// WriteUint16 writes a 16-bit unsigned integer (big-endian).
func (b *Buffer) WriteUint16(v uint16) {
	b.buf = binary.BigEndian.AppendUint16(b.buf, v)
}

// WriteInt32 writes a 32-bit integer (big-endian).
func (b *Buffer) WriteInt32(v int32) {
	b.buf = binary.BigEndian.AppendUint32(b.buf, uint32(v))
}

// WriteUint32 writes a 32-bit unsigned integer (big-endian).
func (b *Buffer) WriteUint32(v uint32) {
	b.buf = binary.BigEndian.AppendUint32(b.buf, v)
}

// WriteInt64 writes a 64-bit integer (big-endian).
func (b *Buffer) WriteInt64(v int64) {
	b.buf = binary.BigEndian.AppendUint64(b.buf, uint64(v))
}

// WriteBytes writes bytes.
func (b *Buffer) WriteBytes(v []byte) {
	b.buf = append(b.buf, v...)
}

// WriteString writes a null-terminated string.
func (b *Buffer) WriteString(s string) error {
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
	return nil
}

// WriteLengthPrefixedString writes a string prefixed with its length (int32).
func (b *Buffer) WriteLengthPrefixedString(s string) {
	b.WriteInt32(int32(len(s)))
	b.buf = append(b.buf, s...)
}

// ReadFull reads exactly len(buf) bytes from r into buf.
func ReadFull(r io.Reader, buf []byte) error {
	_, err := io.ReadFull(r, buf)
	return err
}

// ReadMessageHeader reads a 5-byte message header (type + length).
func ReadMessageHeader(r io.Reader) (msgType byte, length int32, err error) {
	header := make([]byte, 5)
	if err = ReadFull(r, header); err != nil {
		return 0, 0, err
	}
	return header[0], int32(binary.BigEndian.Uint32(header[1:5])), nil
}

// WriteMessageHeader writes a 5-byte message header (type + length).
func WriteMessageHeader(w io.Writer, msgType byte, length int32) error {
	buf := make([]byte, 5)
	buf[0] = msgType
	binary.BigEndian.PutUint32(buf[1:5], uint32(length))
	_, err := w.Write(buf)
	return err
}

// StartupMessage represents a protocol startup message.
type StartupMessage struct {
	ProtocolVersion uint32
	Parameters      map[string]string
}

// ReadStartupMessage reads a startup message from the connection.
// Used by PostgreSQL and similar protocols.
func ReadStartupMessage(r io.Reader) (*StartupMessage, error) {
	// Read length (4 bytes)
	lenBuf := make([]byte, 4)
	if err := ReadFull(r, lenBuf); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(lenBuf))

	if length < 8 || length > 10000 {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// Read the rest of the message
	buf := make([]byte, length-4)
	if err := ReadFull(r, buf); err != nil {
		return nil, err
	}

	msg := &StartupMessage{
		ProtocolVersion: binary.BigEndian.Uint32(buf[0:4]),
		Parameters:      make(map[string]string),
	}

	// Parse null-terminated key-value pairs
	pos := 4
	for pos < len(buf)-1 {
		// Find null terminator for key
		keyStart := pos
		for pos < len(buf) && buf[pos] != 0 {
			pos++
		}
		if pos >= len(buf) {
			break
		}
		key := string(buf[keyStart:pos])
		pos++ // skip null

		// Find null terminator for value
		valStart := pos
		for pos < len(buf) && buf[pos] != 0 {
			pos++
		}
		if pos >= len(buf) {
			break
		}
		val := string(buf[valStart:pos])
		pos++ // skip null

		if key != "" {
			msg.Parameters[key] = val
		}
	}

	return msg, nil
}

// WriteStartupMessage writes a startup message to the connection.
func WriteStartupMessage(w io.Writer, msg *StartupMessage) error {
	// Calculate total length
	length := 4 // protocol version
	for k, v := range msg.Parameters {
		length += len(k) + 1 + len(v) + 1
	}
	length += 1 // final null terminator

	// Write length
	if err := binary.Write(w, binary.BigEndian, int32(length+4)); err != nil {
		return err
	}

	// Write protocol version
	if err := binary.Write(w, binary.BigEndian, msg.ProtocolVersion); err != nil {
		return err
	}

	// Write parameters
	for k, v := range msg.Parameters {
		if _, err := w.Write([]byte(k)); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, byte(0)); err != nil {
			return err
		}
		if _, err := w.Write([]byte(v)); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, byte(0)); err != nil {
			return err
		}
	}

	// Write final null terminator
	return binary.Write(w, binary.BigEndian, byte(0))
}
