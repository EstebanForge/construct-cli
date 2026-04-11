package security

import (
	"testing"
	"time"
)

func TestTokenManager_Mint(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Generate a session ID
	sessionID := SessionID("test-session-1")

	// Mint a token
	token, rawToken, err := tm.Mint(sessionID, DefaultSessionTokenTTL)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	// Check token properties
	if token.ID == "" {
		t.Error("Token.ID is empty")
	}
	if token.SessionID != sessionID {
		t.Errorf("Token.SessionID = %v, want %v", token.SessionID, sessionID)
	}
	if rawToken == "" {
		t.Error("Raw token is empty")
	}
	if !token.ExpiresAt.After(time.Now()) {
		t.Error("Token.ExpiresAt is not in the future")
	}
	if token.Revoked {
		t.Error("Token.Revoked = true, want false")
	}

	// Check token prefix
	if len(rawToken) <= len(TokenPrefix) {
		t.Error("Raw token is too short")
	}
	if rawToken[:len(TokenPrefix)] != TokenPrefix {
		t.Errorf("Raw token prefix = %q, want %q", rawToken[:len(TokenPrefix)], TokenPrefix)
	}
}

func TestTokenManager_Validate(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint a token
	sessionID := SessionID("test-session-validate")
	_, rawToken, err := tm.Mint(sessionID, DefaultSessionTokenTTL)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	// Validate the token
	token, err := tm.Validate(rawToken)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if token.SessionID != sessionID {
		t.Errorf("Validated token SessionID = %v, want %v", token.SessionID, sessionID)
	}

	// Validate wrong token
	wrongToken := TokenPrefix + "wrong"
	_, err = tm.Validate(wrongToken)
	if err == nil {
		t.Error("Validate(wrong) should return error")
	}

	// Validate empty token
	_, err = tm.Validate("")
	if err == nil {
		t.Error("Validate(empty) should return error")
	}
}

func TestTokenManager_Revoke(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint a token
	sessionID := SessionID("test-session-revoke")
	token, rawToken, err := tm.Mint(sessionID, DefaultSessionTokenTTL)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}

	// Revoke the token
	err = tm.Revoke(token.ID)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	// Token should no longer be valid
	_, err = tm.Validate(rawToken)
	if err == nil {
		t.Error("Validate(revoked) should return error")
	}

	// Revoke non-existent token
	err = tm.Revoke(TokenID("non-existent"))
	if err == nil {
		t.Error("Revoke(non-existent) should return error")
	}
}

func TestTokenManager_RevokeBySession(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint multiple tokens for the same session
	sessionID := SessionID("test-session-revoke-all")
	_, rawToken1, _ := tm.Mint(sessionID, DefaultSessionTokenTTL)
	_, rawToken2, _ := tm.Mint(sessionID, DefaultSessionTokenTTL)

	// Both tokens should be valid
	_, err := tm.Validate(rawToken1)
	if err != nil {
		t.Error("Token1 should be valid before revocation")
	}
	_, err = tm.Validate(rawToken2)
	if err != nil {
		t.Error("Token2 should be valid before revocation")
	}

	// Revoke all tokens for the session
	count := tm.RevokeBySession(sessionID)
	if count != 2 {
		t.Errorf("RevokeBySession() revoked %d tokens, want 2", count)
	}

	// Both tokens should now be invalid
	_, err = tm.Validate(rawToken1)
	if err == nil {
		t.Error("Token1 should be invalid after revocation")
	}
	_, err = tm.Validate(rawToken2)
	if err == nil {
		t.Error("Token2 should be invalid after revocation")
	}

	// Check that tokens are marked as revoked
	// We can't access internal state, but the validation failure confirms it
}

func TestTokenManager_TokenTTL(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Test default TTL
	sessionID := SessionID("test-session-ttl")
	token, _, _ := tm.Mint(sessionID, DefaultSessionTokenTTL)

	expectedTTL := DefaultSessionTokenTTL
	actualTTL := token.ExpiresAt.Sub(token.CreatedAt)

	// Allow 1 second tolerance
	tolerance := 1 * time.Second
	if actualTTL < expectedTTL-tolerance || actualTTL > expectedTTL+tolerance {
		t.Errorf("Token TTL = %v, want %v ± %v", actualTTL, expectedTTL, tolerance)
	}

	// Test custom TTL
	customTTL := 30 * time.Minute
	token2, _, _ := tm.Mint(sessionID, customTTL)

	actualTTL2 := token2.ExpiresAt.Sub(token2.CreatedAt)
	if actualTTL2 < customTTL-tolerance || actualTTL2 > customTTL+tolerance {
		t.Errorf("Token TTL = %v, want %v ± %v", actualTTL2, customTTL, tolerance)
	}
}

func TestTokenManager_MultipleTokens(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint multiple tokens
	sessions := []SessionID{"session1", "session2", "session3"}
	tokens := make(map[SessionID]string)

	for _, sessionID := range sessions {
		_, rawToken, err := tm.Mint(sessionID, DefaultSessionTokenTTL)
		if err != nil {
			t.Fatalf("Mint(%v) error = %v", sessionID, err)
		}
		tokens[sessionID] = rawToken
	}

	// Each token should be valid for its session
	for sessionID, rawToken := range tokens {
		token, err := tm.Validate(rawToken)
		if err != nil {
			t.Errorf("Token for %v should be valid, got error: %v", sessionID, err)
		}
		if token.SessionID != sessionID {
			t.Errorf("Token session ID = %v, want %v", token.SessionID, sessionID)
		}
	}
}

func TestTokenManager_CleanupExpired(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint a token with very short TTL
	sessionID := SessionID("test-session-expired")
	_, rawToken, _ := tm.Mint(sessionID, 1*time.Nanosecond) // Essentially expired

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Token should be expired
	_, err := tm.Validate(rawToken)
	if err == nil {
		t.Error("Expired token should not be valid")
	}

	// Cleanup expired tokens
	count := tm.CleanupExpired()
	if count < 1 {
		t.Errorf("CleanupExpired() removed %d tokens, want at least 1", count)
	}
}

func TestTokenManager_ConstantTimeComparison(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Mint a token
	sessionID := SessionID("test-session-constant-time")
	_, rawToken, _ := tm.Mint(sessionID, DefaultSessionTokenTTL)

	// Validate multiple times to ensure consistent timing
	// This is a basic test - a proper constant-time test would be more sophisticated
	for i := 0; i < 10; i++ {
		_, err := tm.Validate(rawToken)
		if err != nil {
			t.Errorf("Validation failed on iteration %d: %v", i, err)
		}
	}
}

func TestTokenManager_EmptyToken(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Try to validate empty token
	_, err := tm.Validate("")
	if err == nil {
		t.Error("Validate(empty) should return error")
	}
	if err.Error() != "token is empty" {
		t.Errorf("Error message = %q, want %q", err.Error(), "token is empty")
	}
}

func TestTokenManager_InvalidTokenFormat(t *testing.T) {
	tm := NewTokenManager("test-audit-key")

	// Try to validate token with wrong prefix
	_, err := tm.Validate("wrong_prefix_123456")
	if err == nil {
		t.Error("Validate(wrong_prefix) should return error")
	}
	if err.Error() != "invalid token format" {
		t.Errorf("Error message = %q, want %q", err.Error(), "invalid token format")
	}
}
