package auth

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestIsNTLMSSPSignature(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid", []byte("NTLMSSP\x00"), true},
		{"short", []byte("NTLMSSP"), false},
		{"wrong", []byte("NTLMSSQ\x00"), false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNTLMSSPSignature(tt.data); got != tt.want {
				t.Errorf("isNTLMSSPSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeNTLMString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		unicode bool
	}{
		{"ascii unicode", "HELLO", true},
		{"ascii oem", "HELLO", false},
		{"mixed unicode", "Test123", true},
		{"empty unicode", "", true},
		{"empty oem", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeNTLMString(tt.input, tt.unicode)
			decoded := decodeNTLMString(encoded, tt.unicode)
			if decoded != tt.input {
				t.Errorf("roundtrip failed: got %q, want %q", decoded, tt.input)
			}
		})
	}
}

func TestMD4Hash(t *testing.T) {
	// Known MD4 test vectors from RFC 1320
	tests := []struct {
		input string
		want  string
	}{
		{"", "31d6cfe0d16ae931b73c59d7e0c089c0"},
		{"a", "bde52cb31de33e46245e05fbdbd6fb24"},
		{"abc", "a448017aaf21d8525fc10ae87aa6729d"},
		{"message digest", "d9130a8164549fe818874806e1c7014b"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := md4Hash([]byte(tt.input))
			if hex.EncodeToString(got) != tt.want {
				t.Errorf("md4Hash(%q) = %s, want %s", tt.input, hex.EncodeToString(got), tt.want)
			}
		})
	}
}

func TestComputeNTHash(t *testing.T) {
	// Known NT hash for "password" - widely documented
	hash := ComputeNTHash("password")
	expected := "8846f7eaee8fb117ad06bdd830b7586c"
	if hex.EncodeToString(hash) != expected {
		t.Errorf("ComputeNTHash(\"password\") = %s, want %s", hex.EncodeToString(hash), expected)
	}

	// Empty password
	emptyHash := ComputeNTHash("")
	expectedEmpty := "31d6cfe0d16ae931b73c59d7e0c089c0"
	if hex.EncodeToString(emptyHash) != expectedEmpty {
		t.Errorf("ComputeNTHash(\"\") = %s, want %s", hex.EncodeToString(emptyHash), expectedEmpty)
	}
}

func TestParseNTLMNegotiate(t *testing.T) {
	// Build a minimal valid Type 1 message
	buf := make([]byte, 32)
	copy(buf[0:8], ntlmssPSignature)
	binary.LittleEndian.PutUint32(buf[8:12], ntlmTypeNegotiate)
	binary.LittleEndian.PutUint32(buf[12:16], ntlmFlagUnicode|ntlmFlagNTLM)

	neg, err := ParseNTLMNegotiate(buf)
	if err != nil {
		t.Fatalf("ParseNTLMNegotiate() error: %v", err)
	}
	if neg.Flags&ntlmFlagUnicode == 0 {
		t.Error("expected Unicode flag set")
	}

	// Too short
	_, err = ParseNTLMNegotiate([]byte{0x00, 0x01})
	if err == nil {
		t.Error("expected error for short message")
	}

	// Wrong type
	badType := make([]byte, 16)
	copy(badType[0:8], ntlmssPSignature)
	binary.LittleEndian.PutUint32(badType[8:12], ntlmTypeChallenge)
	_, err = ParseNTLMNegotiate(badType)
	if err == nil {
		t.Error("expected error for wrong type")
	}

	// Bad signature
	badSig := make([]byte, 16)
	copy(badSig[0:8], []byte("BADSIG\x00\x00"))
	binary.LittleEndian.PutUint32(badSig[8:12], ntlmTypeNegotiate)
	_, err = ParseNTLMNegotiate(badSig)
	if err == nil {
		t.Error("expected error for bad signature")
	}
}

func TestParseNTLMNegotiateWithDomain(t *testing.T) {
	// Build Type 1 with domain name
	domain := encodeNTLMString("DOMAIN", true)
	buf := make([]byte, 32+len(domain))
	copy(buf[0:8], ntlmssPSignature)
	binary.LittleEndian.PutUint32(buf[8:12], ntlmTypeNegotiate)
	binary.LittleEndian.PutUint32(buf[12:16], ntlmFlagUnicode|ntlmFlagNTLM)

	// Domain security buffer
	domLen := uint16(len(domain))
	domOffset := uint32(32)
	binary.LittleEndian.PutUint16(buf[16:18], domLen)
	binary.LittleEndian.PutUint16(buf[18:20], domLen)
	binary.LittleEndian.PutUint32(buf[20:24], domOffset)
	copy(buf[domOffset:], domain)

	neg, err := ParseNTLMNegotiate(buf)
	if err != nil {
		t.Fatalf("ParseNTLMNegotiate() error: %v", err)
	}
	if neg.Domain != "DOMAIN" {
		t.Errorf("Domain = %q, want %q", neg.Domain, "DOMAIN")
	}
}

