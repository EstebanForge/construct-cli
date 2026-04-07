package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEventType represents the type of security event.
type AuditEventType string

const (
	// EventSessionStart marks the start of a hide-secrets session.
	EventSessionStart AuditEventType = "session.start"
	// EventSessionEnd marks the end of a hide-secrets session.
	EventSessionEnd AuditEventType = "session.end"
	// EventTokenMint indicates a session token was created.
	EventTokenMint AuditEventType = "token.mint"
	// EventTokenRevoke indicates a session token was revoked.
	EventTokenRevoke AuditEventType = "token.revoke"
	// EventTokenReject indicates a session token was rejected.
	EventTokenReject AuditEventType = "token.reject"
	// EventProxyInvokeAllow indicates a proxy request was allowed.
	EventProxyInvokeAllow AuditEventType = "proxy.invoke.allow"
	// EventProxyInvokeDeny indicates a proxy request was denied.
	EventProxyInvokeDeny AuditEventType = "proxy.invoke.deny"
	// EventProxyPolicyViolation indicates a proxy policy was violated.
	EventProxyPolicyViolation AuditEventType = "proxy.policy.violation"
	// EventRedactionSummary summarizes redaction statistics.
	EventRedactionSummary AuditEventType = "redaction.summary"
)

// AuditEvent represents a single security event in the chain.
type AuditEvent struct {
	Timestamp  time.Time              `json:"ts"`
	SessionID  string                 `json:"session_id"`
	Event      AuditEventType         `json:"event"`
	Actor      string                 `json:"actor"`
	Outcome    string                 `json:"outcome"`
	Provider   string                 `json:"provider,omitempty"`
	Capability string                 `json:"capability,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	PrevHMAC   string                 `json:"prev_hmac,omitempty"`
	ChainHMAC  string                 `json:"chain_hmac"`
}

// AuditLog manages the HMAC-chained audit log.
type AuditLog struct {
	logPath   string
	statePath string
	key       []byte
	mu        sync.Mutex
	headHMAC  string
}

// NewAuditLog creates or opens an audit log.
func NewAuditLog(securityDir, auditKey string) (*AuditLog, error) {
	logPath := filepath.Join(securityDir, "audit", "security-audit.log")
	statePath := filepath.Join(securityDir, "audit", "security-audit.state")

	// Ensure audit directory exists
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create audit directory: %w", err)
	}

	al := &AuditLog{
		logPath:   logPath,
		statePath: statePath,
		key:       []byte(auditKey),
	}

	// Load existing chain state if available
	if data, err := os.ReadFile(statePath); err == nil {
		var state struct {
			HeadHMAC string `json:"head_hmac"`
		}
		if err := json.Unmarshal(data, &state); err == nil {
			al.headHMAC = state.HeadHMAC
		}
	}

	return al, nil
}

// Append adds a new event to the audit chain.
func (al *AuditLog) Append(event *AuditEvent) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	event.Timestamp = time.Now()
	event.PrevHMAC = al.headHMAC

	// Compute chain HMAC
	chainData := al.headHMAC + string(al.marshalEvent(event))
	hash := hmac.New(sha256.New, al.key)
	hash.Write([]byte(chainData))
	event.ChainHMAC = hex.EncodeToString(hash.Sum(nil))

	// Write to log file
	f, err := os.OpenFile(al.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			// Log the close error but don't overwrite the original error
			fmt.Fprintf(os.Stderr, "warning: failed to close audit log: %v\n", cerr)
		}
	}()

	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("failed to write audit event: %w", err)
	}

	// Update chain state
	al.headHMAC = event.ChainHMAC
	if err := al.saveState(); err != nil {
		return fmt.Errorf("failed to save audit state: %w", err)
	}

	return nil
}

// Verify verifies the integrity of the audit chain.
func (al *AuditLog) Verify() error {
	al.mu.Lock()
	defer al.mu.Unlock()

	data, err := os.ReadFile(al.logPath)
	if err != nil {
		return fmt.Errorf("failed to read audit log: %w", err)
	}

	lines := splitLines(data)
	prevHMAC := ""
	hash := hmac.New(sha256.New, al.key)

	for i, line := range lines {
		if len(line) == 0 {
			continue
		}

		var event AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("failed to unmarshal event at line %d: %w", i+1, err)
		}

		if event.PrevHMAC != prevHMAC {
			return fmt.Errorf("chain broken at line %d: prev_hmac mismatch", i+1)
		}

		// Recompute HMAC
		chainData := prevHMAC + string(line)
		hash.Reset()
		hash.Write([]byte(chainData))
		expectedHMAC := hex.EncodeToString(hash.Sum(nil))

		if event.ChainHMAC != expectedHMAC {
			return fmt.Errorf("chain broken at line %d: hmac mismatch", i+1)
		}

		prevHMAC = event.ChainHMAC
	}

	if prevHMAC != al.headHMAC {
		return fmt.Errorf("chain head mismatch with state file")
	}

	return nil
}

// saveState writes the current chain head to the state file.
func (al *AuditLog) saveState() error {
	state := struct {
		HeadHMAC string `json:"head_hmac"`
	}{
		HeadHMAC: al.headHMAC,
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return os.WriteFile(al.statePath, data, 0600)
}

// marshalEvent converts an event to JSON for HMAC computation.
func (al *AuditLog) marshalEvent(event *AuditEvent) []byte {
	// Create a copy without ChainHMAC for signing
	eventCopy := *event
	eventCopy.ChainHMAC = ""
	data, err := json.Marshal(eventCopy)
	if err != nil {
		// Fallback: return empty JSON object on marshal error
		return []byte("{}")
	}
	return data
}

// splitLines splits data into lines.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
