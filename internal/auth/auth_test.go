package auth

import (
	"strings"
	"testing"

	"github.com/GeryonProxy/geryon/internal/config"
)

func TestNewUserDatabase(t *testing.T) {
	db := NewUserDatabase()
	if db == nil {
		t.Fatal("NewUserDatabase returned nil")
	}
	if db.users == nil {
		t.Fatal("users map not initialized")
	}
}

func TestUserCanAccessPool(t *testing.T) {
	user := &User{
		Username:     "testuser",
		AllowedPools: []string{"pool1", "pool2"},
	}

	if !user.CanAccessPool("pool1") {
		t.Error("expected access to pool1")
	}
	if !user.CanAccessPool("pool2") {
		t.Error("expected access to pool2")
	}
	if user.CanAccessPool("pool3") {
		t.Error("expected no access to pool3")
	}

	// Test wildcard access
	user.AllowedPools = []string{"*"}
	if !user.CanAccessPool("anypool") {
		t.Error("expected wildcard access to any pool")
	}
}

func TestUserDatabaseAddAndGet(t *testing.T) {
	db := NewUserDatabase()

	user := &User{
		Username:       "testuser",
		PasswordHash:   "SCRAM-SHA-256$4096:c29tZXNhbHQ=:aGFzaA==:c2VydmVy",
		MaxConnections: 100,
	}

	// Add user
	err := db.AddUser(user)
	if err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}

	// Get user
	got := db.GetUser("testuser")
	if got == nil {
		t.Fatal("expected to get user, got nil")
	}
	if got.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", got.Username)
	}
	if got.MaxConnections != 100 {
		t.Errorf("expected max connections 100, got %d", got.MaxConnections)
	}

	// Get non-existent user
	got = db.GetUser("nonexistent")
	if got != nil {
		t.Error("expected nil for non-existent user")
	}
}

func TestUserDatabaseAddDuplicate(t *testing.T) {
	db := NewUserDatabase()

	user := &User{Username: "testuser"}
	db.AddUser(user)

	// Try to add duplicate
	err := db.AddUser(user)
	if err == nil {
		t.Error("expected error for duplicate user")
	}
}

func TestUserDatabaseRemove(t *testing.T) {
	db := NewUserDatabase()

	user := &User{Username: "testuser"}
	db.AddUser(user)

	// Remove user
	err := db.RemoveUser("testuser")
	if err != nil {
		t.Fatalf("RemoveUser failed: %v", err)
	}

	// Verify removal
	if db.GetUser("testuser") != nil {
		t.Error("expected user to be removed")
	}

	// Remove non-existent
	err = db.RemoveUser("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent user")
	}
}

func TestUserDatabaseList(t *testing.T) {
	db := NewUserDatabase()

	db.AddUser(&User{Username: "user1"})
	db.AddUser(&User{Username: "user2"})
	db.AddUser(&User{Username: "user3"})

	users := db.ListUsers()
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}

	// Check all usernames are present
	usernames := make(map[string]bool)
	for _, u := range users {
		usernames[u.Username] = true
	}
	for _, name := range []string{"user1", "user2", "user3"} {
		if !usernames[name] {
			t.Errorf("expected user %s in list", name)
		}
	}
}

func TestUserDatabaseLoadFromConfig(t *testing.T) {
	db := NewUserDatabase()

	cfg := &config.AuthConfig{
		Users: []config.User{
			{
				Username:       "admin",
				PasswordHash:   "SCRAM-SHA-256$4096:salt:hash:server",
				MaxConnections: 100,
				DefaultPool:    "default",
				AllowedPools:   []string{"*"},
			},
			{
				Username:       "readonly",
				PasswordHash:   "SCRAM-SHA-256$4096:salt2:hash2:server2",
				MaxConnections: 10,
				AllowedPools:   []string{"replica"},
			},
		},
	}

	err := db.LoadFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadFromConfig failed: %v", err)
	}

	// Check users loaded
	admin := db.GetUser("admin")
	if admin == nil {
		t.Fatal("expected admin user")
	}
	if admin.MaxConnections != 100 {
		t.Errorf("expected max connections 100, got %d", admin.MaxConnections)
	}
	if admin.DefaultPool != "default" {
		t.Errorf("expected default pool 'default', got %q", admin.DefaultPool)
	}

	readonly := db.GetUser("readonly")
	if readonly == nil {
		t.Fatal("expected readonly user")
	}
	if len(readonly.AllowedPools) != 1 || readonly.AllowedPools[0] != "replica" {
		t.Errorf("expected allowed pools [replica], got %v", readonly.AllowedPools)
	}

	// Test reload clears existing
	cfg.Users = []config.User{{Username: "newuser"}}
	db.LoadFromConfig(cfg)

	if db.GetUser("admin") != nil {
		t.Error("expected admin to be cleared after reload")
	}
	if db.GetUser("newuser") == nil {
		t.Error("expected newuser after reload")
	}
}

