// Package pki is gomon's built-in certificate authority. It issues the
// Xymon TLS listener's server certificate and client certificates whose
// subjectAltName is the client's hostname, which is how a client proves
// which host it may report for (upstream xymon-tls(7) level 3).
package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CA struct {
	Dir  string
	Cert *x509.Certificate
	key  crypto.Signer
	PEM  []byte
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func encodeKey(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// LoadOrCreate loads the CA from dir, creating it on first use.
func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		host, _ := os.Hostname()
		tmpl := &x509.Certificate{
			SerialNumber:          serial(),
			Subject:               pkix.Name{CommonName: "gomon CA " + host, Organization: []string{"gomon"}},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().AddDate(10, 0, 0),
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			BasicConstraintsValid: true,
			IsCA:                  true,
			MaxPathLenZero:        true,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		kp, err := encodeKey(key)
		if err != nil {
			return nil, err
		}
		if err := writeFile(keyPath, kp, 0o600); err != nil {
			return nil, err
		}
		if err := writeFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
			return nil, err
		}
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("CA key is not a signer")
	}
	return &CA{Dir: dir, Cert: cert, key: signer, PEM: certPEM}, nil
}

// Pool returns a cert pool containing the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

func (ca *CA) sign(tmpl *x509.Certificate, pub crypto.PublicKey) ([]byte, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// IssueClient creates a key and client certificate for hostname.
func (ca *CA) IssueClient(hostname string, days int) (certPEM, keyPEM []byte, err error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return nil, nil, errors.New("hostname required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	certPEM, err = ca.sign(clientTemplate(hostname, days), &key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = encodeKey(key)
	return certPEM, keyPEM, err
}

func clientTemplate(hostname string, days int) *x509.Certificate {
	if days <= 0 {
		days = 365
	}
	return &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, days),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
}

// SignCSR signs a CSR made on the client host (its key never leaves it).
// The certificate always names hostname only, whatever the CSR asked for.
func (ca *CA) SignCSR(csrPEM []byte, hostname string, days int) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || !strings.Contains(block.Type, "CERTIFICATE REQUEST") {
		return nil, errors.New("not a PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature: %w", err)
	}
	if hostname == "" {
		hostname = csr.Subject.CommonName
		if len(csr.DNSNames) > 0 {
			hostname = csr.DNSNames[0]
		}
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return nil, errors.New("hostname required")
	}
	return ca.sign(clientTemplate(hostname, days), csr.PublicKey)
}

// IssueServer writes a server certificate for names into dir (server.pem /
// server.key), reusing an existing one unless it expires within 30 days or
// lacks a requested name.
func (ca *CA) IssueServer(names []string) (certPath, keyPath string, err error) {
	certPath, keyPath = filepath.Join(ca.Dir, "server.pem"), filepath.Join(ca.Dir, "server.key")
	if b, err := os.ReadFile(certPath); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if c, err := x509.ParseCertificate(blk.Bytes); err == nil && time.Until(c.NotAfter) > 30*24*time.Hour && covers(c, names) {
				return certPath, keyPath, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"gomon"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	certPEM, err := ca.sign(tmpl, &key.PublicKey)
	if err != nil {
		return "", "", err
	}
	// Full chain: server certificate then the CA.
	certPEM = append(certPEM, ca.PEM...)
	kp, err := encodeKey(key)
	if err != nil {
		return "", "", err
	}
	if err := writeFile(keyPath, kp, 0o600); err != nil {
		return "", "", err
	}
	if err := writeFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func covers(c *x509.Certificate, names []string) bool {
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			found := false
			for _, i := range c.IPAddresses {
				if i.Equal(ip) {
					found = true
				}
			}
			if !found {
				return false
			}
		} else if c.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

// DefaultServerNames returns the hostname, localhost and local IPs.
func DefaultServerNames(extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, e := range extra {
		add(e)
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				add(ipn.IP.String())
			}
		}
	}
	return out
}

// CertReloader serves a certificate from files, reloading them when they
// change on disk (so renewed certificates are used without a restart).
type CertReloader struct {
	certPath, keyPath string
	mu                sync.Mutex
	cert              *tls.Certificate
	mod               time.Time
	checked           time.Time
}

func NewCertReloader(certPath, keyPath string) (*CertReloader, error) {
	r := &CertReloader{certPath: certPath, keyPath: keyPath}
	if _, err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CertReloader) load() (*tls.Certificate, error) {
	st, err := os.Stat(r.certPath)
	if err != nil {
		return nil, err
	}
	if r.cert != nil && !st.ModTime().After(r.mod) {
		return r.cert, nil
	}
	c, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return nil, err
	}
	r.cert, r.mod = &c, st.ModTime()
	return r.cert, nil
}

func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.checked) > time.Minute {
		r.checked = time.Now()
		if c, err := r.load(); err == nil {
			return c, nil
		}
	}
	return r.cert, nil
}
