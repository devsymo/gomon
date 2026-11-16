package xymond

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gomon/internal/analysis"
	"gomon/internal/cfgfiles"
	"gomon/internal/engine"
	"gomon/internal/hostscfg"
	"gomon/internal/model"
	"gomon/internal/protocol"
)

// Handle processes one message and returns the response (may be empty).
func (s *Server) Handle(ctx context.Context, msg string, peer Peer) string {
	msg = strings.TrimLeft(msg, " \t\r\n")
	if msg == "" {
		return ""
	}
	kind := protocol.Kind(msg)
	switch kind {
	case "status":
		s.handleStatus(ctx, msg, peer)
	case "combo":
		for _, m := range protocol.SplitCombo(msg) {
			s.handleStatus(ctx, m, peer)
		}
	case "extcombo":
		parts, err := protocol.SplitExtCombo(msg)
		if err != nil {
			s.log.Warn("extcombo", "peer", peer.String(), "err", err)
			return ""
		}
		for _, m := range parts {
			s.Handle(ctx, m, peer)
		}
	case "data":
		s.handleData(ctx, msg, peer)
	case "client":
		return s.handleClient(ctx, msg, peer)
	case "clientlog":
		return s.handleClientLog(ctx, msg, peer)
	case "notes":
		s.handleNotes(ctx, msg, peer)
	case "enable", "disable":
		s.handleEnableDisable(ctx, kind, msg, peer)
	case "drop":
		s.handleDrop(ctx, msg, peer)
	case "rename":
		s.handleRename(ctx, msg, peer)
	case "ack", "xymondack", "hobbitdack":
		s.handleAck(ctx, msg, peer)
	case "query":
		return s.handleQuery(msg, peer)
	case "xymondlog", "hobbitdlog":
		return s.handleLog(msg, peer)
	case "xymondboard", "hobbitdboard":
		return s.handleBoard(msg, peer)
	case "hostinfo":
		return s.handleHostInfo(msg, peer)
	case "config":
		return s.handleConfig(msg, peer)
	case "ping":
		return "xymond " + Version + "\n"
	case "ghostlist", "multisrclist", "senderstats", "schedule", "download", "xymondxboard", "hobbitdxboard",
		"xymondxlog", "hobbitdxlog", "meta", "modify", "summary", "notify", "dummy", "usermsg", "flush", "hostdatasave":
		// Accepted for compatibility; no-op.
	default:
		s.log.Debug("unknown message", "kind", kind, "peer", peer.String())
	}
	return ""
}

func (s *Server) reject(peer Peer, what, host string) {
	s.Stats.Rejected.Add(1)
	s.log.Warn("rejected message", "what", what, "host", host, "peer", peer.String(), "transport", peer.Transport)
}

func (s *Server) handleStatus(ctx context.Context, msg string, peer Peer) {
	st, err := protocol.ParseStatus(msg)
	if err != nil {
		s.log.Debug("bad status", "peer", peer.String(), "err", err)
		return
	}
	if !s.mayReportFor(peer, st.Host) {
		s.reject(peer, "status", st.Host)
		return
	}
	if st.Color == protocol.Purple {
		return // xymond ignores purple reports
	}
	sender := peer.String()
	if st.Sender != "" && inNets(peer.IP, s.statusNets) {
		sender = st.Sender
	}
	host, err := s.eng.ResolveHost(ctx, st.Host, sender)
	if err != nil {
		s.log.Debug("status for unknown host", "host", st.Host, "peer", sender)
		return
	}
	if st.Color == protocol.ColorClient {
		c := &protocol.Client{Host: host, OS: "", Collector: st.Test, Raw: msg}
		c.Sections = protocol.ParseSections(st.Text)
		s.saveClient(ctx, c, peer, sender)
		return
	}
	if err := s.eng.Submit(ctx, engine.Update{Host: host, Test: st.Test, Color: st.Color, Text: st.Text,
		Lifetime: st.Lifetime, Group: st.Group, Sender: sender, Transport: peer.Transport}); err != nil {
		s.log.Warn("submit status", "host", host, "test", st.Test, "err", err)
	}
}

