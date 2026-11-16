// Package xymond implements the Xymon daemon network protocol: plaintext on
// port 1984 and TLS on port 1985 (compatible with upstream xymon's
// "xymons://" recipients and client certificates naming the host).
package xymond

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gomon/internal/cfgfiles"
	"gomon/internal/engine"
	"gomon/internal/store"
)

const Version = "4.3.32-gomon"

type Options struct {
	StatusSenders     []string
	AdminSenders      []string
	MaxMessageSize    int
	RequireClientCert bool
}

type Server struct {
	eng   *engine.Engine
	store *store.Store
	cfgs  *cfgfiles.Manager
	log   *slog.Logger

	statusNets []*net.IPNet
	adminNets  []*net.IPNet
	maxSize    int

	Stats Stats
	wg    sync.WaitGroup
}

// Stats are exposed in the UI.
type Stats struct {
	Connections   atomic.Int64
	Messages      atomic.Int64
	TLSMessages   atomic.Int64
	Rejected      atomic.Int64
	ClientReports atomic.Int64
}

func parseNets(list []string) []*net.IPNet {
	var out []*net.IPNet
	for _, s := range list {
		s = strings.TrimSpace(s)
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				if ip.To4() != nil {
					s += "/32"
				} else {
					s += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func New(eng *engine.Engine, st *store.Store, cfgs *cfgfiles.Manager, log *slog.Logger, o Options) *Server {
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = 10 << 20
	}
	return &Server{
		eng: eng, store: st, cfgs: cfgs, log: log,
		statusNets: parseNets(o.StatusSenders),
		adminNets:  parseNets(o.AdminSenders),
		maxSize:    o.MaxMessageSize,
	}
}

// Peer describes who sent a message and how.
type Peer struct {
	IP        net.IP
	Transport string   // "plaintext", "TLSv1.3", "TLSv1.3 cert", "local"
	CertNames []string // verified client certificate DNS names
}

func (p Peer) String() string {
	if p.IP == nil {
		return "local"
	}
	return p.IP.String()
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return true // internal
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// MayReportFor: a verified certificate naming the host, or an allowed network.
func (s *Server) mayReportFor(p Peer, host string) bool {
	for _, n := range p.CertNames {
		if strings.EqualFold(n, host) {
			return true
		}
	}
	return inNets(p.IP, s.statusNets)
}

func (s *Server) isAdmin(p Peer) bool { return inNets(p.IP, s.adminNets) }

func (s *Server) mayQuery(p Peer) bool {
	return len(p.CertNames) > 0 || inNets(p.IP, s.statusNets)
}

// ListenPlain serves the plaintext protocol.
func (s *Server) ListenPlain(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.log.Info("xymon protocol listening", "addr", addr, "tls", false)
	return s.serve(ctx, ln, false)
}

// ListenTLS serves Xymon over TLS.
func (s *Server) ListenTLS(ctx context.Context, addr string, cfg *tls.Config) error {
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return err
	}
	s.log.Info("xymon protocol listening", "addr", addr, "tls", true)
	return s.serve(ctx, ln, true)
}

// TLSConfig builds the listener config.
func TLSConfig(getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error), clientCAs *x509.CertPool, require bool) *tls.Config {
	auth := tls.VerifyClientCertIfGiven
	if require {
		auth = tls.RequireAndVerifyClientCert
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: getCert,
		ClientCAs:      clientCAs,
		ClientAuth:     auth,
	}
}

func (s *Server) serve(ctx context.Context, ln net.Listener, isTLS bool) error {
	go func() { <-ctx.Done(); ln.Close() }()
	sem := make(chan struct{}, 512)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		s.Stats.Connections.Add(1)
		sem <- struct{}{}
		s.wg.Add(1)
		go func() {
			defer func() { <-sem; s.wg.Done() }()
			s.handleConn(ctx, c, isTLS)
		}()
	}
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLSv1.3"
	case tls.VersionTLS12:
		return "TLSv1.2"
	}
	return "TLS"
}

func (s *Server) handleConn(ctx context.Context, c net.Conn, isTLS bool) {
	defer c.Close()
	peer := Peer{Transport: "plaintext"}
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		peer.IP = ta.IP
		if v4 := ta.IP.To4(); v4 != nil {
			peer.IP = v4
		}
	}
	// Silent connections are dropped quickly; messages get a full timeout.
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if tc, ok := c.(*tls.Conn); ok {
		if err := tc.HandshakeContext(ctx); err != nil {
			s.log.Debug("tls handshake failed", "peer", peer.String(), "err", err)
			s.Stats.Rejected.Add(1)
			return
		}
		cs := tc.ConnectionState()
		peer.Transport = tlsVersion(cs.Version)
		if len(cs.VerifiedChains) > 0 && len(cs.PeerCertificates) > 0 {
			peer.CertNames = cs.PeerCertificates[0].DNSNames
			if len(peer.CertNames) > 0 {
				peer.Transport += " cert"
			}
		}
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 32*1024)
	first := true
	for {
		n, err := c.Read(tmp)
		if n > 0 {
			if first {
				_ = c.SetDeadline(time.Now().Add(60 * time.Second))
				first = false
			}
			buf = append(buf, tmp[:n]...)
			if len(buf) > s.maxSize {
				s.log.Warn("message too large", "peer", peer.String(), "size", len(buf))
				s.Stats.Rejected.Add(1)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Some legacy senders never half-close: process what we have.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && len(buf) > 0 {
				break
			}
			if len(buf) == 0 {
				return
			}
			break
		}
	}
	if len(buf) == 0 {
		return
	}
	s.Stats.Messages.Add(1)
	if isTLS {
		s.Stats.TLSMessages.Add(1)
	}
	resp := s.Handle(ctx, string(buf), peer)
	if resp != "" {
		_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		_, _ = io.WriteString(c, resp)
	}
	if tc, ok := c.(*tls.Conn); ok {
		_ = tc.CloseWrite()
	}
}
