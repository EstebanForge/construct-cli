package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultSessionTokenTTL is the default lifetime of a session token.
	DefaultSessionTokenTTL = 60 * time.Minute

	// TokenIDLength is the length of the token identifier in bytes.
	TokenIDLength = 16

	// TokenPrefix is the prefix for session tokens.
	TokenPrefix = "construct_hs_v1."
)

// TokenID is a non-secret identifier for a session token.
type TokenID string

// SessionToken represents a run-only session authorization token.
type SessionToken struct {
	ID        TokenID
	SessionID SessionID
	ExpiresAt time.Time
	CreatedAt time.Time
	Revoked   bool
}

// tokenEntry stores the token hash and metadata separately.
type tokenEntry struct {
	hash  []byte
	token *SessionToken
}

// TokenManager manages run-only session tokens.
type TokenManager struct {
	tokens   map[TokenID]*tokenEntry
	key      []byte
	mu       sync.RWMutex
	auditKey []byte
}

// NewTokenManager creates a new token manager.
func NewTokenManager(auditKey string) *TokenManager {
	return &TokenManager{
		tokens:   make(map[TokenID]*tokenEntry),
		key:      deriveTokenKey(auditKey),
		auditKey: []byte(auditKey),
	}
}

// deriveTokenKey derives a token signing key from the audit key.
func deriveTokenKey(auditKey string) []byte {
	h := hmac.New(sha256.New, []byte(auditKey))
	h.Write([]byte("construct-token-v1"))
	return h.Sum(nil)
}

// Mint creates a new session token.
func (tm *TokenManager) Mint(sessionID SessionID, ttl time.Duration) (*SessionToken, string, error) {
	if ttl <= 0 {
		ttl = DefaultSessionTokenTTL
	}

	// Generate token ID
	idBytes := make([]byte, TokenIDLength)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate token ID: %w", err)
	}
	tokenID := TokenID(hex.EncodeToString(idBytes))

	// Generate raw token value
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate token: %w", err)
	}
	rawToken := TokenPrefix + hex.EncodeToString(rawBytes)

	// Store hash of token (never store raw value)
	hash := sha256.Sum256([]byte(rawToken))

	token := &SessionToken{
		ID:        tokenID,
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
		Revoked:   false,
	}

	entry := &tokenEntry{
		hash:  hash[:],
		token: token,
	}

	tm.mu.Lock()
	tm.tokens[tokenID] = entry
	tm.mu.Unlock()

	return token, rawToken, nil
}

// Validate checks if a token is valid and not expired/revoked.
func (tm *TokenManager) Validate(rawToken string) (*SessionToken, error) {
	if rawToken == "" {
		return nil, fmt.Errorf("token is empty")
	}

	// Check token prefix
	if len(rawToken) <= len(TokenPrefix) || rawToken[:len(TokenPrefix)] != TokenPrefix {
		return nil, fmt.Errorf("invalid token format")
	}

	// Compute hash
	hash := sha256.Sum256([]byte(rawToken))

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	// Constant-time comparison to prevent timing attacks
	for _, entry := range tm.tokens {
		if hmac.Equal(hash[:], entry.hash) {
			// Check expiration
			if time.Now().After(entry.token.ExpiresAt) {
				return nil, fmt.Errorf("token expired")
			}
			if entry.token.Revoked {
				return nil, fmt.Errorf("token revoked")
			}
			return entry.token, nil
		}
	}

	return nil, fmt.Errorf("invalid or expired token")
}

// Revoke revokes a session token.
func (tm *TokenManager) Revoke(tokenID TokenID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	entry, exists := tm.tokens[tokenID]
	if !exists {
		return fmt.Errorf("token not found")
	}

	entry.token.Revoked = true
	return nil
}

// RevokeBySession revokes all tokens for a given session.
func (tm *TokenManager) RevokeBySession(sessionID SessionID) int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	count := 0
	for _, entry := range tm.tokens {
		if entry.token.SessionID == sessionID && !entry.token.Revoked {
			entry.token.Revoked = true
			count++
		}
	}
	return count
}

// CleanupExpired removes expired tokens.
func (tm *TokenManager) CleanupExpired() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	count := 0
	now := time.Now()
	for tokenID, entry := range tm.tokens {
		if now.After(entry.token.ExpiresAt) {
			delete(tm.tokens, tokenID)
			count++
		}
	}
	return count
}