func (s *Server) handleData(ctx context.Context, msg string, peer Peer) {
	d, err := protocol.ParseData(msg)
	if err != nil {
		return
	}
	if !s.mayReportFor(peer, d.Host) {
		s.reject(peer, "data", d.Host)
		return
	}
	host, err := s.eng.ResolveHost(ctx, d.Host, peer.String())
	if err != nil {
		return
	}
	now := time.Now()
	if d.Name == "trends" {
		for test, ms := range protocol.ParseTrends(d.Body) {
			s.eng.RecordMetrics(host, test, ms, now)
		}
		return
	}
	if ms := protocol.ParseNCV(d.Body, 100); len(ms) > 0 {
		s.eng.RecordMetrics(host, d.Name, ms, now)
	}
}

func (s *Server) handleClient(ctx context.Context, msg string, peer Peer) string {
	c, err := protocol.ParseClient(msg)
	if err != nil {
		s.log.Debug("bad client message", "peer", peer.String(), "err", err)
		return ""
	}
	if !s.mayReportFor(peer, c.Host) {
		s.reject(peer, "client", c.Host)
		return ""
	}
	sender := peer.String()
	if px, ok := c.Get("proxy"); ok && inNets(peer.IP, s.statusNets) {
		for _, ln := range strings.Split(px, "\n") {
			if v, ok := strings.CutPrefix(ln, "ClientIP:"); ok {
				sender = strings.TrimSpace(v)
			}
		}
	}
	host, err := s.eng.ResolveHost(ctx, c.Host, sender)
	if err != nil {
		return ""
	}
	c.Host = host
	s.Stats.ClientReports.Add(1)
	if c.Collector == "" {
		s.eng.SetHostOS(ctx, host, c.OS, c.Class)
	}
	s.saveClient(ctx, c, peer, sender)

	h, _ := s.eng.Host(host)
	hi := analysis.HostInfo{Name: host, OS: c.OS, Class: c.Class}
	if h != nil {
		hi.Page = h.Page
		if h.Class != "" {
			hi.Class = h.Class
		}
	}
	res := analysis.Analyze(c, s.cfgs.Rules(), hi)
	now := time.Now()
	for _, r := range res.Reports {
		if err := s.eng.Submit(ctx, engine.Update{Host: host, Test: r.Test, Color: r.Color, Text: r.Text,
			Sender: sender, Transport: peer.Transport, Time: now}); err != nil {
			s.log.Warn("submit client report", "host", host, "test", r.Test, "err", err)
		}
	}
	for test, ms := range res.Metrics {
		s.eng.RecordMetrics(host, test, ms, now)
	}
	return s.cfgs.ClientLocal().For(host, hi.Class, c.OS)
}

func (s *Server) saveClient(ctx context.Context, c *protocol.Client, peer Peer, sender string) {
	if err := s.store.SaveClientData(ctx, &model.ClientData{Host: c.Host, Collector: c.Collector, Time: time.Now(),
		OS: c.OS, Class: c.Class, Sender: sender, Transport: peer.Transport, Data: c.Raw}); err != nil {
		s.log.Warn("save client data", "host", c.Host, "err", err)
	}
}

// clientlog HOSTNAME [section=NAME,...]
func (s *Server) handleClientLog(ctx context.Context, msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "clientlog", "")
		return ""
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) < 2 {
		return ""
	}
	data, err := s.store.GetClientData(ctx, strings.ToLower(f[1]))
	if err != nil || len(data) == 0 {
		return ""
	}
	var want map[string]bool
	if len(f) > 2 && strings.HasPrefix(f[2], "section=") {
		want = map[string]bool{}
		for _, sec := range strings.Split(strings.TrimPrefix(f[2], "section="), ",") {
			want[sec] = true
		}
	}
	var sb strings.Builder
	for _, d := range data {
		if want == nil {
			sb.WriteString(d.Data)
			continue
		}
		for _, sec := range protocol.ParseSections(d.Data) {
			if want[sec.Name] {
				fmt.Fprintf(&sb, "[%s]\n%s\n", sec.Name, sec.Content)
			}
		}
	}
	return sb.String()
}

func (s *Server) handleNotes(ctx context.Context, msg string, peer Peer) {
	if !s.isAdmin(peer) {
		s.reject(peer, "notes", "")
		return
	}
	line := protocol.FirstLine(msg)
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	h, ok := s.eng.Host(strings.ToLower(f[1]))
	if !ok {
		return
	}
	h.Notes = strings.TrimLeft(msg[len(line):], "\r\n")
	_ = s.eng.SaveHost(ctx, h)
}