func TestGenerateAndParseChallenge(t *testing.T) {
	challenge, err := GenerateChallenge("MSSQL")
	if err != nil {
		t.Fatalf("GenerateChallenge() error: %v", err)
	}

	// Parse it back
	c, err := ParseNTLMChallenge(challenge)
	if err != nil {
		t.Fatalf("ParseNTLMChallenge() error: %v", err)
	}

	if c.TargetName != "MSSQL" {
		t.Errorf("TargetName = %q, want %q", c.TargetName, "MSSQL")
	}
	if c.Flags&ntlmFlagUnicode == 0 {
		t.Error("expected Unicode flag in challenge")
	}
	if c.Flags&ntlmFlagNTLM == 0 {
		t.Error("expected NTLM flag in challenge")
	}
	if len(c.TargetInfo) == 0 {
		t.Error("expected TargetInfo to be populated")
	}
}

func TestNTLMv2ResponseVerification(t *testing.T) {
	// End-to-end NTLMv2 verification test
	password := "SecREt01"
	user := "user"
	domain := "DOMAIN"

	// Compute NT hash
	ntHash := ComputeNTHash(password)

	// Generate server challenge
	challengeMsg, err := GenerateChallenge(domain)
	if err != nil {
		t.Fatalf("GenerateChallenge() error: %v", err)
	}
	chal, err := ParseNTLMChallenge(challengeMsg)
	if err != nil {
		t.Fatalf("ParseNTLMChallenge() error: %v", err)
	}

	// Derive ResponseKeyNT
	responseKeyNT := hmacMD5(ntHash, encodeNTLMString(
		toUpper(user)+toUpper(domain), true,
	))

	// Build a minimal client blob (just the timestamp and AvEOL)
	clientBlob := buildMinimalClientBlob()

	// Compute NTProofStr
	ntProofStr := hmacMD5(responseKeyNT, append(chal.Challenge[:], clientBlob...))

	// Build NTLM response = NTProofStr + clientBlob
	ntlmResponse := append(ntProofStr, clientBlob...)

	auth := &NTLMAuthenticate{
		NTLMResponse: ntlmResponse,
		Domain:       domain,
		User:         user,
		Flags:        ntlmFlagUnicode | ntlmFlagNTLM,
	}

	if !VerifyNTLMResponse(auth, chal.Challenge, ntHash) {
		t.Error("VerifyNTLMResponse() = false, want true")
	}

	// Test with wrong password
	wrongHash := ComputeNTHash("wrong")
	if VerifyNTLMResponse(auth, chal.Challenge, wrongHash) {
		t.Error("VerifyNTLMResponse() should fail with wrong password")
	}
}

func TestVerifyNTLMResponse_TooShort(t *testing.T) {
	auth := &NTLMAuthenticate{
		NTLMResponse: []byte{0x01, 0x02, 0x03},
	}
	var chal [8]byte
	hash := ComputeNTHash("test")

	if VerifyNTLMResponse(auth, chal, hash) {
		t.Error("expected verification to fail with short response")
	}
}

func TestParseNTLMAuthenticate(t *testing.T) {
	// Build a minimal Type 3 message
	domain := encodeNTLMString("DOMAIN", true)
	user := encodeNTLMString("user", true)
	ws := encodeNTLMString("WORKSTATION", true)
	lmResp := make([]byte, 24)
	ntlmResp := make([]byte, 24)

	// Calculate offsets: header (64) + lmResp (24) + ntlmResp (24) + domain + user + ws
	offset := uint32(64)

	buf := make([]byte, 64+len(lmResp)+len(ntlmResp)+len(domain)+len(user)+len(ws))
	copy(buf[0:8], ntlmssPSignature)
	binary.LittleEndian.PutUint32(buf[8:12], ntlmTypeAuthenticate)

	// LM Response security buffer at offset 12
	putSecBuf(buf[12:20], uint16(len(lmResp)), offset)
	copy(buf[offset:], lmResp)
	offset += uint32(len(lmResp))

	// NTLM Response security buffer at offset 20
	putSecBuf(buf[20:28], uint16(len(ntlmResp)), offset)
	copy(buf[offset:], ntlmResp)
	offset += uint32(len(ntlmResp))

	// Domain security buffer at offset 28
	putSecBuf(buf[28:36], uint16(len(domain)), offset)
	copy(buf[offset:], domain)
	offset += uint32(len(domain))

	// User security buffer at offset 36
	putSecBuf(buf[36:44], uint16(len(user)), offset)
	copy(buf[offset:], user)
	offset += uint32(len(user))

	// Workstation security buffer at offset 44
	putSecBuf(buf[44:52], uint16(len(ws)), offset)
	copy(buf[offset:], ws)

	// Flags
	binary.LittleEndian.PutUint32(buf[60:64], ntlmFlagUnicode|ntlmFlagNTLM)

	auth, err := ParseNTLMAuthenticate(buf)
	if err != nil {
		t.Fatalf("ParseNTLMAuthenticate() error: %v", err)
	}

	if auth.Domain != "DOMAIN" {
		t.Errorf("Domain = %q, want %q", auth.Domain, "DOMAIN")
	}
	if auth.User != "user" {
		t.Errorf("User = %q, want %q", auth.User, "user")
	}
	if auth.Workstation != "WORKSTATION" {
		t.Errorf("Workstation = %q, want %q", auth.Workstation, "WORKSTATION")
	}
	if len(auth.LMResponse) != 24 {
		t.Errorf("LMResponse length = %d, want 24", len(auth.LMResponse))
	}
	if len(auth.NTLMResponse) != 24 {
		t.Errorf("NTLMResponse length = %d, want 24", len(auth.NTLMResponse))
	}
}

