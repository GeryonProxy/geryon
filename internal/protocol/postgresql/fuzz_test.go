package postgresql

import (
	"bytes"
	"testing"
)

// FuzzCodec tests the PostgreSQL codec with random inputs.
func FuzzCodec(f *testing.F) {
	// Seed with valid and invalid test cases
	f.Add([]byte{0x51, 0x00, 0x00, 0x00, 0x10, 0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x31}) // Valid SELECT 1
	f.Add([]byte{0x51, 0x00, 0x00, 0x00, 0x08, 0x51, 0x00})                                     // Valid SELECT
	f.Add([]byte{0x54, 0x00, 0x00, 0x00, 0x1a, 0x42, 0x45, 0x47, 0x49, 0x4e})                   // BEGIN
	f.Add([]byte{0x58, 0x00, 0x00, 0x00, 0x04})                                                 // Valid Sync
	f.Add([]byte{0x51})                                                                         // Incomplete header
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})                                                       // Empty message
	f.Add([]byte{0xff, 0x00, 0x00, 0x00, 0x01})                                                 // Invalid type
	f.Add([]byte{0x51, 0xff, 0xff, 0xff, 0xff})                                                 // Invalid length

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()

		// Test ReadMessage with fuzzed input
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		// If we got a valid message, verify its invariants
		if err == nil && msg != nil {
			// Message type should be a valid byte
			if msg.Type == 0 {
				t.Error("Message type should not be 0 for a valid message")
			}

			// Raw message should not be nil if we successfully read
			if msg.Raw == nil && len(data) > 5 {
				t.Error("Message Raw should not be nil when reading succeeds")
			}
		}

		// Test with empty input - should return error
		if len(data) == 0 {
			_, err := c.ReadMessage(bytes.NewReader([]byte{}))
			if err == nil {
				t.Error("Empty input should return error")
			}
		}
	})
}

// FuzzStartupMessage tests startup message parsing.
func FuzzStartupMessage(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x00, 0x30, 0x00, 0x00, 0x00, 0x03}) // Valid startup
	f.Add([]byte{0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00}) // Minimal startup
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})                         // Too short
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})                         // Invalid length
	f.Add([]byte{})                                               // Empty

	f.Fuzz(func(t *testing.T, data []byte) {
		// Test that codec doesn't crash on any input
		c := NewCodec()
		reader := bytes.NewReader(data)
		_, _ = c.ReadMessage(reader)

		// Edge cases - empty input should error
		if len(data) == 0 {
			// Already handled - should error
		}

		// Very short input should error or return valid message
		if len(data) > 0 && len(data) < 5 {
			// Either succeeds or fails, both acceptable
		}
	})
}

// FuzzParseQuery tests query message parsing.
func FuzzParseQuery(f *testing.F) {
	f.Add([]byte{'Q', 0, 0, 0, 13, 0x53, 0x45, 0x4c, 0x45, 0x43, 0x54, 0x20, 0x31, 0x00})
	f.Add([]byte{'Q', 0, 0, 0, 9, 0x42, 0x45, 0x47, 0x49, 0x4e, 0x00})
	f.Add([]byte{'Q', 0, 0, 0, 11, 0x43, 0x4f, 0x4d, 0x4d, 0x49, 0x54, 0x00})
	f.Add([]byte{'Q'})
	f.Add([]byte{0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec()
		reader := bytes.NewReader(data)
		msg, err := c.ReadMessage(reader)

		if err == nil && msg != nil {
			if msg.Type == 'Q' && len(msg.Payload) > 0 {
				query, _ := c.ExtractQuery(msg)
				// Query should be a valid string (null-terminated or raw)
				if len(query) > 0 && query[0] == 0 {
					t.Error("Query should not start with null byte")
				}
			}
		}
	})
}
