package proxy

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestSecretRegistryMintAndLookup(t *testing.T) {
	reg := NewSecretRegistry()

	realKey := "sk-ant-live-real-secret-12345678"
	sentinel := reg.Register("ANTHROPIC_API_KEY", realKey, "api.anthropic.com")

	if !strings.HasPrefix(sentinel, "sk-ant-api03-") {
		t.Errorf("unexpected sentinel format: %s", sentinel)
	}

	// Authorized host lookup succeeds
	lookup, ok := reg.LookupReal(sentinel, "api.anthropic.com")
	if !ok || lookup != realKey {
		t.Errorf("expected real key on matching host, got %q (ok=%v)", lookup, ok)
	}

	// Subdomain lookup succeeds
	lookupSub, ok := reg.LookupReal(sentinel, "v1.api.anthropic.com")
	if !ok || lookupSub != realKey {
		t.Errorf("expected real key on subdomain, got %q (ok=%v)", lookupSub, ok)
	}

	// Unauthorized host lookup fails
	_, ok = reg.LookupReal(sentinel, "evil.com")
	if ok {
		t.Errorf("expected lookup failure on unauthorized host")
	}
}

func TestSecretRegistryHeaderSwapping(t *testing.T) {
	reg := NewSecretRegistry()
	realKey := "sk-ant-live-real-secret-12345678"
	sentinel := reg.Register("ANTHROPIC_API_KEY", realKey, "api.anthropic.com")

	// 1. Authorized swap
	headers := http.Header{
		"X-Api-Key": []string{sentinel},
	}
	if err := reg.SwapHeaders(headers, "api.anthropic.com"); err != nil {
		t.Fatalf("unexpected swap error: %v", err)
	}
	if headers.Get("X-Api-Key") != realKey {
		t.Errorf("expected swapped real key, got %q", headers.Get("X-Api-Key"))
	}

	// 2. Unauthorized swap passes sentinel untouched
	headersUnauth := http.Header{
		"X-Api-Key": []string{sentinel},
	}
	if err := reg.SwapHeaders(headersUnauth, "other.com"); err != nil {
		t.Fatalf("unexpected swap error: %v", err)
	}
	if headersUnauth.Get("X-Api-Key") != sentinel {
		t.Errorf("expected untouched sentinel on unauthorized host, got %q", headersUnauth.Get("X-Api-Key"))
	}

	// 3. Basic-auth header swap
	basicVal := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:"+sentinel))
	headersBasic := http.Header{
		"Authorization": []string{basicVal},
	}
	if err := reg.SwapHeaders(headersBasic, "api.anthropic.com"); err != nil {
		t.Fatalf("unexpected basic auth swap error: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(headersBasic.Get("Authorization"), "Basic "))
	if string(decoded) != "user:"+realKey {
		t.Errorf("expected swapped basic auth, got %q", string(decoded))
	}

	// 4. Outbound leak detection drops request
	headersLeaking := http.Header{
		"Authorization": []string{"Bearer " + realKey},
	}
	err := reg.SwapHeaders(headersLeaking, "evil.com")
	if err == nil || !strings.Contains(err.Error(), "outbound leak detected") {
		t.Errorf("expected leak detection error, got %v", err)
	}
}
