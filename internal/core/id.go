package core

import (
	"crypto/rand"
	"encoding/hex"
)

// newID returns a random conversation identifier with a human-readable
// prefix, e.g. "sess-1a2b3c4d5e6f7080".
func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never realistically fails; fall back to the prefix alone
		// rather than panicking in library code.
		return prefix
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}