func TestGenerateSCRAMHash(t *testing.T) {
	hash, err := GenerateSCRAMHash("testpassword")
	if err != nil {
		t.Fatalf("GenerateSCRAMHash failed: %v", err)
	}

	// Verify format
	if !strings.HasPrefix(hash, "SCRAM-SHA-256$") {
		t.Errorf("expected hash to start with 'SCRAM-SHA-256$', got %q", hash)
	}

	parts := strings.Split(hash[len("SCRAM-SHA-256$"):], ":")
	if len(parts) != 4 {
		t.Errorf("expected 4 parts, got %d", len(parts))
	}

	// Parse iterations
	if parts[0] != "120000" {
		t.Errorf("expected iterations 120000, got %s", parts[0])
	}

	// Verify all parts are non-empty
	for i, part := range parts {
		if part == "" {
			t.Errorf("expected part %d to be non-empty", i)
		}
	}
}

func TestParseSCRAMHash(t *testing.T) {
	// Valid hash
	hash := "SCRAM-SHA-256$4096:c29tZXNhbHQ=:aGFzaA==:c2VydmVy"
	storedKey, serverKey, salt, iterations, err := parseSCRAMHash(hash)

	if err != nil {
		t.Fatalf("parseSCRAMHash failed: %v", err)
	}
	if iterations != 4096 {
		t.Errorf("expected iterations 4096, got %d", iterations)
	}
	if len(salt) == 0 {
		t.Error("expected non-empty salt")
	}
	if len(storedKey) == 0 {
		t.Error("expected non-empty stored key")
	}
	if len(serverKey) == 0 {
		t.Error("expected non-empty server key")
	}

	// Invalid format
	_, _, _, _, err = parseSCRAMHash("invalid")
	if err == nil {
		t.Error("expected error for invalid hash format")
	}

	// Wrong number of parts
	_, _, _, _, err = parseSCRAMHash("SCRAM-SHA-256$4096:salt:hash")
	if err == nil {
		t.Error("expected error for wrong number of parts")
	}

	// Invalid base64
	_, _, _, _, err = parseSCRAMHash("SCRAM-SHA-256$4096:not-valid-base64!:hash:server")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestSCRAMServerParseClientFirst(t *testing.T) {
	db := NewUserDatabase()
	server := NewSCRAMServer(db)

	// Valid client-first
	msg := "n,,n=user,r=clientnonce123"
	state, err := server.ParseClientFirst(msg)
	if err != nil {
		t.Fatalf("ParseClientFirst failed: %v", err)
	}
	if state.Username != "user" {
		t.Errorf("expected username 'user', got %q", state.Username)
	}
	if state.Nonce != "clientnonce123" {
		t.Errorf("expected nonce 'clientnonce123', got %q", state.Nonce)
	}

	// Invalid format - too few parts
	_, err = server.ParseClientFirst("n,nonce")
	if err == nil {
		t.Error("expected error for invalid format")
	}

	// Missing username
	_, err = server.ParseClientFirst("n,,r=nonce")
	if err == nil {
		t.Error("expected error for missing username")
	}

	// Missing nonce
	_, err = server.ParseClientFirst("n,,n=user")
	if err == nil {
		t.Error("expected error for missing nonce")
	}

	// Unsupported GS2 mechanism
	_, err = server.ParseClientFirst("y,,n=user,r=nonce")
	if err == nil {
		t.Error("expected error for unsupported GS2 mechanism")
	}
}

func TestSCRAMServerGenerateServerFirst(t *testing.T) {
	db := NewUserDatabase()

	// Add user with valid SCRAM hash
	hash, _ := GenerateSCRAMHash("testpassword")
	db.AddUser(&User{
		Username:     "testuser",
		PasswordHash: hash,
	})

	server := NewSCRAMServer(db)

	// Parse client first
	state, _ := server.ParseClientFirst("n,,n=testuser,r=clientnonce")

	// Generate server first
	serverFirst, err := server.GenerateServerFirst(state)
	if err != nil {
		t.Fatalf("GenerateServerFirst failed: %v", err)
	}

	// Verify format
	if !strings.HasPrefix(serverFirst, "r=") {
		t.Errorf("expected server-first to start with 'r=', got %q", serverFirst)
	}
	if !strings.Contains(serverFirst, "s=") {
		t.Error("expected server-first to contain salt 's='")
	}
	if !strings.Contains(serverFirst, "i=") {
		t.Error("expected server-first to contain iterations 'i='")
	}

	// Verify nonce was extended
	if !strings.HasPrefix(state.Nonce, "clientnonce") {
		t.Error("expected nonce to start with client nonce")
	}
	if len(state.Nonce) <= len("clientnonce") {
		t.Error("expected nonce to be extended with server nonce")
	}

	// Non-existent user
	badState, _ := server.ParseClientFirst("n,,n=nouser,r=nonce")
	_, err = server.GenerateServerFirst(badState)
	if err == nil {
		t.Error("expected error for non-existent user")
	}
}

func TestHMACSHA256(t *testing.T) {
	key := []byte("key")
	data := []byte("data")

	result := hmacSHA256(key, data)
	if len(result) != 32 { // SHA-256 output size
		t.Errorf("expected 32 bytes, got %d", len(result))
	}

	// Same input should produce same output
	result2 := hmacSHA256(key, data)
	for i := range result {
		if result[i] != result2[i] {
			t.Error("expected same output for same input")
			break
		}
	}
}

func TestXOR(t *testing.T) {
	a := []byte{0x01, 0x02, 0x03}
	b := []byte{0xFF, 0xFE, 0xFD}

	result := xor(a, b)
	expected := []byte{0xFE, 0xFC, 0xFE}

	if len(result) != len(expected) {
		t.Errorf("expected %d bytes, got %d", len(expected), len(result))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("expected %x at %d, got %x", expected[i], i, result[i])
		}
	}

	// Different lengths
	c := []byte{0x01, 0x02}
	d := []byte{0xFF, 0xFE, 0xFD}
	result = xor(c, d)
	if len(result) != 2 { // should be min length
		t.Errorf("expected 2 bytes for different lengths, got %d", len(result))
	}
}

