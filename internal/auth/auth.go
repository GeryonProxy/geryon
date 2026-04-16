// Package auth implements user authentication for the Geryon proxy.
// It provides SCRAM-SHA-256 password hashing, user database management,
// credential verification, and mTLS certificate-based auth with CN/SAN
// to username mapping.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/GeryonProxy/geryon/internal/config"
)

// User represents an authenticated user.
type User struct {
	Username          string
	PasswordHash      string // SCRAM-SHA-256 format (PostgreSQL)
	MysqlPasswordHash string // SHA256(SHA256(password)) for MySQL caching_sha2_password
	MaxConnections    int
	DefaultPool       string
	AllowedPools      []string
}

// CanAccessPool checks if the user can access the given pool.
func (u *User) CanAccessPool(poolName string) bool {
	for _, allowed := range u.AllowedPools {
		if allowed == "*" || allowed == poolName {
			return true
		}
	}
	return false
}

// UserDatabase stores user credentials and permissions.
type UserDatabase struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewUserDatabase creates a new user database.
func NewUserDatabase() *UserDatabase {
	return &UserDatabase{
		users: make(map[string]*User),
	}
}

// LoadFromConfig loads users from configuration.
func (db *UserDatabase) LoadFromConfig(cfg *config.AuthConfig) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	// Clear existing users
	db.users = make(map[string]*User)

	for _, u := range cfg.Users {
		user := &User{
			Username:          u.Username,
			PasswordHash:      u.PasswordHash,
			MysqlPasswordHash: u.MysqlPasswordHash,
			MaxConnections:    u.MaxConnections,
			DefaultPool:       u.DefaultPool,
			AllowedPools:      u.AllowedPools,
		}
		db.users[u.Username] = user
	}

	return nil
}

// GetUser returns a user by username.
func (db *UserDatabase) GetUser(username string) *User {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.users[username]
}

// AddUser adds a user to the database.
func (db *UserDatabase) AddUser(user *User) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.users[user.Username]; exists {
		return fmt.Errorf("user %s already exists", user.Username)
	}

	db.users[user.Username] = user
	return nil
}

// RemoveUser removes a user from the database.
func (db *UserDatabase) RemoveUser(username string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, exists := db.users[username]; !exists {
		return fmt.Errorf("user %s not found", username)
	}

	delete(db.users, username)
	return nil
}

// ListUsers returns all users.
func (db *UserDatabase) ListUsers() []*User {
	db.mu.RLock()
	defer db.mu.RUnlock()

	users := make([]*User, 0, len(db.users))
	for _, u := range db.users {
		users = append(users, u)
	}
	return users
}

// SCRAMServer implements SCRAM-SHA-256 server-side authentication.
type SCRAMServer struct {
	iterations int
	users      *UserDatabase
}

// NewSCRAMServer creates a new SCRAM server.
func NewSCRAMServer(users *UserDatabase) *SCRAMServer {
	return &SCRAMServer{
		iterations: 120000, // OWASP 2023+ recommendation
		users:      users,
	}
}

// SCRAMState holds the authentication state.
type SCRAMState struct {
	Username    string
	Nonce       string
	ClientFirst string
	ServerFirst string
	AuthMessage string
	StoredKey   []byte
	ServerKey   []byte
	Iterations  int
	Salt        []byte
}

// ParseClientFirst parses the client-first-message.
func (s *SCRAMServer) ParseClientFirst(msg string) (*SCRAMState, error) {
	// client-first-message = gs2-header client-first-data-bare
	// gs2-header = "n," [gs2-cbind-flag] ","
	// client-first-data-bare = [reserved-mext ","] username "," nonce

	// Remove GS2 header
	parts := strings.SplitN(msg, ",", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid client-first-message format")
	}

	// Parse gs2-header
	gs2 := parts[0] + "," + parts[1]
	if !strings.HasPrefix(gs2, "n,") {
		return nil, fmt.Errorf("unsupported GS2 mechanism")
	}

	// Parse client-first-data-bare
	data := parts[2]
	dataParts := strings.Split(data, ",")

	state := &SCRAMState{
		ClientFirst: msg,
	}

	for _, part := range dataParts {
		if strings.HasPrefix(part, "n=") {
			state.Username = part[2:]
		} else if strings.HasPrefix(part, "r=") {
			state.Nonce = part[2:]
		}
	}

	if state.Username == "" {
		return nil, fmt.Errorf("username not provided")
	}
	if state.Nonce == "" {
		return nil, fmt.Errorf("nonce not provided")
	}

	return state, nil
}

