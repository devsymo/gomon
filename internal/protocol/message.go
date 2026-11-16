package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind returns the message keyword, e.g. "status", "combo", "client".
// For "status+30/group:foo" it returns "status".
func Kind(msg string) string {
	end := strings.IndexAny(msg, " \t\r\n")
	if end < 0 {
		end = len(msg)
	}
	kw := msg[:end]
	if i := strings.IndexAny(kw, "+/"); i >= 0 {
		kw = kw[:i]
	}
	return strings.ToLower(kw)
}

// FirstLine returns the first line of s without the line terminator.
func FirstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// SplitHostTest splits "HOST.TEST" at the last dot. Commas in the hostname
// are converted to dots (the Big Brother "www,example,com" convention).
func SplitHostTest(s string) (host, test string, err error) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("invalid HOST.TEST %q", s)
	}
	host = strings.ReplaceAll(s[:i], ",", ".")
	test = s[i+1:]
	return strings.ToLower(host), test, nil
}

// ParseDuration parses a Xymon duration ("30", "30m", "2h", "1d", "1w", "45s").
// A bare number is minutes. "-1" means "until OK" and is returned as -1.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if s == "-1" {
		return -1, nil
	}
	mult := time.Minute
	switch s[len(s)-1] {
	case 's':
		mult = time.Second
		s = s[:len(s)-1]
	case 'm':
		s = s[:len(s)-1]
	case 'h':
		mult = time.Hour
		s = s[:len(s)-1]
	case 'd':
		mult = 24 * time.Hour
		s = s[:len(s)-1]
	case 'w':
		mult = 7 * 24 * time.Hour
		s = s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return time.Duration(n) * mult, nil
}

// Status is a parsed "status" message.
//
//	status[+LIFETIME][/group:GROUP] HOSTNAME.TESTNAME COLOR <text>
type Status struct {
	Host     string
	Test     string
	Color    Color
	Lifetime time.Duration // 0 means server default
	Group    string
	Text     string // everything after the color: first line is the summary
	Sender   string // "Status message received from X" (relayed messages)
}

// Summary returns the first line of the status text.
func (s *Status) Summary() string { return strings.TrimSpace(FirstLine(s.Text)) }

// ParseStatus parses a single status message.
func ParseStatus(msg string) (*Status, error) {
	st := &Status{}

	// Relayed messages carry their original sender.
	if i := strings.Index(msg, "\nStatus message received from "); i >= 0 {
		rest := msg[i+len("\nStatus message received from "):]
		st.Sender = strings.Fields(FirstLine(rest + " "))[0]
		msg = msg[:i+1]
	}

	line := FirstLine(msg)
	rest := ""
	if len(line) < len(msg) {
		rest = strings.TrimLeft(msg[len(line):], "\r")
		rest = strings.TrimPrefix(rest, "\n")
	}

	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil, errors.New("status: too few fields")
	}
	kw := fields[0]
	if !strings.HasPrefix(strings.ToLower(kw), "status") {
		return nil, fmt.Errorf("not a status message: %q", kw)
	}
	mods := kw[len("status"):]
	for mods != "" {
		switch mods[0] {
		case '+':
			end := strings.IndexByte(mods[1:], '/')
			val := mods[1:]
			if end >= 0 {
				val = mods[1 : end+1]
				mods = mods[end+1:]
			} else {
				mods = ""
			}
			d, err := ParseDuration(val)
			if err == nil && d > 0 {
				st.Lifetime = d
			}
		case '/':
			val := mods[1:]
			end := strings.IndexAny(val, "+/")
			if end >= 0 {
				mods = val[end:]
				val = val[:end]
			} else {
				mods = ""
			}
			if strings.HasPrefix(val, "group:") {
				st.Group = strings.TrimPrefix(val, "group:")
			}
		default:
			mods = mods[1:]
		}
	}

	host, test, err := SplitHostTest(fields[1])
	if err != nil {
		return nil, err
	}
	st.Host, st.Test = host, test

	col, ok := ParseColor(fields[2])
	if !ok {
		return nil, fmt.Errorf("status: invalid color %q", fields[2])
	}
	st.Color = col

	// Text: remainder of first line after the color token, plus following lines.
	idx := indexAfterFields(line, 3)
	first := strings.TrimSpace(line[idx:])
	if rest != "" {
		st.Text = first + "\n" + rest
	} else {
		st.Text = first
	}
	return st, nil
}

// indexAfterFields returns the byte offset right after the n-th whitespace
// separated field of s.
func indexAfterFields(s string, n int) int {
	i := 0
	for f := 0; f < n; f++ {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
	}
	return i
}

// SplitCombo splits a "combo" message into its individual status messages.
func SplitCombo(msg string) []string {
	body := strings.TrimPrefix(msg, "combo\n")
	if body == msg {
		body = strings.TrimPrefix(msg, "combo\r\n")
	}
	var out []string
	for {
		i := strings.Index(body, "\n\nstatus")
		if i < 0 {
			if strings.TrimSpace(body) != "" {
				out = append(out, body)
			}
			return out
		}
		out = append(out, body[:i+1])
		body = body[i+2:]
	}
}

// SplitExtCombo splits an "extcombo START END1 END2 ..." message, where the
// offsets are byte positions in the whole message.
func SplitExtCombo(msg string) ([]string, error) {
	nl := strings.IndexByte(msg, '\n')
	if nl < 0 {
		return nil, errors.New("extcombo: no header line")
	}
	fields := strings.Fields(msg[len("extcombo "):nl])
	if len(fields) < 2 {
		return nil, errors.New("extcombo: no offsets")
	}
	start, err := strconv.Atoi(fields[0])
	if err != nil || start <= 0 || start > len(msg) {
		return nil, errors.New("extcombo: invalid start offset")
	}
	var out []string
	for _, f := range fields[1:] {
		end, err := strconv.Atoi(f)
		if err != nil || end <= start || end > len(msg) {
			return nil, errors.New("extcombo: invalid offset")
		}
		out = append(out, msg[start:end])
		start = end
	}
	return out, nil
}

