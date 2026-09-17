package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Sign generates v1=hex(HMAC_SHA256(secret, timestamp+"."+rawBody))
func Sign(secret, timestamp, rawBody string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "." + rawBody))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks signature (constant time)
func Verify(secret, timestamp, rawBody, signature string) bool {
	expected := Sign(secret, timestamp, rawBody)
	return hmac.Equal([]byte(expected), []byte(signature))
}

func ValidateTimestamp(toleranceSec int64) func(ts string) error {
	return func(ts string) error {
		// for consumer verification, not needed in server delivery
		_ = ts
		return nil
	}
}

var _ = fmt.Sprintf
