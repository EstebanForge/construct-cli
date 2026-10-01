package msbembed

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex is the hex digest helper the corruption tests compare against.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
