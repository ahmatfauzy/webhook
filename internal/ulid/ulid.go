package ulid

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// Generate returns a ULID string with prefix like evt_01J...
func Generate(prefix string) string {
	t := ulid.Timestamp(time.Now())
	id, err := ulid.New(t, rand.Reader)
	if err != nil {
		// fallback to timestamp+rand panic is not expected
		panic(err)
	}
	return fmt.Sprintf("%s%s", prefix, id.String())
}

// MustGenerate panics on error (not used, kept for completeness)
func MustGenerate(prefix string) string {
	return Generate(prefix)
}

// ValidPrefixes for validation
var validPrefixes = map[string]bool{
	"evt_":  true,
	"del_":  true,
	"ep_":   true,
	"proj_": true,
	"req_":  true,
	"ak_":   true,
	"sub_":  true,
	"att_":  true,
}

// IsValid checks prefix and length
func IsValid(id string, prefix string) bool {
	if len(id) <= len(prefix) {
		return false
	}
	if id[:len(prefix)] != prefix {
		return false
	}
	// ULID part should be 26 chars
	ulidPart := id[len(prefix):]
	if len(ulidPart) != 26 {
		return false
	}
	_, err := ulid.Parse(ulidPart)
	return err == nil
}