// Client is a parsed "client" message:
//
//	client[/COLLECTORID] HOSTNAME.CLIENTOS [CLIENTCLASS]
//	[section]
//	...
type Client struct {
	Host      string
	OS        string
	Class     string
	Collector string
	Raw       string    // the full original message
	Sections  []Section // in order of appearance
	index     map[string]int
}

// Section is one "[name]" block of client data.
type Section struct {
	Name    string
	Content string
}

// Get returns a section's content by exact name.
func (c *Client) Get(name string) (string, bool) {
	if i, ok := c.index[name]; ok {
		return c.Sections[i].Content, true
	}
	return "", false
}

// WithPrefix returns all sections whose name starts with prefix (e.g. "msgs:").
func (c *Client) WithPrefix(prefix string) []Section {
	var out []Section
	for _, s := range c.Sections {
		if strings.HasPrefix(s.Name, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// ParseClient parses a client message.
func ParseClient(msg string) (*Client, error) {
	line := FirstLine(msg)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, errors.New("client: too few fields")
	}
	c := &Client{Raw: msg, index: map[string]int{}}
	if i := strings.IndexByte(fields[0], '/'); i >= 0 {
		c.Collector = fields[0][i+1:]
	}
	host, os, err := SplitHostTest(fields[1])
	if err != nil {
		return nil, err
	}
	c.Host, c.OS = host, strings.ToLower(os)
	if len(fields) > 2 && c.Collector == "" {
		c.Class = fields[2]
	}
	c.Sections = ParseSections(msg[len(line):])
	for i, s := range c.Sections {
		if _, dup := c.index[s.Name]; !dup {
			c.index[s.Name] = i
		}
	}
	return c, nil
}

// ParseSections splits "[name]\ncontent" blocks.
func ParseSections(body string) []Section {
	var out []Section
	var cur *Section
	var sb strings.Builder
	flush := func() {
		if cur != nil {
			cur.Content = strings.TrimRight(sb.String(), "\n")
			out = append(out, *cur)
		}
		sb.Reset()
	}
	for _, ln := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if len(ln) > 2 && ln[0] == '[' && ln[len(ln)-1] == ']' && !strings.ContainsAny(ln[1:len(ln)-1], "[]") {
			flush()
			cur = &Section{Name: ln[1 : len(ln)-1]}
			continue
		}
		if cur != nil {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
	}
	flush()
	return out
}

// Data is a parsed "data HOST.DATANAME" message.
type Data struct {
	Host string
	Name string
	Body string
}

// ParseData parses a data message.
func ParseData(msg string) (*Data, error) {
	line := FirstLine(msg)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, errors.New("data: too few fields")
	}
	host, name, err := SplitHostTest(fields[1])
	if err != nil {
		return nil, err
	}
	body := strings.TrimLeft(msg[len(line):], "\r\n")
	return &Data{Host: host, Name: name, Body: body}, nil
}

// Metric is one numeric sample extracted from a message.
type Metric struct {
	Name  string
	Value float64
}

// ParseTrends parses the body of a "data HOST.trends" message:
//
//	[filename.rrd]
//	DS:dsname:GAUGE:600:0:U 123
//
// It returns the metrics keyed by the rrd file's base name (used as test).
func ParseTrends(body string) map[string][]Metric {
	out := map[string][]Metric{}
	cur := ""
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if ln[0] == '[' && ln[len(ln)-1] == ']' {
			cur = strings.TrimSuffix(ln[1:len(ln)-1], ".rrd")
			continue
		}
		if cur == "" || !strings.HasPrefix(ln, "DS:") {
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		parts := strings.Split(f[0], ":")
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[len(f)-1], 64)
		if err != nil {
			continue
		}
		out[cur] = append(out[cur], Metric{Name: parts[1], Value: v})
	}
	return out
}

// ParseNCV extracts "Name : value" / "Name = value" pairs from text, the
// same convention xymond_rrd's NCV handler uses for plugin statuses.
func ParseNCV(text string, limit int) []Metric {
	var out []Metric
	for _, ln := range strings.Split(text, "\n") {
		if len(out) >= limit {
			break
		}
		ln = strings.TrimSpace(ln)
		// Strip leading color markers like "&green".
		if strings.HasPrefix(ln, "&") {
			if sp := strings.IndexByte(ln, ' '); sp > 0 {
				ln = strings.TrimSpace(ln[sp:])
			}
		}
		sep := strings.IndexAny(ln, ":=")
		if sep <= 0 {
			continue
		}
		name := strings.TrimSpace(ln[:sep])
		valf := strings.Fields(ln[sep+1:])
		if name == "" || len(valf) == 0 || len(name) > 64 {
			continue
		}
		v, ok := leadingNumber(valf[0])
		if !ok {
			continue
		}
		out = append(out, Metric{Name: sanitizeMetricName(name), Value: v})
	}
	return out
}

func sanitizeMetricName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// NLEncode encodes a message for single-line board output the same way
// xymond does: backslash, pipe, newline, carriage return and tab escaped.
func NLEncode(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "|", "\\p", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return r.Replace(s)
}

// leadingNumber parses the numeric prefix of s ("3.5ms" -> 3.5).
func leadingNumber(s string) (float64, bool) {
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= '0' && c <= '9') || c == '.' || ((c == '-' || c == '+') && end == 0) || ((c == 'e' || c == 'E') && end > 0) {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	return v, err == nil
}
