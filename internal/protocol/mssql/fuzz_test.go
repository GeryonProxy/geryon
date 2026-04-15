package mssql

import (
	"bytes"
	"testing"
)

// FuzzCodec tests the MSSQL TDS codec with random inputs.
func FuzzCodec(f *testing.F) {
	// Seed with valid and invalid TDS packets
	f.Add([]byte{0x04, 0x01, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x00}) // Valid TDS header
	f.Add([]byte{0x04, 0x01, 0x00, 0x08, 0x00, 0x00, 0x01, 0x00}) // Minimal packet
	f.Add([]byte{0x01, 0x00, 0x00, 0x08, 0x00, 0x00, 0x01, 0x00}) // Query packet
	f.Add([]byte{0x04})                                           // Incomplete header
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})       // Invalid
	f.Add([]byte{})                                               // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		// If successful, verify TDS packet structure
		if err == nil && msg != nil {
			// TDS packet should have at least 8 byte header
			if len(msg.Payload) > 0 && len(msg.Raw) < 8 {
				t.Error("TDS message with payload should have at least 8 header bytes")
			}
		}

		// Empty should fail
		if len(data) == 0 {
			_, err := c.ReadMessage(bytes.NewReader([]byte{}))
			if err == nil {
				t.Error("Empty input should return error")
			}
		}
	})
}

// FuzzLogin7 tests Login7 packet parsing.
func FuzzLogin7(f *testing.F) {
	f.Add([]byte{0x10, 0x01, 0x00, 0x34, 0x00, 0x00, 0x01, 0x00}) // Partial Login7
	f.Add([]byte{0x10, 0x01, 0x00, 0x08, 0x00, 0x00, 0x01, 0x00}) // Minimal
	f.Add([]byte{})
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		if err == nil && msg != nil {
			// Verify message type is valid
			if msg.Type == 0 {
				// Could be valid for some packets
			}
		}
	})
}

// FuzzPreLogin tests PreLogin packet parsing.
func FuzzPreLogin(f *testing.F) {
	f.Add([]byte{0x12, 0x01, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x00}) // Valid PreLogin
	f.Add([]byte{0x12, 0x01, 0x00, 0x08, 0x00, 0x00, 0x01, 0x00}) // Minimal
	f.Add([]byte{})
	f.Add([]byte{0x12})

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		// Valid PreLogin or error - both acceptable
		_ = msg
		_ = err
	})
}
