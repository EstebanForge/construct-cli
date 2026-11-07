package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CA manages root certificate authority and leaf certificate signing for MITM proxy.
type CA struct {
	mu        sync.RWMutex
	cert      *x509.Certificate
	key       *ecdsa.PrivateKey
	tlsCert   tls.Certificate
	certPEM   []byte
	leafCache map[string]*cachedCert
	leafOrder []string
	maxCache  int
}

type cachedCert struct {
	cert      *tls.Certificate
	expiresAt time.Time
}

// EnsureCA loads an existing CA from caDir, or generates a fresh one.
func EnsureCA(caDir string) (*CA, error) {
	if err := os.MkdirAll(caDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create CA directory: %w", err)
	}

	certPath := filepath.Join(caDir, "construct-ca.crt")
	keyPath := filepath.Join(caDir, "construct-ca.key")

	certPEM, errCert := os.ReadFile(certPath)
	keyPEM, errKey := os.ReadFile(keyPath)

	if errCert == nil && errKey == nil {
		return parseCA(certPEM, keyPEM)
	}

	return generateCA(certPath, keyPath)
}

func generateCA(certPath, keyPath string) (*CA, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA private key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Construct Agent Proxy CA"},
			CommonName:   "Construct Root CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0), // 5 years validity
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CA private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("failed to save CA key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return nil, fmt.Errorf("failed to save CA certificate: %w", err)
	}

	return parseCA(certPEM, keyPEM)
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid CA keypair: %w", err)
	}

	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("invalid CA certificate: %w", err)
	}

	privKey, ok := tlsCert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("CA private key is not ECDSA")
	}

	return &CA{
		cert:      x509Cert,
		key:       privKey,
		tlsCert:   tlsCert,
		certPEM:   certPEM,
		leafCache: make(map[string]*cachedCert),
		maxCache:  1024,
	}, nil
}

// CertPEM returns the PEM-encoded CA public certificate.
func (ca *CA) CertPEM() []byte {
	return ca.certPEM
}

// SignHostCert issues or retrieves a cached leaf certificate for the given hostname.
func (ca *CA) SignHostCert(host string) (*tls.Certificate, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	now := time.Now()

	ca.mu.RLock()
	if cached, ok := ca.leafCache[host]; ok && cached.expiresAt.After(now) {
		ca.mu.RUnlock()
		return cached.cert, nil
	}
	ca.mu.RUnlock()

	ca.mu.Lock()
	defer ca.mu.Unlock()

	// Double-check under write lock
	if cached, ok := ca.leafCache[host]; ok && cached.expiresAt.After(now) {
		return cached.cert, nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate leaf key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate leaf serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &priv.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to sign leaf certificate: %w", err)
	}

	leaf := &tls.Certificate{
		Certificate: [][]byte{derBytes, ca.cert.Raw},
		PrivateKey:  priv,
	}

	// LRU eviction if cache exceeds capacity
	if len(ca.leafCache) >= ca.maxCache && len(ca.leafOrder) > 0 {
		oldest := ca.leafOrder[0]
		ca.leafOrder = ca.leafOrder[1:]
		delete(ca.leafCache, oldest)
	}

	ca.leafCache[host] = &cachedCert{
		cert:      leaf,
		expiresAt: now.Add(23 * time.Hour),
	}
	ca.leafOrder = append(ca.leafOrder, host)

	return leaf, nil
}