func TestExtractBare(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"n,,n=user,r=nonce", "n=user,r=nonce"},
		{"n,channel-binding,n=user,r=nonce", "n=user,r=nonce"},
		{"no-comma-here", "no-comma-here"},
	}

	for _, tt := range tests {
		got := extractBare(tt.input)
		if got != tt.expected {
			t.Errorf("extractBare(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestVerifyNativePassword_InvalidHashLength tests error for invalid hash length
func TestVerifyNativePassword_InvalidHashLength(t *testing.T) {
	storedHash := []byte{0x01, 0x02, 0x03} // too short, need 20 bytes
	scramble := []byte("0123456789ABCDEF0123456789ABCDEF")
	clientResponse := make([]byte, 20)
	
	err := verifyNativePassword(storedHash, scramble, clientResponse)
	if err == nil {
		t.Error("verifyNativePassword() = nil, want error for invalid hash length")
	}
}

// TestVerifyNativePassword_InvalidClientResponseLength tests error for invalid client response length
func TestVerifyNativePassword_InvalidClientResponseLength(t *testing.T) {
	storedHash := make([]byte, 20)
	scramble := []byte("0123456789ABCDEF0123456789ABCDEF")
	clientResponse := []byte{0x01, 0x02} // too short, need 20 bytes
	
	err := verifyNativePassword(storedHash, scramble, clientResponse)
	if err == nil {
		t.Error("verifyNativePassword() = nil, want error for invalid client response length")
	}
}

// TestVerifyNativePassword_ScrambleTooShort tests error for scramble too short
func TestVerifyNativePassword_ScrambleTooShort(t *testing.T) {
	storedHash := make([]byte, 20)
	scramble := []byte("01234567") // less than 8 bytes
	clientResponse := make([]byte, 20)
	
	err := verifyNativePassword(storedHash, scramble, clientResponse)
	if err == nil {
		t.Error("verifyNativePassword() = nil, want error for scramble too short")
	}
}

// TestVerifyNativePassword_WrongPassword tests error for wrong password
func TestVerifyNativePassword_WrongPassword(t *testing.T) {
	storedHash := make([]byte, 20)
	scramble := []byte("0123456789ABCDEF0123456789ABCDEF")
	// Use wrong client response (all zeros instead of proper XOR result)
	clientResponse := make([]byte, 20)
	
	err := verifyNativePassword(storedHash, scramble, clientResponse)
	if err == nil {
		t.Error("verifyNativePassword() = nil, want error for wrong password")
	}
}

// TestXorBytes_Basic tests xorBytes basic functionality
func TestXorBytes_Basic(t *testing.T) {
	a := []byte{0x01, 0x02, 0x03, 0x04}
	b := []byte{0xFF, 0xFE, 0xFD, 0xFC}
	result := xorBytes(a, b)
	expected := []byte{0xFE, 0xFC, 0xFE, 0xF8}
	
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("xorBytes()[%d] = %x, want %x", i, result[i], expected[i])
		}
	}
}

// TestXorBytes_EqualLengths tests xorBytes with equal length slices
func TestXorBytes_EqualLengths(t *testing.T) {
	a := []byte{0xAA, 0xBB, 0xCC}
	b := []byte{0x11, 0x22, 0x33}
	result := xorBytes(a, b)
	
	if len(result) != 3 {
		t.Errorf("xorBytes length = %d, want 3", len(result))
	}
}

// TestXorBytes_DifferentLengths tests xorBytes with different length slices (returns nil)
func TestXorBytes_DifferentLengths(t *testing.T) {
	a := []byte{0x01, 0x02}
	b := []byte{0xFF, 0xFE, 0xFD}
	result := xorBytes(a, b)

	// xorBytes returns nil when lengths differ
	if result != nil {
		t.Errorf("xorBytes() = %v, want nil for different lengths", result)
	}
}

func BenchmarkGenerateSCRAMHash(b *testing.B) {
	for b.Loop() {
		GenerateSCRAMHash("benchmarkpassword")
	}
}

func BenchmarkHMACSHA256(b *testing.B) {
	key := []byte("benchmark-key")
	data := []byte("benchmark-data-for-hmac")
	for b.Loop() {
		hmacSHA256(key, data)
	}
}
