// Package auth provides NTLMSSP authentication support for MSSQL connections.
// Implements NTLMv2 challenge-response for interception mode proxying.
package auth

import (
	"crypto/des"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// NTLM message types.
const (
	ntlmTypeNegotiate    = 1
	ntlmTypeChallenge    = 2
	ntlmTypeAuthenticate = 3
)

var ntlmssPSignature = []byte("NTLMSSP\x00")

// NTLMNegotiate represents a Type 1 (Negotiate) message.
type NTLMNegotiate struct {
	Flags       uint32
	Domain      string
	Workstation string
}

// NTLMChallenge represents a Type 2 (Challenge) message.
type NTLMChallenge struct {
	TargetName string
	Challenge  [8]byte
	Flags      uint32
	TargetInfo []byte
}

// NTLMAuthenticate represents a Type 3 (Authenticate) message.
type NTLMAuthenticate struct {
	LMResponse          []byte
	NTLMResponse        []byte
	Domain              string
	User                string
	Workstation         string
	EncryptedSessionKey []byte
	Flags               uint32
}

// NTLM flags.
const (
	ntlmFlagUnicode       uint32 = 0x00000001
	ntlmFlagNTLM          uint32 = 0x00000200
	ntlmFlagTargetInfo    uint32 = 0x00800000
	ntlmFlagVersion       uint32 = 0x02000000
	ntlmFlag128Bit        uint32 = 0x20000000
	ntlmFlag56Bit         uint32 = 0x08000000
	ntlmFlagKeyExchange   uint32 = 0x40000000
	ntlmFlagExtendedSec   uint32 = 0x00080000
	ntlmFlagNegotiateSign uint32 = 0x00000010
	ntlmFlagNegotiateSeal uint32 = 0x00000020
	ntlmFlagAlwaysSign    uint32 = 0x00008000
)

// defaultServerFlags returns flags the proxy's NTLM server will advertise.
func defaultServerFlags() uint32 {
	return ntlmFlagUnicode | ntlmFlagNTLM | ntlmFlagTargetInfo |
		ntlmFlag128Bit | ntlmFlag56Bit | ntlmFlagExtendedSec |
		ntlmFlagNegotiateSign | ntlmFlagNegotiateSeal | ntlmFlagAlwaysSign
}

// ParseNTLMNegotiate parses a Type 1 NTLM message.
func ParseNTLMNegotiate(data []byte) (*NTLMNegotiate, error) {
	if len(data) < 16 {
		return nil, errors.New("NTLM negotiate message too short")
	}
	if !isNTLMSSPSignature(data[:8]) {
		return nil, errors.New("invalid NTLMSSP signature")
	}
	if typ := binary.LittleEndian.Uint32(data[8:12]); typ != ntlmTypeNegotiate {
		return nil, fmt.Errorf("expected Type 1, got Type %d", typ)
	}

	n := &NTLMNegotiate{
		Flags: binary.LittleEndian.Uint32(data[12:16]),
	}

	if len(data) >= 32 {
		domainLen := int(binary.LittleEndian.Uint16(data[16:18]))
		domainOffset := int(binary.LittleEndian.Uint32(data[20:24]))
		if domainOffset+domainLen <= len(data) {
			n.Domain = decodeNTLMString(data[domainOffset:domainOffset+domainLen], n.Flags&ntlmFlagUnicode != 0)
		}
		wsLen := int(binary.LittleEndian.Uint16(data[24:26]))
		wsOffset := int(binary.LittleEndian.Uint32(data[28:32]))
		if wsOffset+wsLen <= len(data) {
			n.Workstation = decodeNTLMString(data[wsOffset:wsOffset+wsLen], n.Flags&ntlmFlagUnicode != 0)
		}
	}

	return n, nil
}

// ParseNTLMChallenge parses a Type 2 NTLM message.
func ParseNTLMChallenge(data []byte) (*NTLMChallenge, error) {
	if len(data) < 32 {
		return nil, errors.New("NTLM challenge message too short")
	}
	if !isNTLMSSPSignature(data[:8]) {
		return nil, errors.New("invalid NTLMSSP signature")
	}
	if typ := binary.LittleEndian.Uint32(data[8:12]); typ != ntlmTypeChallenge {
		return nil, fmt.Errorf("expected Type 2, got Type %d", typ)
	}

	c := &NTLMChallenge{
		Flags: binary.LittleEndian.Uint32(data[20:24]),
	}
	copy(c.Challenge[:], data[24:32])

	// Target name
	if len(data) >= 48 {
		nameLen := int(binary.LittleEndian.Uint16(data[12:14]))
		nameOffset := int(binary.LittleEndian.Uint32(data[16:20]))
		if nameOffset+nameLen <= len(data) {
			c.TargetName = decodeNTLMString(data[nameOffset:nameOffset+nameLen], c.Flags&ntlmFlagUnicode != 0)
		}
	}

	// Target info (if present)
	if len(data) >= 48 {
		infoLen := int(binary.LittleEndian.Uint16(data[40:42]))
		infoOffset := int(binary.LittleEndian.Uint32(data[44:48]))
		if infoOffset+infoLen <= len(data) {
			c.TargetInfo = make([]byte, infoLen)
			copy(c.TargetInfo, data[infoOffset:infoOffset+infoLen])
		}
	}

	return c, nil
}

// ParseNTLMAuthenticate parses a Type 3 NTLM message.
func ParseNTLMAuthenticate(data []byte) (*NTLMAuthenticate, error) {
	if len(data) < 64 {
		return nil, errors.New("NTLM authenticate message too short")
	}
	if !isNTLMSSPSignature(data[:8]) {
		return nil, errors.New("invalid NTLMSSP signature")
	}
	if typ := binary.LittleEndian.Uint32(data[8:12]); typ != ntlmTypeAuthenticate {
		return nil, fmt.Errorf("expected Type 3, got Type %d", typ)
	}

	a := &NTLMAuthenticate{
		Flags: binary.LittleEndian.Uint32(data[60:64]),
	}

	// LM Response
	a.LMResponse = readField(data, 12)
	// NTLM Response
	a.NTLMResponse = readField(data, 20)
	// Domain
	a.Domain = decodeNTLMString(readField(data, 28), a.Flags&ntlmFlagUnicode != 0)
	// User
	a.User = decodeNTLMString(readField(data, 36), a.Flags&ntlmFlagUnicode != 0)
	// Workstation
	a.Workstation = decodeNTLMString(readField(data, 44), a.Flags&ntlmFlagUnicode != 0)

	if len(data) >= 68 {
		a.EncryptedSessionKey = readField(data, 52)
	}

	return a, nil
}

// GenerateChallenge creates a Type 2 NTLM challenge message.
func GenerateChallenge(targetName string) ([]byte, error) {
	encodedTarget := encodeNTLMString(strings.ToUpper(targetName), true)
	flags := defaultServerFlags()

	// Generate random 8-byte server challenge
	var challenge [8]byte
	if _, err := rand.Read(challenge[:]); err != nil {
		return nil, fmt.Errorf("failed to generate challenge: %w", err)
	}

	// Build target info blob
	targetInfo := buildTargetInfo(targetName)

	// Calculate offsets
	targetNameOffset := uint32(48) // header + 2 security buffers
	targetInfoOffset := targetNameOffset + uint32(len(encodedTarget))
	totalLen := targetInfoOffset + uint32(len(targetInfo))

	buf := make([]byte, totalLen)

	// Signature
	copy(buf[0:8], ntlmssPSignature)
	// Type
	binary.LittleEndian.PutUint32(buf[8:12], ntlmTypeChallenge)
	// Target name field (security buffer at offset 12)
	putSecBuf(buf[12:20], uint16(len(encodedTarget)), targetNameOffset)
	// Flags
	binary.LittleEndian.PutUint32(buf[20:24], flags)
	// Server challenge
	copy(buf[24:32], challenge[:])
	// Context (8 bytes, zeroed)
	// Target info field (security buffer at offset 40)
	putSecBuf(buf[40:48], uint16(len(targetInfo)), targetInfoOffset)
	// Target name data
	copy(buf[targetNameOffset:], encodedTarget)
	// Target info data
	copy(buf[targetInfoOffset:], targetInfo)

	return buf, nil
}

// VerifyNTLMResponse verifies a Type 3 NTLM response against a stored NT password hash.
// The passwordHash should be the NT hash (MD4 of UTF-16LE password).
func VerifyNTLMResponse(auth *NTLMAuthenticate, serverChallenge [8]byte, passwordHash []byte) bool {
	if len(auth.NTLMResponse) < 16 {
		return false
	}

	// For NTLMv2: the NTLM response is 16 bytes of HMAC-MD5 followed by the client blob
	ntProofStr := auth.NTLMResponse[:16]
	clientBlob := auth.NTLMResponse[16:]

	// Derive NTLMv2 response key from NT hash
	// ResponseKeyNT = HMAC_MD5(NTHash, UNICODE(uppercase(username) + domain))
	responseKeyNT := hmacMD5(passwordHash, encodeNTLMString(
		strings.ToUpper(auth.User)+strings.ToUpper(auth.Domain), true,
	))

	// Compute NTProofStr = HMAC_MD5(ResponseKeyNT, serverChallenge + clientBlob)
	expectedProof := hmacMD5(responseKeyNT, append(serverChallenge[:], clientBlob...))

	return hmac.Equal(ntProofStr, expectedProof)
}

// ComputeNTHash computes the NT hash (MD4 of UTF-16LE encoded password).
// This is the standard NTLM password hash format.
func ComputeNTHash(password string) []byte {
	encoded := encodeNTLMString(password, true)
	return md4Hash(encoded)
}

// --- Internal helpers ---

func isNTLMSSPSignature(sig []byte) bool {
	return len(sig) >= 8 && string(sig[:8]) == "NTLMSSP\x00"
}

func decodeNTLMString(data []byte, unicode bool) string {
	if unicode && len(data) >= 2 {
		// Decode UTF-16LE to string
		runes := make([]uint16, len(data)/2)
		for i := range runes {
			runes[i] = binary.LittleEndian.Uint16(data[i*2:])
		}
		return strings.TrimRight(string(utf16.Decode(runes)), "\x00")
	}
	return strings.TrimRight(string(data), "\x00")
}

func encodeNTLMString(s string, unicode bool) []byte {
	if unicode {
		runes := []rune(s)
		b := make([]byte, len(runes)*2)
		for i, r := range runes {
			binary.LittleEndian.PutUint16(b[i*2:], uint16(r))
		}
		return b
	}
	return []byte(s)
}

// readField reads a security buffer field from an NTLM message.
func readField(data []byte, offset int) []byte {
	if offset+8 > len(data) {
		return nil
	}
	length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
	fieldOffset := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
	if fieldOffset+length > len(data) {
		return nil
	}
	return data[fieldOffset : fieldOffset+length]
}

func putSecBuf(buf []byte, length uint16, offset uint32) {
	binary.LittleEndian.PutUint16(buf[0:2], length)
	binary.LittleEndian.PutUint16(buf[2:4], length) // allocated
	binary.LittleEndian.PutUint32(buf[4:8], offset)
}

// buildTargetInfo constructs the AV_PAIR structure for Type 2 messages.
func buildTargetInfo(serverName string) []byte {
	var buf []byte

	// AvNbDomainName (type 2)
	name := encodeNTLMString(strings.ToUpper(serverName), true)
	buf = append(buf, 0x02, 0x00)
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, uint16(len(name)))
	buf = append(buf, b...)
	buf = append(buf, name...)

	// AvNbComputerName (type 1)
	comp := encodeNTLMString("GERYON", true)
	buf = append(buf, 0x01, 0x00)
	binary.LittleEndian.PutUint16(b, uint16(len(comp)))
	buf = append(buf, b...)
	buf = append(buf, comp...)

	// AvEOL (type 0)
	buf = append(buf, 0x00, 0x00, 0x00, 0x00)

	return buf
}

