package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCAAndSignLeaf(t *testing.T) {
	caDir := t.TempDir()

	// 1. Initial generation
	ca, err := EnsureCA(caDir)
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	certPath := filepath.Join(caDir, "construct-ca.crt")
	keyPath := filepath.Join(caDir, "construct-ca.key")

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("cert file does not exist: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key file does not exist: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected key permissions 0600, got %o", info.Mode().Perm())
	}

	if len(ca.CertPEM()) == 0 {
		t.Errorf("expected non-empty CertPEM")
	}

	// 2. Loading existing CA from disk
	caLoaded, err := EnsureCA(caDir)
	if err != nil {
		t.Fatalf("failed to load existing CA: %v", err)
	}
	if string(ca.CertPEM()) != string(caLoaded.CertPEM()) {
		t.Errorf("loaded CA cert differs from generated cert")
	}

	// 3. Leaf signing
	leaf1, err := ca.SignHostCert("api.anthropic.com")
	if err != nil {
		t.Fatalf("failed to sign leaf cert: %v", err)
	}
	if leaf1 == nil || len(leaf1.Certificate) == 0 {
		t.Fatalf("invalid leaf certificate returned")
	}

	// 4. Cached leaf cert return
	leafCached, err := ca.SignHostCert("api.anthropic.com")
	if err != nil {
		t.Fatalf("failed to fetch cached leaf cert: %v", err)
	}
	if leaf1 != leafCached {
		t.Errorf("expected identical pointer from leaf cache")
	}

	// Verify certificate is valid
	if leaf1.PrivateKey == nil {
		t.Errorf("expected non-nil private key on leaf certificate")
	}
}