// disable HOST.TEST DURATION message / enable HOST.TEST
func (s *Server) handleEnableDisable(ctx context.Context, kind, msg string, peer Peer) {
	if !s.isAdmin(peer) {
		s.reject(peer, kind, "")
		return
	}
	line := protocol.FirstLine(msg)
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	host, test, err := protocol.SplitHostTest(f[1])
	if err != nil {
		return
	}
	if kind == "enable" {
		_, _ = s.eng.Enable(ctx, host, test)
		return
	}
	if len(f) < 3 {
		return
	}
	dur, err := protocol.ParseDuration(f[2])
	if err != nil {
		return
	}
	text := strings.TrimSpace(strings.Join(f[3:], " ") + "\n" + strings.TrimLeft(msg[len(line):], "\r\n"))
	_, _ = s.eng.Disable(ctx, host, test, dur, text)
}

func (s *Server) handleDrop(ctx context.Context, msg string, peer Peer) {
	if !s.isAdmin(peer) {
		s.reject(peer, "drop", "")
		return
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) < 2 {
		return
	}
	test := ""
	if len(f) > 2 {
		test = f[2]
	}
	_ = s.eng.Drop(ctx, strings.ToLower(f[1]), test)
}

func (s *Server) handleRename(ctx context.Context, msg string, peer Peer) {
	if !s.isAdmin(peer) {
		s.reject(peer, "rename", "")
		return
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) == 3 {
		_ = s.eng.RenameHost(ctx, f[1], f[2])
	}
}

// xymondack COOKIE DURATION message  |  ack ack_event COOKIE DURATION message
func (s *Server) handleAck(ctx context.Context, msg string, peer Peer) {
	if !s.isAdmin(peer) {
		s.reject(peer, "ack", "")
		return
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) > 1 && f[0] == "ack" && f[1] == "ack_event" {
		f = f[1:]
	}
	if len(f) < 3 {
		return
	}
	cookie, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return
	}
	dur, err := protocol.ParseDuration(f[2])
	if err != nil {
		return
	}
	_, _ = s.eng.Ack(ctx, cookie, "", "", dur, strings.Join(f[3:], " "), peer.String())
}

func (s *Server) handleQuery(msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "query", "")
		return ""
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) < 2 {
		return ""
	}
	host, test, err := protocol.SplitHostTest(f[1])
	if err != nil {
		return ""
	}
	st, ok := s.eng.Status(host, test)
	if !ok {
		return ""
	}
	return st.Color.String() + " " + st.Summary + "\n"
}

var defaultBoardFields = []string{"hostname", "testname", "color", "flags", "lastchange", "logtime", "validtime",
	"acktime", "disabletime", "sender", "cookie", "line1"}

type boardFilter struct {
	host, test, page, ip *regexp.Regexp
	colors               map[string]bool
	fields               []string
}

func parseBoardArgs(args []string) (*boardFilter, error) {
	bf := &boardFilter{fields: defaultBoardFields}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			continue
		}
		var err error
		switch strings.ToLower(k) {
		case "host":
			bf.host, err = regexp.Compile("^(?:" + v + ")$")
		case "test":
			bf.test, err = regexp.Compile("^(?:" + v + ")$")
		case "page":
			bf.page, err = regexp.Compile(v)
		case "ip":
			bf.ip, err = regexp.Compile(v)
		case "color":
			bf.colors = map[string]bool{}
			for _, c := range strings.Split(v, ",") {
				bf.colors[c] = true
			}
		case "fields":
			bf.fields = strings.Split(v, ",")
		}
		if err != nil {
			return nil, err
		}
	}
	return bf, nil
}

func unix(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "0"
	}
	if t.Year() >= 9999 {
		return "-1"
	}
	return strconv.FormatInt(t.Unix(), 10)
}

func (s *Server) field(st *model.Status, h *model.Host, f string) string {
	switch f {
	case "hostname":
		return st.Host
	case "testname":
		return st.Test
	case "color":
		return st.Color.String()
	case "flags":
		flags := ""
		if st.Acked(time.Now()) {
			flags += "A"
		}
		if st.Disabled(time.Now()) {
			flags += "D"
		}
		return flags
	case "lastchange":
		return unix(&st.LastChange)
	case "logtime":
		return unix(&st.LastUpdate)
	case "validtime":
		return unix(&st.ValidUntil)
	case "acktime":
		return unix(st.AckUntil)
	case "disabletime":
		return unix(st.DisabledUntil)
	case "sender":
		return st.Sender
	case "cookie":
		if st.Cookie == 0 {
			return "-1"
		}
		return strconv.FormatInt(st.Cookie, 10)
	case "line1":
		return protocol.NLEncode(st.Summary)
	case "ackmsg":
		return protocol.NLEncode(st.AckMsg)
	case "dismsg":
		return protocol.NLEncode(st.DisableMsg)
	case "msg":
		return protocol.NLEncode(st.Message)
	case "transport":
		return st.Transport
	case "client":
		return "N"
	case "ip", "XMH_IP":
		if h != nil {
			return h.IP
		}
	case "XMH_HOSTNAME":
		return st.Host
	case "XMH_PAGENAME", "XMH_PAGEPATH":
		if h != nil {
			return h.Page
		}
	case "XMH_OS":
		if h != nil {
			return h.OS
		}
	case "XMH_CLASS":
		if h != nil {
			return h.Class
		}
	case "XMH_RAW":
		if h != nil {
			return hostscfg.HostInfoLine(h)
		}
	}
	return ""
}

