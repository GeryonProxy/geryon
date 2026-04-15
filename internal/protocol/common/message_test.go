package common

import (
	"bytes"
	"io"
	"testing"
)

func TestDirection(t *testing.T) {
	if Frontend != 0 {
		t.Errorf("Frontend = %d, want 0", Frontend)
	}
	if Backend != 1 {
		t.Errorf("Backend = %d, want 1", Backend)
	}
}

func TestProtocol(t *testing.T) {
	if ProtocolPostgreSQL != 0 {
		t.Errorf("ProtocolPostgreSQL = %d, want 0", ProtocolPostgreSQL)
	}
	if ProtocolMySQL != 1 {
		t.Errorf("ProtocolMySQL = %d, want 1", ProtocolMySQL)
	}
	if ProtocolMSSQL != 2 {
		t.Errorf("ProtocolMSSQL = %d, want 2", ProtocolMSSQL)
	}
}

func TestMessage(t *testing.T) {
	msg := &Message{
		Type:      'Q',
		Length:    10,
		Payload:   []byte("test"),
		Raw:       []byte{0, 0, 0, 0, 10},
		Direction: Frontend,
	}
	if msg.Type != 'Q' {
		t.Errorf("Type = %c, want Q", msg.Type)
	}
	if msg.Direction != Frontend {
		t.Errorf("Direction = %d, want Frontend", msg.Direction)
	}
}

func TestReadFull(t *testing.T) {
	data := []byte{1, 2, 3, 4}
	buf := make([]byte, 4)
	err := ReadFull(bytes.NewReader(data), buf)
	if err != nil {
		t.Fatalf("ReadFull failed: %v", err)
	}
	if !bytes.Equal(data, buf) {
		t.Errorf("Got %v, want %v", buf, data)
	}

	// Short read
	err = ReadFull(bytes.NewReader([]byte{1}), buf)
	if err == nil {
		t.Error("Should fail on short read")
	}
}

func TestReadMessageHeader(t *testing.T) {
	data := []byte{'Q', 0, 0, 0, 10}
	msgType, length, err := ReadMessageHeader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadMessageHeader failed: %v", err)
	}
	if msgType != 'Q' {
		t.Errorf("Type = %c, want Q", msgType)
	}
	if length != 10 {
		t.Errorf("Length = %d, want 10", length)
	}

	// Short header
	_, _, err = ReadMessageHeader(bytes.NewReader([]byte{'Q'}))
	if err == nil {
		t.Error("Should fail on short header")
	}
}

func TestWriteMessageHeader(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMessageHeader(&buf, 'Z', 42)
	if err != nil {
		t.Fatalf("WriteMessageHeader failed: %v", err)
	}
	data := buf.Bytes()
	if data[0] != 'Z' {
		t.Errorf("Type = %c, want Z", data[0])
	}
	if len(data) != 5 {
		t.Errorf("Length = %d, want 5", len(data))
	}
}

func TestReadStartupMessage(t *testing.T) {
	// Valid startup message: 4 bytes length + 4 bytes proto version + key\0value\0\0
	var buf bytes.Buffer
	// Length: 19 bytes total (4 length + 4 version + "user\0test\0\0" = 15)
	buf.Write([]byte{0, 0, 0, 19})
	buf.Write([]byte{0, 0, 0, 196}) // protocol version 196
	buf.Write([]byte("user\x00test\x00\x00"))

	msg, err := ReadStartupMessage(&buf)
	if err != nil {
		t.Fatalf("ReadStartupMessage failed: %v", err)
	}
	if msg.ProtocolVersion != 196 {
		t.Errorf("ProtocolVersion = %d, want 196", msg.ProtocolVersion)
	}
	if msg.Parameters["user"] != "test" {
		t.Errorf("user param = %q, want %q", msg.Parameters["user"], "test")
	}

	// Too short
	_, err = ReadStartupMessage(bytes.NewReader([]byte{0, 0, 0, 5}))
	if err == nil {
		t.Error("Should fail on too short message")
	}

	// Too large
	_, err = ReadStartupMessage(bytes.NewReader([]byte{0, 1, 0, 0}))
	if err == nil {
		t.Error("Should fail on too large message")
	}
}

