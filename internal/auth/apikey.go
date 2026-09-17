package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

const (
	PrefixLive = "whk_live_"
	// key random part length 32 bytes -> 64 hex chars
	KeyRandomBytes = 32
)

// GenerateAPIKey creates a new API key plaintext and its hash/prefix.
// Returns plaintext (to show once), prefix (first 8 chars of random for lookup), hash (hex sha256).
func GenerateAPIKey() (plaintext, prefix, hash string, err error) {
	b := make([]byte, KeyRandomBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	randomHex := hex.EncodeToString(b) // 64 chars
	plaintext = PrefixLive + randomHex
	// prefix for fast lookup: first 8 chars after whk_live_
	if len(randomHex) >= 8 {
		prefix = PrefixLive + randomHex[:8]
	} else {
		prefix = PrefixLive
	}
	hash = HashAPIKey(plaintext)
	return plaintext, prefix, hash, nil
}

// HashAPIKey returns hex(sha256(plaintext))
func HashAPIKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// Verify compares plaintext against stored hash using constant-time compare
func Verify(plaintext, storedHash string) bool {
	computed := HashAPIKey(plaintext)
	if len(computed) != len(storedHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// ExtractBearer extracts token from "Bearer whk_live_..."
func ExtractBearer(authHeader string) (string, bool) {
	const bearer = "Bearer "
	if len(authHeader) <= len(bearer) {
		return "", false
	}
	if authHeader[:len(bearer)] != bearer {
		return "", false
	}
	token := authHeader[len(bearer):]
	if token == "" {
		return "", false
	}
	return token, true
}

// ValidateFormat checks prefix
func ValidateFormat(token string) error {
	if len(token) < len(PrefixLive)+10 {
		return fmt.Errorf("invalid api key format")
	}
	if token[:len(PrefixLive)] != PrefixLive {
		return fmt.Errorf("api key must start with %s", PrefixLive)
	}
	return nil
}