// GenerateServerFirst generates the server-first-message.
func (s *SCRAMServer) GenerateServerFirst(state *SCRAMState) (string, error) {
	// Get user from database
	user := s.users.GetUser(state.Username)
	if user == nil {
		return "", fmt.Errorf("user not found")
	}

	// Parse stored password hash
	// Format: SCRAM-SHA-256$<iterations>:<salt>:<storedkey>:<serverkey>
	storedKey, serverKey, salt, iterations, err := parseSCRAMHash(user.PasswordHash)
	if err != nil {
		return "", fmt.Errorf("invalid password hash: %w", err)
	}

	state.StoredKey = storedKey
	state.ServerKey = serverKey
	state.Salt = salt
	state.Iterations = iterations

	// Generate server nonce (append to client nonce)
	serverNonce := make([]byte, 24)
	if _, err := rand.Read(serverNonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	state.Nonce = state.Nonce + base64.StdEncoding.EncodeToString(serverNonce)

	// server-first-message = [reserved-mext ","] nonce "," salt "," iteration-count ["," extensions]
	state.ServerFirst = fmt.Sprintf("r=%s,s=%s,i=%d",
		state.Nonce,
		base64.StdEncoding.EncodeToString(salt),
		iterations)

	// Build AuthMessage
	clientFirstBare := extractBare(state.ClientFirst)
	state.AuthMessage = clientFirstBare + "," + state.ServerFirst

	return state.ServerFirst, nil
}

// VerifyClientFinal verifies the client-final-message.
func (s *SCRAMServer) VerifyClientFinal(state *SCRAMState, msg string) (bool, error) {
	// client-final-message = client-final-message-without-proof "," proof
	// client-final-message-without-proof = channel-binding "," nonce ["," extensions]
	// proof = "p=" base64

	parts := strings.Split(msg, ",")
	var proof string
	var clientFinalWithoutProof []string

	for _, part := range parts {
		if strings.HasPrefix(part, "p=") {
			proof = part[2:]
		} else {
			clientFinalWithoutProof = append(clientFinalWithoutProof, part)
		}
	}

	if proof == "" {
		return false, fmt.Errorf("proof not provided")
	}

	// Update AuthMessage
	state.AuthMessage = state.AuthMessage + "," + strings.Join(clientFinalWithoutProof, ",")

	// Decode proof
	clientProof, err := base64.StdEncoding.DecodeString(proof)
	if err != nil {
		return false, fmt.Errorf("invalid proof encoding: %w", err)
	}

	// Calculate ClientSignature = HMAC(StoredKey, AuthMessage)
	clientSig := hmacSHA256(state.StoredKey, []byte(state.AuthMessage))

	// Calculate ClientKey = ClientProof XOR ClientSignature
	clientKey := xor(clientProof, clientSig)

	// Verify: H(ClientKey) == StoredKey
	h := sha256.Sum256(clientKey)
	if !hmac.Equal(h[:], state.StoredKey) {
		return false, fmt.Errorf("invalid password")
	}

	return true, nil
}

// GenerateServerFinal generates the server-final-message.
func (s *SCRAMServer) GenerateServerFinal(state *SCRAMState) string {
	// ServerSignature = HMAC(ServerKey, AuthMessage)
	serverSig := hmacSHA256(state.ServerKey, []byte(state.AuthMessage))
	return "v=" + base64.StdEncoding.EncodeToString(serverSig)
}

// GenerateSCRAMHash generates a SCRAM-SHA-256 hash from a password.
func GenerateSCRAMHash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	iterations := 120000 // OWASP 2023+ recommendation

	// SaltedPassword = PBKDF2(HMAC-SHA-256, password, salt, iterations, 32)
	saltedPassword := pbkdf2Key([]byte(password), salt, iterations, 32, sha256.New)

	// ClientKey = HMAC(SaltedPassword, "Client Key")
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))

	// StoredKey = SHA256(ClientKey)
	storedKey := sha256.Sum256(clientKey)

	// ServerKey = HMAC(SaltedPassword, "Server Key")
	serverKey := hmacSHA256(saltedPassword, []byte("Server Key"))

	// Format: SCRAM-SHA-256$<iterations>:<salt>:<storedkey>:<serverkey>
	hash := fmt.Sprintf("SCRAM-SHA-256$%d:%s:%s:%s",
		iterations,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(storedKey[:]),
		base64.StdEncoding.EncodeToString(serverKey))

	return hash, nil
}