func TestParseNTLMAuthenticate_Errors(t *testing.T) {
	// Too short
	_, err := ParseNTLMAuthenticate(make([]byte, 10))
	if err == nil {
		t.Error("expected error for short message")
	}

	// Bad signature
	bad := make([]byte, 64)
	copy(bad[0:8], []byte("BAD\x00\x00\x00\x00\x00"))
	binary.LittleEndian.PutUint32(bad[8:12], ntlmTypeAuthenticate)
	_, err = ParseNTLMAuthenticate(bad)
	if err == nil {
		t.Error("expected error for bad signature")
	}

	// Wrong type
	wrong := make([]byte, 64)
	copy(wrong[0:8], ntlmssPSignature)
	binary.LittleEndian.PutUint32(wrong[8:12], ntlmTypeNegotiate)
	_, err = ParseNTLMAuthenticate(wrong)
	if err == nil {
		t.Error("expected error for wrong type")
	}
}

func TestParseNTLMChallenge_Errors(t *testing.T) {
	// Too short
	_, err := ParseNTLMChallenge(make([]byte, 10))
	if err == nil {
		t.Error("expected error for short message")
	}

	// Bad signature
	bad := make([]byte, 48)
	copy(bad[0:8], []byte("BAD\x00\x00\x00\x00\x00"))
	binary.LittleEndian.PutUint32(bad[8:12], ntlmTypeChallenge)
	_, err = ParseNTLMChallenge(bad)
	if err == nil {
		t.Error("expected error for bad signature")
	}
}

func TestHMACMD5(t *testing.T) {
	// Known HMAC-MD5 test vector from RFC 2104
	key := []byte{0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b}
	data := []byte("Hi There")
	expected := "9294727a3638bb1c13f48ef8158bfc9d"
	got := hmacMD5(key, data)
	if hex.EncodeToString(got) != expected {
		t.Errorf("hmacMD5() = %s, want %s", hex.EncodeToString(got), expected)
	}
}

func TestPutSecBuf(t *testing.T) {
	buf := make([]byte, 8)
	putSecBuf(buf, 100, 200)
	if binary.LittleEndian.Uint16(buf[0:2]) != 100 {
		t.Error("length mismatch")
	}
	if binary.LittleEndian.Uint16(buf[2:4]) != 100 {
		t.Error("allocated mismatch")
	}
	if binary.LittleEndian.Uint32(buf[4:8]) != 200 {
		t.Error("offset mismatch")
	}
}

// Helpers

func toUpper(s string) string {
	result := make([]rune, len(s))
	for i, r := range s {
		if r >= 'a' && r <= 'z' {
			result[i] = r - 32
		} else {
			result[i] = r
		}
	}
	return string(result)
}

func buildMinimalClientBlob() []byte {
	// Minimal NTLMv2 client blob with just AvEOL
	blob := []byte{
		// BlobSignature (4 bytes)
		0x01, 0x01, 0x00, 0x00,
		// Reserved (4 bytes)
		0x00, 0x00, 0x00, 0x00,
		// Timestamp (8 bytes) - Jan 1, 2020 00:00:00 in 100ns intervals since Jan 1, 1601
		0x00, 0x80, 0x3d, 0xd5, 0xde, 0xb0, 0x1d, 0x01,
		// ClientChallenge (8 bytes)
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		// Reserved (4 bytes)
		0x00, 0x00, 0x00, 0x00,
		// AvEOL
		0x00, 0x00, 0x00, 0x00,
	}
	return blob
}
