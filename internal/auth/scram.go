package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// GenerateSCRAMSHA256 generates a SCRAM-SHA-256 password hash.
func GenerateSCRAMSHA256(password string) (string, error) {
	// Generate a random salt
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	// Use PBKDF2 with SHA-256, 120000 iterations (OWASP 2023+ recommendation)
	// Implementing PBKDF2 manually to avoid external dependencies
	iterations := 120000
	saltedPassword := pbkdf2Key([]byte(password), salt, iterations, 32, sha256.New)

	// Calculate ClientKey
	clientKey := hmacSum(saltedPassword, []byte("Client Key"))

	// Calculate StoredKey (hash of ClientKey)
	storedKey := sha256.Sum256(clientKey)

	// Encode components
	saltB64 := base64.StdEncoding.EncodeToString(salt)
	storedKeyB64 := base64.StdEncoding.EncodeToString(storedKey[:])
	serverKeyB64 := base64.StdEncoding.EncodeToString(hmacSum(saltedPassword, []byte("Server Key")))

	// Format: SCRAM-SHA-256$<iterations>:<salt>$<storedKey>:<serverKey>
	hash := fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s",
		iterations, saltB64, storedKeyB64, serverKeyB64)

	return hash, nil
}

// pbkdf2Key derives a key from password and salt using PBKDF2.
func pbkdf2Key(password, salt []byte, iter, keyLen int, hashFunc func() hash.Hash) []byte {
	prf := hmac.New(hashFunc, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		// N.B.: || means concatenation, ^ means XOR
		// for each block T_i = U_1 ^ U_2 ^ ... ^ U_iter
		// U_1 = PRF(password, salt || INT_32_BE(i))
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(u, t)

		// U_n = PRF(password, U_(n-1))
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = u[:0]
			u = prf.Sum(u)
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return dk[:keyLen]
}

// hmacSum calculates HMAC-SHA256
func hmacSum(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// VerifySCRAMSHA256 verifies a password against a SCRAM-SHA-256 hash.
func VerifySCRAMSHA256(password, hash string) (bool, error) {
	// Parse hash format: SCRAM-SHA-256$<iterations>:<salt>$<storedKey>:<serverKey>
	parts := strings.Split(hash, "$")
	if len(parts) != 3 {
		return false, fmt.Errorf("invalid hash format")
	}

	if parts[0] != "SCRAM-SHA-256" {
		return false, fmt.Errorf("unsupported algorithm: %s", parts[0])
	}

	// Parse iterations and salt
	iterSalt := strings.Split(parts[1], ":")
	if len(iterSalt) != 2 {
		return false, fmt.Errorf("invalid iterations/salt format")
	}

	iterations, err := strconv.Atoi(iterSalt[0])
	if err != nil {
		return false, fmt.Errorf("invalid iterations: %v", err)
	}

	salt, err := base64.StdEncoding.DecodeString(iterSalt[1])
	if err != nil {
		return false, fmt.Errorf("invalid salt: %v", err)
	}

	// Parse stored key and server key
	keys := strings.Split(parts[2], ":")
	if len(keys) != 2 {
		return false, fmt.Errorf("invalid keys format")
	}

	expectedStoredKey, err := base64.StdEncoding.DecodeString(keys[0])
	if err != nil {
		return false, fmt.Errorf("invalid stored key: %v", err)
	}

	// Calculate
	saltedPassword := pbkdf2Key([]byte(password), salt, iterations, 32, sha256.New)
	clientKey := hmacSum(saltedPassword, []byte("Client Key"))
	calculatedStoredKey := sha256.Sum256(clientKey)

	return subtle.ConstantTimeCompare(expectedStoredKey, calculatedStoredKey[:]) == 1, nil
}

// SCRAMClient implements SCRAM-SHA-256 client-side authentication.
// Used when the proxy needs to authenticate to a backend server.
type SCRAMClient struct {
	username    string
	password    string
	mechanism   string
	clientNonce string
	salt        []byte
	saltedPass  []byte
	clientFirst string
	serverFirst string
	clientFinal string
}

// NewSCRAMClient creates a new SCRAM client for authentication.
func NewSCRAMClient(username, password, mechanism string) *SCRAMClient {
	return &SCRAMClient{
		username:  username,
		password:  password,
		mechanism: mechanism,
	}
}

// BuildClientFirst creates the client-first message.
func (c *SCRAMClient) BuildClientFirst(username string) string {
	// Generate client nonce
	nonceBytes := make([]byte, 18)
	rand.Read(nonceBytes)
	c.clientNonce = base64.StdEncoding.EncodeToString(nonceBytes)

	// Format: n=username,r=nonce (n = SCRAM-SHA-256 mechanism flag)
	c.username = username
	c.clientFirst = fmt.Sprintf("n=%s,r=%s", username, c.clientNonce)
	return c.clientFirst
}

// BuildClientFinal creates the client-final message after receiving server challenge.
func (c *SCRAMClient) BuildClientFinal(serverFirst string) string {
	// Parse server-first message: r=nonce,s=salt,i=iterations
	parts := strings.Split(serverFirst, ",")
	var serverNonce string
	for _, part := range parts {
		if strings.HasPrefix(part, "r=") {
			serverNonce = part[2:]
		} else if strings.HasPrefix(part, "s=") {
			saltB64 := part[2:]
			c.salt, _ = base64.StdEncoding.DecodeString(saltB64)
		}
	}

	if serverNonce == "" || c.salt == nil {
		return ""
	}

	// Store server-first for AuthMessage computation
	c.serverFirst = serverFirst

	// Append client nonce to server nonce for combined nonce
	combinedNonce := serverNonce + c.clientNonce

	// Compute salted password with 120000 iterations
	c.saltedPass = pbkdf2Key([]byte(c.password), c.salt, 120000, 32, sha256.New)

	// Compute ClientKey
	clientKey := hmacSum(c.saltedPass, []byte("Client Key"))

	// Compute AuthMessage = client-first-bare + server-first + client-final-without-proof
	// client-final-without-proof = channel-binding "," nonce
	c.clientFinal = fmt.Sprintf("c=biws,r=%s", combinedNonce)
	authMessage := c.clientFirst + "," + c.serverFirst + "," + c.clientFinal

	// Compute ClientSignature = HMAC(SaltedPassword, AuthMessage)
	clientSignature := hmacSum(c.saltedPass, []byte(authMessage))

	// Compute ClientProof: ClientKey XOR ClientSignature
	clientProof := make([]byte, len(clientKey))
	for i := range clientKey {
		clientProof[i] = clientKey[i] ^ clientSignature[i]
	}

	proofB64 := base64.StdEncoding.EncodeToString(clientProof)

	return fmt.Sprintf("c=biws,r=%s,p=%s", combinedNonce, proofB64)
}

// authMessage holds the full SCRAM authentication exchange for server signature verification.
// Stored during BuildClientFinal and used in VerifyServerFinal.
var authMessage string

// VerifyServerFinal verifies the server's final message.
// Per RFC 5802, ServerSignature = HMAC(ServerKey, AuthMessage)
// where AuthMessage = client-first-message-bare + "," + server-first-message + "," + client-final-message-without-proof
func (c *SCRAMClient) VerifyServerFinal(serverFinal string) bool {
	if !strings.HasPrefix(serverFinal, "v=") {
		return false
	}

	// Parse server signature
	serverSigB64 := strings.TrimPrefix(serverFinal, "v=")
	expectedServerSig, err := base64.StdEncoding.DecodeString(serverSigB64)
	if err != nil {
		return false
	}

	// Recompute ServerKey = HMAC(SaltedPassword, "Server Key")
	serverKey := hmacSum(c.saltedPass, []byte("Server Key"))

	// Build the AuthMessage that was used for server signature
	// Per RFC 5802: AuthMessage = client-first-message-bare + "," + server-first-message + "," + client-final-message-without-proof
	authMessage := c.clientFirst + "," + c.serverFirst + "," + c.clientFinal

	// ServerSignature = HMAC(ServerKey, AuthMessage)
	recomputedSig := hmacSum(serverKey, []byte(authMessage))

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare(expectedServerSig, recomputedSig) == 1
}