// Helper functions
func parseSCRAMHash(hash string) (storedKey, serverKey, salt []byte, iterations int, err error) {
	// Format: SCRAM-SHA-256$<iterations>:<salt>$<storedkey>:<serverkey>
	// (also supports legacy format with : instead of second $)
	if !strings.HasPrefix(hash, "SCRAM-SHA-256$") {
		return nil, nil, nil, 0, fmt.Errorf("unsupported hash format")
	}

	data := hash[len("SCRAM-SHA-256$"):]

	// Split into three parts: "iter:salt", "storedkey", "serverkey"
	// Use $ as delimiter between major sections
	parts := strings.Split(data, "$")
	if len(parts) != 2 {
		// Fall back to legacy format: all colon-separated
		parts = strings.Split(data, ":")
		if len(parts) != 4 {
			return nil, nil, nil, 0, fmt.Errorf("invalid hash format")
		}
		if _, err := fmt.Sscanf(parts[0], "%d", &iterations); err != nil {
			return nil, nil, nil, 0, fmt.Errorf("invalid iterations: %w", err)
		}
		salt, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("invalid salt: %w", err)
		}
		storedKey, err = base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("invalid stored key: %w", err)
		}
		serverKey, err = base64.StdEncoding.DecodeString(parts[3])
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("invalid server key: %w", err)
		}
		return storedKey, serverKey, salt, iterations, nil
	}

	// New format: "iter:salt$storedkey:serverkey"
	// Parse iterations and salt from first part
	iterSaltParts := strings.Split(parts[0], ":")
	if len(iterSaltParts) != 2 {
		return nil, nil, nil, 0, fmt.Errorf("invalid iterations/salt format")
	}
	if _, err := fmt.Sscanf(iterSaltParts[0], "%d", &iterations); err != nil {
		return nil, nil, nil, 0, fmt.Errorf("invalid iterations: %w", err)
	}
	salt, err = base64.StdEncoding.DecodeString(iterSaltParts[1])
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("invalid salt: %w", err)
	}

	// Parse stored key and server key from second part
	keyParts := strings.Split(parts[1], ":")
	if len(keyParts) != 2 {
		return nil, nil, nil, 0, fmt.Errorf("invalid keys format")
	}
	storedKey, err = base64.StdEncoding.DecodeString(keyParts[0])
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("invalid stored key: %w", err)
	}
	serverKey, err = base64.StdEncoding.DecodeString(keyParts[1])
	if err != nil {
		return nil, nil, nil, 0, fmt.Errorf("invalid server key: %w", err)
	}

	return storedKey, serverKey, salt, iterations, nil
}