func (s *Server) selectStatuses(bf *boardFilter) []*model.Status {
	hosts := map[string]*model.Host{}
	for _, h := range s.eng.Hosts() {
		hosts[h.Name] = h
	}
	return s.eng.Statuses(func(st *model.Status) bool {
		if bf.host != nil && !bf.host.MatchString(st.Host) {
			return false
		}
		if bf.test != nil && !bf.test.MatchString(st.Test) {
			return false
		}
		if bf.colors != nil && !bf.colors[st.Color.String()] {
			return false
		}
		h := hosts[st.Host]
		if bf.page != nil && (h == nil || !bf.page.MatchString(h.Page)) {
			return false
		}
		if bf.ip != nil && (h == nil || !bf.ip.MatchString(h.IP)) {
			return false
		}
		return true
	}, true)
}

func (s *Server) handleBoard(msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "xymondboard", "")
		return ""
	}
	bf, err := parseBoardArgs(strings.Fields(protocol.FirstLine(msg))[1:])
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, st := range s.selectStatuses(bf) {
		h, _ := s.eng.Host(st.Host)
		vals := make([]string, len(bf.fields))
		for i, f := range bf.fields {
			vals[i] = s.field(st, h, f)
		}
		sb.WriteString(strings.Join(vals, "|"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// xymondlog HOST.TEST
func (s *Server) handleLog(msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "xymondlog", "")
		return ""
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) < 2 {
		return ""
	}
	host, test, err := protocol.SplitHostTest(f[1])
	if err != nil {
		return ""
	}
	st, ok := s.eng.Status(host, test)
	if !ok {
		return ""
	}
	h, _ := s.eng.Host(host)
	if len(f) > 2 && strings.HasPrefix(f[2], "fields=") {
		bf, _ := parseBoardArgs(f[2:])
		vals := make([]string, len(bf.fields))
		for i, fl := range bf.fields {
			vals[i] = s.field(st, h, fl)
		}
		return strings.Join(vals, "|") + "\n"
	}
	hdr := []string{"hostname", "testname", "color", "flags", "lastchange", "logtime", "validtime", "acktime",
		"disabletime", "sender", "cookie", "ackmsg", "dismsg", "client"}
	vals := make([]string, len(hdr))
	for i, fl := range hdr {
		vals[i] = s.field(st, h, fl)
	}
	return strings.Join(vals, "|") + "\n" + st.Color.String() + " " + st.Message
}

// hostinfo [host=PATTERN]
func (s *Server) handleHostInfo(msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "hostinfo", "")
		return ""
	}
	var re *regexp.Regexp
	for _, a := range strings.Fields(protocol.FirstLine(msg))[1:] {
		if v, ok := strings.CutPrefix(a, "host="); ok {
			re, _ = regexp.Compile("^(?:" + v + ")$")
		}
	}
	var lines []string
	for _, h := range s.eng.Hosts() {
		if re == nil || re.MatchString(h.Name) {
			lines = append(lines, hostscfg.HostInfoLine(h))
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// config FILENAME
func (s *Server) handleConfig(msg string, peer Peer) string {
	if !s.mayQuery(peer) {
		s.reject(peer, "config", "")
		return ""
	}
	f := strings.Fields(protocol.FirstLine(msg))
	if len(f) < 2 {
		return ""
	}
	switch f[1] {
	case "hosts.cfg", "bb-hosts":
		titles, _ := s.store.PageTitles(context.Background())
		return hostscfg.Format(s.eng.Hosts(), titles)
	case cfgfiles.AnalysisCfg, cfgfiles.ClientLocalCfg:
		c, _ := s.cfgs.Get(f[1])
		return c
	}
	return ""
}