func TestWriteStartupMessage(t *testing.T) {
	var buf bytes.Buffer
	msg := &StartupMessage{
		ProtocolVersion: 196,
		Parameters:      map[string]string{"user": "test"},
	}
	err := WriteStartupMessage(&buf, msg)
	if err != nil {
		t.Fatalf("WriteStartupMessage failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("Should have written data")
	}
}

func TestBuffer_Readers(t *testing.T) {
	data := []byte{
		0x01,       // byte
		0x00, 0x02, // int16 = 2
		0x00, 0x03, // uint16 = 3
		0x00, 0x00, 0x00, 0x04, // int32 = 4
		0x00, 0x00, 0x00, 0x05, // uint32 = 5
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, // int64 = 6
	}
	buf := NewBuffer(data)

	b, err := buf.ReadByte()
	if err != nil || b != 0x01 {
		t.Errorf("ReadByte = %d, want 1", b)
	}

	v16, err := buf.ReadInt16()
	if err != nil || v16 != 2 {
		t.Errorf("ReadInt16 = %d, want 2", v16)
	}

	uv16, err := buf.ReadUint16()
	if err != nil || uv16 != 3 {
		t.Errorf("ReadUint16 = %d, want 3", uv16)
	}

	v32, err := buf.ReadInt32()
	if err != nil || v32 != 4 {
		t.Errorf("ReadInt32 = %d, want 4", v32)
	}

	uv32, err := buf.ReadUint32()
	if err != nil || uv32 != 5 {
		t.Errorf("ReadUint32 = %d, want 5", uv32)
	}

	v64, err := buf.ReadInt64()
	if err != nil || v64 != 6 {
		t.Errorf("ReadInt64 = %d, want 6", v64)
	}
}

func TestBuffer_ReadBytes(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	buf := NewBuffer(data)
	b, err := buf.ReadBytes(3)
	if err != nil {
		t.Fatalf("ReadBytes failed: %v", err)
	}
	if len(b) != 3 || b[0] != 1 || b[2] != 3 {
		t.Errorf("ReadBytes = %v, want [1 2 3]", b)
	}
}

func TestBuffer_ReadString(t *testing.T) {
	data := []byte("hello\x00world\x00")
	buf := NewBuffer(data)
	s, err := buf.ReadString()
	if err != nil {
		t.Fatalf("ReadString failed: %v", err)
	}
	if s != "hello" {
		t.Errorf("String = %q, want %q", s, "hello")
	}

	// No null terminator
	buf2 := NewBuffer([]byte("no null"))
	_, err = buf2.ReadString()
	if err != io.EOF {
		t.Errorf("Should return EOF, got %v", err)
	}
}

func TestBuffer_ReadLengthPrefixedString(t *testing.T) {
	// Normal string: length=5 + "hello"
	data := []byte{0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	buf := NewBuffer(data)
	s, err := buf.ReadLengthPrefixedString()
	if err != nil {
		t.Fatalf("ReadLengthPrefixedString failed: %v", err)
	}
	if s != "hello" {
		t.Errorf("String = %q, want %q", s, "hello")
	}

	// NULL string (negative length)
	data2 := []byte{0xff, 0xff, 0xff, 0xff}
	buf2 := NewBuffer(data2)
	s2, err := buf2.ReadLengthPrefixedString()
	if err != nil {
		t.Fatalf("ReadLengthPrefixedString NULL failed: %v", err)
	}
	if s2 != "" {
		t.Errorf("NULL string = %q, want empty", s2)
	}
}

func TestBuffer_Writers(t *testing.T) {
	buf := NewBuffer(nil)
	buf.WriteByte(0x01)
	buf.WriteInt16(2)
	buf.WriteUint16(3)
	buf.WriteInt32(4)
	buf.WriteUint32(5)
	buf.WriteInt64(6)

	data := buf.Bytes()
	if len(data) != 1+2+2+4+4+8 {
		t.Fatalf("Data length = %d, want 21", len(data))
	}
	if data[0] != 0x01 {
		t.Errorf("First byte = %d, want 1", data[0])
	}
}

func TestBuffer_WriteString(t *testing.T) {
	buf := NewBuffer(nil)
	err := buf.WriteString("hello")
	if err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	data := buf.Bytes()
	if len(data) != 6 || data[5] != 0 {
		t.Errorf("WriteString = %v, want [hello, 0]", data)
	}
}

func TestBuffer_WriteLengthPrefixedString(t *testing.T) {
	buf := NewBuffer(nil)
	buf.WriteLengthPrefixedString("hello")
	data := buf.Bytes()
	if len(data) != 9 {
		t.Fatalf("Length = %d, want 9", len(data))
	}
	if data[3] != 5 {
		t.Errorf("Length prefix = %d, want 5", data[3])
	}
	if string(data[4:]) != "hello" {
		t.Errorf("String = %q, want hello", data[4:])
	}
}

func TestBuffer_Reset(t *testing.T) {
	buf := NewBuffer([]byte{1, 2, 3})
	buf.Reset()
	if buf.Pos() != 0 {
		t.Errorf("After Reset pos = %d, want 0", buf.Pos())
	}
	if buf.Len() != 3 {
		t.Errorf("After Reset len = %d, want 3", buf.Len())
	}
}

func TestBuffer_Remaining(t *testing.T) {
	buf := NewBuffer([]byte{1, 2, 3, 4, 5})
	buf.ReadByte()
	buf.ReadByte()
	if buf.Remaining() != 3 {
		t.Errorf("Remaining = %d, want 3", buf.Remaining())
	}
}

func TestBuffer_ReadExhausted(t *testing.T) {
	buf := NewBuffer([]byte{})
	_, err := buf.ReadByte()
	if err != io.EOF {
		t.Errorf("Should return EOF, got %v", err)
	}
}