func extractBare(clientFirst string) string {
	// Extract client-first-data-bare from gs2-header
	// "n,," -> remove gs2-header prefix
	if strings.HasPrefix(clientFirst, "n,,") {
		return clientFirst[3:]
	}
	// Try finding after second comma
	parts := strings.SplitN(clientFirst, ",", 3)
	if len(parts) >= 3 {
		return parts[2]
	}
	return clientFirst
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func xor(a, b []byte) []byte {
	if len(a) != len(b) {
		// Return shorter length
		n := len(a)
		if len(b) < n {
			n = len(b)
		}
		result := make([]byte, n)
		for i := 0; i < n; i++ {
			result[i] = a[i] ^ b[i]
		}
		return result
	}
	result := make([]byte, len(a))
	for i := range a {
		result[i] = a[i] ^ b[i]
	}
	return result
}

// AuthMode represents authentication modes.
type AuthMode int

const (
	ModePassthrough AuthMode = iota
	ModeInterception
)

// ParseAuthMode parses an auth mode string.
func ParseAuthMode(s string) (AuthMode, error) {
	switch s {
	case "passthrough":
		return ModePassthrough, nil
	case "interception":
		return ModeInterception, nil
	default:
		return ModePassthrough, fmt.Errorf("invalid auth mode: %s", s)
	}
}

// AuthLimiter tracks failed authentication attempts per source IP
// and enforces temporary lockouts to prevent brute-force attacks.
type AuthLimiter struct {
	mu            sync.Mutex
	attempts      map[string]*ipAuthAttempts
	maxAttempts   int
	window        time.Duration
	lockoutPeriod time.Duration
}

type ipAuthAttempts struct {
	mu        sync.Mutex
	count     int
	firstFail time.Time
	locked    bool
	lockUntil time.Time
}

// NewAuthLimiter creates a rate limiter with defaults:
// 10 failed attempts per 5-minute window, 5-minute lockout.
func NewAuthLimiter() *AuthLimiter {
	return &AuthLimiter{
		attempts:      make(map[string]*ipAuthAttempts),
		maxAttempts:   10,
		window:        5 * time.Minute,
		lockoutPeriod: 5 * time.Minute,
	}
}

// NewAuthLimiterConfig creates a rate limiter with custom limits.
func NewAuthLimiterConfig(maxAttempts int, window, lockoutPeriod time.Duration) *AuthLimiter {
	return &AuthLimiter{
		attempts:      make(map[string]*ipAuthAttempts),
		maxAttempts:   maxAttempts,
		window:        window,
		lockoutPeriod: lockoutPeriod,
	}
}

// RecordFailure records a failed authentication attempt for the given IP.
// Returns true if the IP is now locked out.
func (al *AuthLimiter) RecordFailure(ip string) bool {
	al.mu.Lock()
	entry, exists := al.attempts[ip]
	if !exists {
		entry = &ipAuthAttempts{
			firstFail: time.Now(),
		}
		al.attempts[ip] = entry
	}
	al.mu.Unlock()

	// Use per-entry lock for atomic access to entry fields
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()

	// Check if lockout has expired
	if entry.locked && now.After(entry.lockUntil) {
		// Reset after lockout expires
		entry.count = 1
		entry.firstFail = now
		entry.locked = false
		return false
	}

	// Already locked
	if entry.locked {
		return true
	}

	// Clean up old entries outside the window
	if now.Sub(entry.firstFail) > al.window {
		entry.count = 1
		entry.firstFail = now
		return false
	}

	entry.count++
	if entry.count >= al.maxAttempts {
		entry.locked = true
		entry.lockUntil = now.Add(al.lockoutPeriod)
		return true
	}

	return false
}

// RecordSuccess resets the failure counter for an IP after successful auth.
func (al *AuthLimiter) RecordSuccess(ip string) {
	al.mu.Lock()
	defer al.mu.Unlock()
	delete(al.attempts, ip)
}

// IsLimited returns true if the IP is currently locked out.
func (al *AuthLimiter) IsLimited(ip string) bool {
	al.mu.Lock()
	entry, exists := al.attempts[ip]
	if !exists {
		al.mu.Unlock()
		return false
	}
	al.mu.Unlock()

	// Use per-entry lock for consistent read
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()

	// Clean up expired entries
	if now.Sub(entry.firstFail) > al.window {
		al.mu.Lock()
		delete(al.attempts, ip)
		al.mu.Unlock()
		return false
	}

	if entry.locked && now.After(entry.lockUntil) {
		// Lockout expired, clean up
		al.mu.Lock()
		delete(al.attempts, ip)
		al.mu.Unlock()
		return false
	}

	return entry.locked
}

// VerifyMySQLPassword verifies a MySQL password against stored hash using challenge-response.
// Returns nil on success, error on failure.
// Supports caching_sha2_password (SHA256-based) and mysql_native_password (SHA1-based).
func VerifyMySQLPassword(storedHash string, scramble []byte, clientResponse []byte) error {
	if storedHash == "" {
		return fmt.Errorf("no MySQL password hash stored for user")
	}

	// Try to decode if in base64
	hashBytes, err := base64.StdEncoding.DecodeString(storedHash)
	if err != nil {
		hashBytes = []byte(storedHash)
	}

	// Detect auth method based on stored hash length
	// caching_sha2_password: SHA256(SHA256(password)) = 32 bytes
	// mysql_native_password: SHA1(SHA1(password)) = 20 bytes
	if len(hashBytes) == 32 {
		return verifyCachingSHA2Password(hashBytes, scramble, clientResponse)
	} else if len(hashBytes) == 20 {
		return verifyNativePassword(hashBytes, scramble, clientResponse)
	}

	return fmt.Errorf("unknown MySQL password hash format (length: %d)", len(hashBytes))
}

// verifyCachingSHA2Password verifies caching_sha2_password authentication.
// Stored hash: SHA256(SHA256(password)) = 32 bytes raw
// Client response: SHA256(password) XOR SHA256(storedHash + scramble)
//
// Verification: SHA256(clientResponse XOR SHA256(storedHash + scramble) + scramble) == storedHash
//
// This works because:
// clientResponse = SHA256(password) XOR SHA256(storedHash + scramble)
// clientResponse XOR SHA256(storedHash + scramble) = SHA256(password)
//
// Then we compute SHA256(SHA256(password) + scramble) and compare with storedHash
func verifyCachingSHA2Password(storedHash []byte, scramble, clientResponse []byte) error {
	if len(storedHash) != 32 {
		return fmt.Errorf("invalid caching_sha2_password hash length: %d", len(storedHash))
	}

	if len(clientResponse) != 32 {
		return fmt.Errorf("invalid client response length: %d", len(clientResponse))
	}

	if len(scramble) < 20 {
		return fmt.Errorf("scramble too short: %d", len(scramble))
	}

	// Compute SHA256(storedHash + scramble[:20])
	h := sha256.New()
	h.Write(storedHash)
	h.Write(scramble[:20])
	serverPart := h.Sum(nil)

	// XOR: clientResponse XOR serverPart = SHA256(password)
	passwordHash := xorBytes(clientResponse, serverPart)

	// Compute SHA256(SHA256(password) + scramble[:20])
	h.Reset()
	h.Write(passwordHash)
	h.Write(scramble[:20])
	resultHash := h.Sum(nil)

	// Compare result with storedHash
	if subtle.ConstantTimeCompare(resultHash, storedHash) != 1 {
		return fmt.Errorf("password verification failed")
	}

	return nil
}

// verifyNativePassword verifies mysql_native_password authentication.
// Stored hash: SHA1(SHA1(password)) = 20 bytes raw
// Client response: SHA1(password) XOR SHA1(scramble + storedHash)
func verifyNativePassword(storedHash []byte, scramble, clientResponse []byte) error {
	if len(storedHash) != 20 {
		return fmt.Errorf("invalid mysql_native_password hash length: %d", len(storedHash))
	}

	if len(clientResponse) != 20 {
		return fmt.Errorf("invalid client response length: %d", len(clientResponse))
	}

	if len(scramble) < 8 {
		return fmt.Errorf("scramble too short: %d", len(scramble))
	}

	// SHA1(scramble + storedHash)
	h := sha1.New()
	h.Write(scramble[:8])
	h.Write(storedHash)
	serverPart := h.Sum(nil)

	// XOR client response with server computed to get SHA1(password)
	result := xorBytes(clientResponse, serverPart)

	// Compute SHA1(result) = SHA1(SHA1(password)) = storedHash
	// But we verify by computing SHA1(result) and comparing to storedHash
	h.Reset()
	h.Write(result)
	expectedStored := h.Sum(nil)

	if subtle.ConstantTimeCompare(expectedStored, storedHash) != 1 {
		return fmt.Errorf("password verification failed")
	}

	return nil
}

// xorBytes performs XOR on two byte slices of equal length.
func xorBytes(a, b []byte) []byte {
	if len(a) != len(b) {
		return nil
	}
	result := make([]byte, len(a))
	for i := range result {
		result[i] = a[i] ^ b[i]
	}
	return result
}
