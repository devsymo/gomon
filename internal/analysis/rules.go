// Package analysis turns Xymon client messages into status columns
// (cpu, disk, inode, memory, procs, ports, msgs) using rules written in
// Xymon's analysis.cfg format, and extracts metrics for graphing.
package analysis

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gomon/internal/protocol"
)

// DefaultAnalysisCfg mirrors the DEFAULT section shipped with Xymon.
const DefaultAnalysisCfg = `# analysis.cfg - thresholds for client data (Xymon format).
#
# Host-specific sections come first; a section starts with one or more
# selectors: HOST=, EXHOST=, PAGE=, EXPAGE=, CLASS=, EXCLASS=
# Values are comma-separated names, or a regex prefixed with %.
#
# HOST=%^db.*
#	DISK    /var/lib/postgresql 85 95
#	PROC    postgres 1
#	PORT    LOCAL=%[.:]5432$ STATE=LISTEN TEXT=postgres
#	LOG     /var/log/messages %(?i)error COLOR=yellow

DEFAULT
	UP      1h
	LOAD    5.0 10.0
	DISK    * 90 95
	INODE   * 70 90
	MEMPHYS 100 101
	MEMSWAP 50 80
	MEMACT  90 97
`

// Matcher matches a name against a comma list or %regex.
type Matcher struct {
	any bool
	set map[string]bool
	re  *regexp.Regexp
	Raw string
}

func NewMatcher(s string) (*Matcher, error) {
	m := &Matcher{Raw: s}
	switch {
	case s == "*" || s == "":
		m.any = true
	case strings.HasPrefix(s, "%"):
		re, err := regexp.Compile(s[1:])
		if err != nil {
			return nil, err
		}
		m.re = re
	default:
		m.set = map[string]bool{}
		for _, p := range strings.Split(s, ",") {
			m.set[strings.TrimSpace(p)] = true
		}
	}
	return m, nil
}

func (m *Matcher) Match(s string) bool {
	if m == nil || m.any {
		return true
	}
	if m.re != nil {
		return m.re.MatchString(s)
	}
	return m.set[s]
}

