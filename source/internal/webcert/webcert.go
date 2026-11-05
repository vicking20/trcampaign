// Package webcert manages the TLS certificate of the launcher HTTPS listener.
//
// Without an operator certificate the server creates a self-signed one once.
// Player packages carry its public-key fingerprint (Pin), and the launcher
// accepts only that key, so no certificate authority is needed.
package webcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ensure writes a self-signed certificate and key for hosts when certPath
// does not exist yet. created reports whether new files were written.
func Ensure(certPath, keyPath string, hosts []string) (created bool, err error) {
	if _, err := os.Stat(certPath); err == nil {
		return false, nil
	}
	certPEM, keyPEM, err := Generate(hosts, time.Now())
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return false, err
	}
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// Generate returns a self-signed ECDSA P-256 certificate valid for 10 years.
// Each host is added as a DNS name or IP address.
func Generate(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "nfsrun launcher API", Organization: []string{"NFS The Run private server"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

func leaf(certPEM []byte) (*x509.Certificate, error) {
	b, _ := pem.Decode(certPEM)
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(b.Bytes)
}

// Pin returns the SHA-256 of the certificate's public key (SPKI), in hex.
func Pin(certPEM []byte) (string, error) {
	c, err := leaf(certPEM)
	if err != nil {
		return "", err
	}
	return PinOf(c), nil
}

// PinOf is Pin for a parsed certificate.
func PinOf(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// SelfSigned reports whether the first certificate in certPEM signed itself.
func SelfSigned(certPEM []byte) (bool, error) {
	c, err := leaf(certPEM)
	if err != nil {
		return false, err
	}
	if c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) != nil {
		return false, nil
	}
	return c.Issuer.String() == c.Subject.String(), nil
}

// NormalizePin accepts a pin as 64 hex characters, optionally prefixed with
// "sha256/" or written with colons, and returns lowercase hex.
func NormalizePin(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "sha256/")
	v = strings.ReplaceAll(v, ":", "")
	if len(v) != 64 {
		return "", fmt.Errorf("certificate pin must be 64 hex characters")
	}
	if _, err := hex.DecodeString(v); err != nil {
		return "", fmt.Errorf("certificate pin is not hex")
	}
	return v, nil
}
