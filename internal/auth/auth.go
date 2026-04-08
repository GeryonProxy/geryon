package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/GeryonProxy/geryon/internal/config"
)

// User represents an authenticated user.
type User struct {
	Username       string
	PasswordHash   string // SCRAM-SHA-256 format
	MaxConnections int
	DefaultPool    string
	AllowedPools   []string
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
			Username:       u.Username,
			PasswordHash:   u.PasswordHash,
			MaxConnections: u.MaxConnections,
			DefaultPool:    u.DefaultPool,
			AllowedPools:   u.AllowedPools,
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
		iterations: 4096,
		users:      users,
	}
}

// SCRAMState holds the authentication state.
type SCRAMState struct {
	Username       string
	Nonce          string
	ClientFirst    string
	ServerFirst    string
	AuthMessage    string
	StoredKey      []byte
	ServerKey      []byte
	Iterations     int
	Salt           []byte
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

	iterations := 4096

	// SaltedPassword = PBKDF2(HMAC-SHA-256, password, salt, iterations, 32)
	saltedPassword := pbkdf2([]byte(password), salt, iterations, 32)

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
	// Format: SCRAM-SHA-256$<iterations>:<salt>:<storedkey>:<serverkey>
	if !strings.HasPrefix(hash, "SCRAM-SHA-256$") {
		return nil, nil, nil, 0, fmt.Errorf("unsupported hash format")
	}

	data := hash[len("SCRAM-SHA-256$"):]
	parts := strings.Split(data, ":")
	if len(parts) != 4 {
		return nil, nil, nil, 0, fmt.Errorf("invalid hash format")
	}

	// Parse iterations
	if _, err := fmt.Sscanf(parts[0], "%d", &iterations); err != nil {
		return nil, nil, nil, 0, fmt.Errorf("invalid iterations: %w", err)
	}

	// Decode base64 values
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

func pbkdf2(password, salt []byte, iterations, keyLen int) []byte {
	// Simple PBKDF2 implementation using HMAC-SHA-256
	prf := func(data []byte) []byte {
		return hmacSHA256(password, data)
	}

	blockSize := 32 // SHA-256 block size
	numBlocks := (keyLen + blockSize - 1) / blockSize

	buf := make([]byte, numBlocks*blockSize)
	u := make([]byte, blockSize)
	for block := 1; block <= numBlocks; block++ {
		// U_1 = PRF(salt || INT_32_BE(block))
		saltBlock := append(salt, []byte{
			byte(block >> 24),
			byte(block >> 16),
			byte(block >> 8),
			byte(block),
		}...)
		u = prf(saltBlock)
		copy(buf[(block-1)*blockSize:], u)

		// U_i = PRF(U_{i-1})
		for i := 2; i <= iterations; i++ {
			u = prf(u)
			for j := range u {
				buf[(block-1)*blockSize+j] ^= u[j]
			}
		}
	}

	return buf[:keyLen]
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
