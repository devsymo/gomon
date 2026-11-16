// Package xclient sends Xymon protocol messages, in plaintext or over TLS,
// using the same recipient syntax and environment as upstream xymon(1):
//
//	host | host:port | xymon://host[:port] | xymons://host[:port]
//
// TLS settings: XYMON_TLS_CA, XYMON_TLS_CERT, XYMON_TLS_KEY,
// XYMON_TLS_VERIFY (full|peer|none), XYMON_TLS_SNI, XYMONDTLSPORT.
package xclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type TLSOptions struct {
	CA     string // CA bundle file
	Cert   string // client certificate (for host identity)
	Key    string
	Verify string // full (default), peer, none
	SNI    string
}

type Options struct {
	Timeout   time.Duration
	TLS       TLSOptions
	PlainPort int
	TLSPort   int
}

// FromEnv reads the upstream xymon environment variables.
func FromEnv() Options {
	o := Options{Timeout: 15 * time.Second, PlainPort: 1984, TLSPort: 1985}
	if p, err := strconv.Atoi(os.Getenv("XYMONDPORT")); err == nil && p > 0 {
		o.PlainPort = p
	}
	if p, err := strconv.Atoi(os.Getenv("XYMONDTLSPORT")); err == nil && p > 0 {
		o.TLSPort = p
	}
	o.TLS = TLSOptions{
		CA: os.Getenv("XYMON_TLS_CA"), Cert: os.Getenv("XYMON_TLS_CERT"), Key: os.Getenv("XYMON_TLS_KEY"),
		Verify: os.Getenv("XYMON_TLS_VERIFY"), SNI: os.Getenv("XYMON_TLS_SNI"),
	}
	return o
}

// Recipient is a parsed destination.
type Recipient struct {
	Host string
	Port int
	TLS  bool
}

func ParseRecipient(s string, o Options) (Recipient, error) {
	r := Recipient{}
	switch {
	case strings.HasPrefix(s, "xymons://"):
		r.TLS = true
		s = s[9:]
	case strings.HasPrefix(s, "xymon://"):
		s = s[8:]
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		return r, errors.New("HTTP transport is not supported; use xymon:// or xymons://")
	}
	s = strings.TrimSuffix(s, "/")
	r.Port = o.PlainPort
	if r.TLS {
		r.Port = o.TLSPort
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		r.Host = h
		r.Port, err = strconv.Atoi(p)
		if err != nil {
			return r, fmt.Errorf("invalid port in %q", s)
		}
	} else {
		r.Host = strings.Trim(s, "[]")
	}
	if r.Host == "" || r.Host == "0.0.0.0" {
		return r, errors.New("no recipient to send to")
	}
	return r, nil
}

// TLSConfig builds a client TLS config from the options.
func (t TLSOptions) Config(serverName string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if t.SNI != "" {
		cfg.ServerName = t.SNI
	}
	if t.CA != "" {
		pem, err := os.ReadFile(t.CA)
		if err != nil {
			return nil, fmt.Errorf("read CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in %s", t.CA)
		}
		cfg.RootCAs = pool
	}
	if t.Cert != "" {
		key := t.Key
		if key == "" {
			key = t.Cert
		}
		c, err := tls.LoadX509KeyPair(t.Cert, key)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{c}
	}
	switch t.Verify {
	case "none":
		cfg.InsecureSkipVerify = true //nolint:gosec // explicit XYMON_TLS_VERIFY=none
	case "peer":
		// Verify the chain but not the name.
		cfg.InsecureSkipVerify = true //nolint:gosec // chain verified below
		roots := cfg.RootCAs
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no server certificate")
			}
			certs := make([]*x509.Certificate, len(raw))
			for i, r := range raw {
				c, err := x509.ParseCertificate(r)
				if err != nil {
					return err
				}
				certs[i] = c
			}
			inter := x509.NewCertPool()
			for _, c := range certs[1:] {
				inter.AddCert(c)
			}
			_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
			return err
		}
	}
	return cfg, nil
}

// Send sends msg to the recipient and returns the server's response.
func Send(ctx context.Context, r Recipient, msg string, o Options) (string, error) {
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	addr := net.JoinHostPort(r.Host, strconv.Itoa(r.Port))
	d := net.Dialer{}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	var conn net.Conn = raw
	if r.TLS {
		cfg, err := o.TLS.Config(r.Host)
		if err != nil {
			return "", err
		}
		tc := tls.Client(raw, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return "", fmt.Errorf("TLS handshake with %s failed: %w", addr, err)
		}
		conn = tc
	}
	if _, err := io.WriteString(conn, msg); err != nil {
		return "", err
	}
	// Half-close: the server reads to EOF, then answers.
	switch c := conn.(type) {
	case *tls.Conn:
		err = c.CloseWrite()
	case *net.TCPConn:
		err = c.CloseWrite()
	}
	if err != nil {
		return "", err
	}
	resp, err := io.ReadAll(io.LimitReader(conn, 64<<20))
	if err != nil && len(resp) == 0 && !errors.Is(err, io.EOF) {
		// Servers that close without close_notify: treat as end of response.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return "", err
		}
	}
	return string(resp), nil
}