// Contains is used for process/log patterns: substring or regex search.
func (m *Matcher) Contains(s string) bool {
	if m == nil || m.any {
		return true
	}
	if m.re != nil {
		return m.re.MatchString(s)
	}
	for p := range m.set {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

type selector struct {
	key  string
	m    *Matcher
	excl bool
}

// HostInfo is what selectors are evaluated against.
type HostInfo struct {
	Name  string
	Page  string
	Class string
	OS    string
}

type Rule struct {
	Kind  string // UP LOAD DISK INODE MEMPHYS MEMSWAP MEMACT PROC PORT LOG
	Line  int
	sels  []selector
	Def   bool
	Args  []string
	KV    map[string]string
	Match *Matcher // DISK/INODE fs, PROC pattern, LOG file
	Warn  float64
	Panic float64
	Ign   bool
	Min   int
	Max   int
	Color protocol.Color
	Text  string
	// LOG
	Pattern *Matcher
	Ignore  *Matcher
	// PORT
	Local, Remote, State *Matcher
	// UP
	BootLimit, TooLong time.Duration
}

type Rules struct {
	rules []*Rule
}

func (r *Rule) applies(h HostInfo) bool {
	for _, s := range r.sels {
		var v string
		switch s.key {
		case "HOST":
			v = h.Name
		case "PAGE":
			v = h.Page
		case "CLASS":
			v = h.Class
		case "OS":
			v = h.OS
		default:
			continue
		}
		hit := s.m.Match(v)
		if s.key == "CLASS" && !hit {
			hit = s.m.Match(h.OS)
		}
		if s.excl == hit {
			return false
		}
	}
	return true
}

var selectorKeys = map[string]bool{"HOST": true, "EXHOST": true, "PAGE": true, "EXPAGE": true, "CLASS": true, "EXCLASS": true, "OS": true, "EXOS": true, "DISPLAYGROUP": true, "TIME": true, "EXTIME": true}

// tokenize splits a line on whitespace honouring double quotes.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	has := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			has = true
		case (r == ' ' || r == '\t') && !inQ:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

func splitKV(tok string) (string, string, bool) {
	i := strings.IndexByte(tok, '=')
	if i <= 0 {
		return "", "", false
	}
	return strings.ToUpper(tok[:i]), tok[i+1:], true
}

// Parse parses analysis.cfg content. Errors name the offending line.
func Parse(content string) (*Rules, error) {
	rs := &Rules{}
	var group []selector
	isDefault := false
	inHeader := false // last line was a selector line
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	ln := 0
	var errs []string
	for sc.Scan() {
		ln++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i == 0 {
			continue
		}
		if line == "" {
			continue
		}
		toks := tokenize(line)
		if len(toks) == 0 {
			continue
		}
		if strings.EqualFold(toks[0], "DEFAULT") {
			group, isDefault, inHeader = nil, true, true
			continue
		}
		// Selector-only line?
		allSel := true
		for _, t := range toks {
			k, _, ok := splitKV(t)
			if !ok || !selectorKeys[k] {
				allSel = false
				break
			}
		}
		if allSel {
			if !inHeader {
				group = nil
			}
			isDefault = false
			for _, t := range toks {
				k, v, _ := splitKV(t)
				s, err := mkSelector(k, v)
				if err != nil {
					errs = append(errs, fmt.Sprintf("line %d: %v", ln, err))
					continue
				}
				if s != nil {
					group = append(group, *s)
				}
			}
			inHeader = true
			continue
		}
		inHeader = false
		r, err := parseRule(toks, ln)
		if err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", ln, err))
			continue
		}
		if r == nil {
			continue
		}
		r.sels = append(append([]selector{}, group...), r.sels...)
		r.Def = isDefault
		rs.rules = append(rs.rules, r)
	}
	if len(errs) > 0 {
		return rs, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return rs, nil
}

func mkSelector(k, v string) (*selector, error) {
	excl := false
	if strings.HasPrefix(k, "EX") {
		excl = true
		k = k[2:]
	}
	switch k {
	case "TIME", "DISPLAYGROUP":
		return nil, nil // accepted, not evaluated
	}
	m, err := NewMatcher(v)
	if err != nil {
		return nil, err
	}
	return &selector{key: k, m: m, excl: excl}, nil
}

func parseRule(toks []string, ln int) (*Rule, error) {
	r := &Rule{Kind: strings.ToUpper(toks[0]), Line: ln, KV: map[string]string{}, Min: -1, Max: -1, Color: -1}
	// separate positional args and KEY=val options
	for _, t := range toks[1:] {
		if k, v, ok := splitKV(t); ok && !strings.HasPrefix(t, "%") {
			if selectorKeys[k] {
				s, err := mkSelector(k, v)
				if err != nil {
					return nil, err
				}
				if s != nil {
					r.sels = append(r.sels, *s)
				}
				continue
			}
			r.KV[k] = v
			continue
		}
		r.Args = append(r.Args, t)
	}
	num := func(i int, def float64) float64 {
		if i < len(r.Args) {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(r.Args[i], "%"), 64); err == nil {
				return v
			}
		}
		return def
	}
	color := func(def protocol.Color) protocol.Color {
		if c, ok := r.KV["COLOR"]; ok {
			if col, ok := protocol.ParseColor(c); ok {
				return col
			}
		}
		return def
	}
	var err error
	switch r.Kind {
	case "LOAD":
		r.Warn, r.Panic = num(0, 5), num(1, 10)
	case "MEMPHYS":
		r.Warn, r.Panic = num(0, 100), num(1, 101)
	case "MEMSWAP":
		r.Warn, r.Panic = num(0, 50), num(1, 80)
	case "MEMACT":
		r.Warn, r.Panic = num(0, 90), num(1, 97)
	case "UP":
		if len(r.Args) > 0 {
			r.BootLimit, err = protocol.ParseDuration(r.Args[0])
			if err != nil {
				return nil, err
			}
		}
		if len(r.Args) > 1 {
			r.TooLong, _ = protocol.ParseDuration(r.Args[1])
		}
		def := protocol.Yellow
		if len(r.Args) > 2 {
			if c, ok := protocol.ParseColor(r.Args[2]); ok {
				def = c
			}
		}
		r.Color = color(def)
	case "DISK", "INODE":
		if len(r.Args) < 2 {
			return nil, fmt.Errorf("%s needs a filesystem and thresholds", r.Kind)
		}
		if r.Match, err = NewMatcher(r.Args[0]); err != nil {
			return nil, err
		}
		if strings.EqualFold(r.Args[1], "IGNORE") {
			r.Ign = true
		} else {
			r.Warn, r.Panic = num(1, 90), num(2, 95)
		}
	case "PROC":
		if len(r.Args) < 1 {
			return nil, fmt.Errorf("PROC needs a pattern")
		}
		if r.Match, err = NewMatcher(r.Args[0]); err != nil {
			return nil, err
		}
		r.Min, r.Max = 1, -1
		if len(r.Args) > 1 {
			r.Min = int(num(1, 1))
		}
		if len(r.Args) > 2 {
			r.Max = int(num(2, -1))
		}
		if v, ok := r.KV["MIN"]; ok {
			r.Min, _ = strconv.Atoi(v)
		}
		if v, ok := r.KV["MAX"]; ok {
			r.Max, _ = strconv.Atoi(v)
		}
		def := protocol.Red
		if len(r.Args) > 3 {
			if c, ok := protocol.ParseColor(r.Args[3]); ok {
				def = c
			}
		}
		r.Color = color(def)
		r.Text = r.KV["TEXT"]
		if r.Text == "" {
			r.Text = strings.TrimPrefix(r.Args[0], "%")
		}
	case "PORT":
		for k, dst := range map[string]**Matcher{"LOCAL": &r.Local, "REMOTE": &r.Remote, "STATE": &r.State} {
			if v, ok := r.KV[k]; ok {
				if *dst, err = NewMatcher(v); err != nil {
					return nil, err
				}
			}
		}
		r.Min, r.Max = 1, -1
		if v, ok := r.KV["MIN"]; ok {
			r.Min, _ = strconv.Atoi(v)
		}
		if v, ok := r.KV["MAX"]; ok {
			r.Max, _ = strconv.Atoi(v)
		}
		r.Color = color(protocol.Red)
		r.Text = r.KV["TEXT"]
		if r.Text == "" {
			var parts []string
			for _, k := range []string{"LOCAL", "REMOTE", "STATE"} {
				if v, ok := r.KV[k]; ok {
					parts = append(parts, strings.ToLower(k)+"="+v)
				}
			}
			r.Text = strings.Join(parts, " ")
		}
	case "LOG":
		if len(r.Args) < 2 {
			return nil, fmt.Errorf("LOG needs a filename and a pattern")
		}
		if r.Match, err = NewMatcher(r.Args[0]); err != nil {
			return nil, err
		}
		pat := r.Args[1]
		if r.Pattern, err = patternMatcher(pat); err != nil {
			return nil, err
		}
		if v, ok := r.KV["IGNORE"]; ok {
			if r.Ignore, err = patternMatcher(v); err != nil {
				return nil, err
			}
		}
		r.Color = color(protocol.Red)
	case "FILE", "DIR", "LINECOUNT", "SVC", "MQ_QUEUE", "MQ_CHANNEL", "DS", "CLOCK":
		return nil, nil // accepted for compatibility; not evaluated yet
	default:
		return nil, fmt.Errorf("unknown rule %q", r.Kind)
	}
	return r, nil
}

// patternMatcher: LOG patterns are substrings unless prefixed with %.
func patternMatcher(p string) (*Matcher, error) {
	if strings.HasPrefix(p, "%") {
		return NewMatcher(p)
	}
	return &Matcher{set: map[string]bool{p: true}, Raw: p}, nil
}

// For returns the rules applying to a host: host-specific rules first (in
// file order), then DEFAULT rules.
func (rs *Rules) For(h HostInfo) []*Rule {
	var spec, def []*Rule
	for _, r := range rs.rules {
		if !r.applies(h) {
			continue
		}
		if r.Def {
			def = append(def, r)
		} else {
			spec = append(spec, r)
		}
	}
	return append(spec, def...)
}

func first(rules []*Rule, kind string) *Rule {
	for _, r := range rules {
		if r.Kind == kind {
			return r
		}
	}
	return nil
}

func all(rules []*Rule, kind string) []*Rule {
	var out []*Rule
	for _, r := range rules {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}