// hmacMD5 computes HMAC-MD5.
func hmacMD5(key, data []byte) []byte {
	h := hmac.New(md5.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// md4Hash computes MD4 hash (used for NT password hashing).
// Implementation per RFC 1320.
func md4Hash(data []byte) []byte {
	var a, b, c, d uint32
	a, b, c, d = 0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476

	msg := padMD4(data)
	n := len(msg) / 64

	for i := 0; i < n; i++ {
		block := msg[i*64 : (i+1)*64]
		aa, bb, cc, dd := a, b, c, d

		var x [16]uint32
		for j := 0; j < 16; j++ {
			x[j] = binary.LittleEndian.Uint32(block[j*4:])
		}

		// Round 1: F function, all 16 words
		r1shifts := []uint32{3, 7, 11, 19}
		for k := 0; k < 16; k++ {
			s := r1shifts[k%4]
			switch k % 4 {
			case 0:
				a = rotl32(a+f(b, c, d)+x[k], s)
			case 1:
				d = rotl32(d+f(a, b, c)+x[k], s)
			case 2:
				c = rotl32(c+f(d, a, b)+x[k], s)
			case 3:
				b = rotl32(b+f(c, d, a)+x[k], s)
			}
		}

		// Round 2: G function, indices [0,4,8,12,1,5,9,13,2,6,10,14,3,7,11,15]
		r2idx := []int{0, 4, 8, 12, 1, 5, 9, 13, 2, 6, 10, 14, 3, 7, 11, 15}
		r2shifts := []uint32{3, 5, 9, 13}
		for k := 0; k < 16; k++ {
			s := r2shifts[k%4]
			switch k % 4 {
			case 0:
				a = rotl32(a+g(b, c, d)+x[r2idx[k]]+0x5a827999, s)
			case 1:
				d = rotl32(d+g(a, b, c)+x[r2idx[k]]+0x5a827999, s)
			case 2:
				c = rotl32(c+g(d, a, b)+x[r2idx[k]]+0x5a827999, s)
			case 3:
				b = rotl32(b+g(c, d, a)+x[r2idx[k]]+0x5a827999, s)
			}
		}

		// Round 3: H function, indices [0,8,4,12,2,10,6,14,1,9,5,13,3,11,7,15]
		r3idx := []int{0, 8, 4, 12, 2, 10, 6, 14, 1, 9, 5, 13, 3, 11, 7, 15}
		r3shifts := []uint32{3, 9, 11, 15}
		for k := 0; k < 16; k++ {
			s := r3shifts[k%4]
			switch k % 4 {
			case 0:
				a = rotl32(a+h(b, c, d)+x[r3idx[k]]+0x6ed9eba1, s)
			case 1:
				d = rotl32(d+h(a, b, c)+x[r3idx[k]]+0x6ed9eba1, s)
			case 2:
				c = rotl32(c+h(d, a, b)+x[r3idx[k]]+0x6ed9eba1, s)
			case 3:
				b = rotl32(b+h(c, d, a)+x[r3idx[k]]+0x6ed9eba1, s)
			}
		}

		a += aa
		b += bb
		c += cc
		d += dd
	}

	result := make([]byte, 16)
	binary.LittleEndian.PutUint32(result[0:4], a)
	binary.LittleEndian.PutUint32(result[4:8], b)
	binary.LittleEndian.PutUint32(result[8:12], c)
	binary.LittleEndian.PutUint32(result[12:16], d)
	return result
}

func padMD4(data []byte) []byte {
	bitLen := uint64(len(data) * 8)
	data = append(data, 0x80)
	for len(data)%64 != 56 {
		data = append(data, 0)
	}
	lenBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenBytes, bitLen)
	return append(data, lenBytes...)
}

func f(x, y, z uint32) uint32 { return (x & y) | (^x & z) }
func g(x, y, z uint32) uint32 { return (x & y) | (x & z) | (y & z) }
func h(x, y, z uint32) uint32 { return x ^ y ^ z }

func rotl32(x uint32, n uint32) uint32 {
	return (x << n) | (x >> (32 - n))
}

// desEncrypt encrypts a single 8-byte block with a 7-byte DES key.
func desEncrypt(key, data []byte) []byte {
	// Expand 7-byte key to 8-byte DES key
	desKey := make([]byte, 8)
	desKey[0] = key[0] & 0xFE
	desKey[1] = ((key[0] << 7) | (key[1] >> 1)) & 0xFE
	desKey[2] = ((key[1] << 6) | (key[2] >> 2)) & 0xFE
	desKey[3] = ((key[2] << 5) | (key[3] >> 3)) & 0xFE
	desKey[4] = ((key[3] << 4) | (key[4] >> 4)) & 0xFE
	desKey[5] = ((key[4] << 3) | (key[5] >> 5)) & 0xFE
	desKey[6] = ((key[5] << 2) | (key[6] >> 6)) & 0xFE
	desKey[7] = (key[6] << 1) & 0xFE

	block, _ := des.NewCipher(desKey)
	result := make([]byte, 8)
	block.Encrypt(result, data)
	return result
}
