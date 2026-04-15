package mysql

import (
	"bytes"
	"testing"
)

// FuzzCodec tests the MySQL codec with random inputs.
func FuzzCodec(f *testing.F) {
	// Seed with valid and invalid packets
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x03}) // COM_QUERY empty
	f.Add([]byte{0x01, 0x00, 0x00, 0x01, 0x03, 0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x31}) // SELECT 1
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x00}) // OK packet
	f.Add([]byte{0x00})                           // Incomplete header
	f.Add([]byte{0xff})                            // Error packet start
	f.Add([]byte{})                                // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		// If successful, verify packet structure
		if err == nil && msg != nil {
			// For MySQL, packet length should be consistent
			if len(msg.Payload) > 0 && len(msg.Payload) > 16*1024*1024 {
				t.Error("MySQL packet payload exceeds 16MB limit")
			}
		}

		// Empty should always fail
		if len(data) == 0 {
			_, err := c.ReadMessage(bytes.NewReader([]byte{}))
			if err == nil {
				t.Error("Empty input should return error")
			}
		}
	})
}

// FuzzHandshakeResponse tests handshake response parsing.
func FuzzHandshakeResponse(f *testing.F) {
	f.Add([]byte{0x85, 0xa2, 0x1e, 0x00, 0x00, 0x00, 0x00, 0x41, 0x00, 0x00}) // Partial handshake
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})                                      // Minimal
	f.Add([]byte{})                                                               // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()

		// Test basic ReadMessage
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		if err == nil && msg != nil {
			// Verify message has reasonable size
			if len(msg.Payload) > 16*1024*1024 {
				t.Error("Payload exceeds MySQL max")
			}
		}
	})
}

// FuzzCOMQuery tests COM_QUERY packet parsing.
func FuzzCOMQuery(f *testing.F) {
	// Valid COM_QUERY (0x03) with "SELECT 1"
	f.Add([]byte{0x0b, 0x00, 0x00, 0x00, 0x03, 0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x31, 0x00})
	// Valid COM_QUIT (0x01)
	f.Add([]byte{0x01, 0x00, 0x00, 0x00, 0x01})
	// Incomplete
	f.Add([]byte{0x03, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		if err == nil && msg != nil {
			// MySQL OK packet starts with 0x00
			if len(msg.Payload) > 0 && msg.Payload[0] == 0x00 {
				// OK packet - valid
			}
		}
	})
}
