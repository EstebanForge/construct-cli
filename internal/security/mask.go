package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// FixedMask is the static placeholder used when mask_style is "fixed".
	FixedMask = "CONSTRUCT_REDACTED"

	// MaskPrefix is the prefix for all mask placeholders.
	MaskPrefix = "CONSTRUCT_REDACTED_"

	// HashLength is the number of hex characters to include in the hash-based mask.
	HashLength = 8
)

// MaskStyle defines the masking strategy.
type MaskStyle string

const (
	// MaskStyleHash uses a unique hash-based placeholder per secret value.
	MaskStyleHash MaskStyle = "hash"
	// MaskStyleFixed uses a static placeholder for all secrets.
	MaskStyleFixed MaskStyle = "fixed"
)

// Masker creates masked placeholders for secret values.
type Masker struct {
	style MaskStyle
}

// NewMasker creates a new Masker with the specified style.
func NewMasker(style string) *Masker {
	ms := MaskStyleHash
	if style == string(MaskStyleFixed) {
		ms = MaskStyleFixed
	}
	return &Masker{style: ms}
}

// Mask returns a masked placeholder for the given secret value.
// The output never contains raw characters from the input.
func (m *Masker) Mask(secret string) string {
	if m.style == MaskStyleFixed {
		return FixedMask
	}
	// Hash-based: compute stable hash prefix
	h := sha256.Sum256([]byte(secret))
	hashStr := hex.EncodeToString(h[:])
	return fmt.Sprintf("%s%s", MaskPrefix, strings.ToUpper(hashStr[:HashLength]))
}

// IsMaskedValue returns true if a string looks like a masked placeholder.
func IsMaskedValue(s string) bool {
	if s == FixedMask {
		return true
	}
	if strings.HasPrefix(s, MaskPrefix) {
		suffix := strings.TrimPrefix(s, MaskPrefix)
		// Check if suffix is exactly HashLength uppercase hex characters
		if len(suffix) == HashLength {
			for _, c := range suffix {
				isDigit := c >= '0' && c <= '9'
				isHexAlpha := c >= 'A' && c <= 'F'
				if !isDigit && !isHexAlpha {
					return false
				}
			}
			return true
		}
	}
	return false
}
