package analysis

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gomon/internal/protocol"
)

// Report is one status column derived from client data.
type Report struct {
	Test  string
	Color protocol.Color
	Text  string
}

// Result of analysing a client message.
type Result struct {
	Reports []Report
	Metrics map[string][]protocol.Metric // keyed by test
}

func (r *Result) add(test string, c protocol.Color, text string) {
	r.Reports = append(r.Reports, Report{Test: test, Color: c, Text: text})
}

func (r *Result) metric(test, name string, v float64) {
	if r.Metrics == nil {
		r.Metrics = map[string][]protocol.Metric{}
	}
	r.Metrics[test] = append(r.Metrics[test], protocol.Metric{Name: name, Value: v})
}

func isWindows(os string) bool {
	return strings.HasPrefix(os, "win") || os == "powershell" || os == "bbwin"
}

// Analyze evaluates a client message against the rules.
func Analyze(c *protocol.Client, rules *Rules, h HostInfo) *Result {
	if rules == nil {
		rules, _ = Parse(DefaultAnalysisCfg)
	}
	if h.OS == "" {
		h.OS = c.OS
	}
	if h.Class == "" {
		h.Class = c.Class
	}
	rs := rules.For(h)
	res := &Result{}
	date, _ := c.Get("date")
	date = strings.TrimSpace(protocol.FirstLine(date))
	if date == "" {
		date = time.Now().Format("Mon Jan 2 15:04:05 MST 2006")
	}

	// Collector messages (client/COLLECTOR) only carry their own sections.
	if c.Collector != "" {
		return res
	}

	cpuReport(c, rs, date, res)
	diskReport(c, rs, date, "disk", "DISK", res)
	diskReport(c, rs, date, "inode", "INODE", res)
	memoryReport(c, rs, date, res)
	procsReport(c, rs, date, res)
	portsReport(c, rs, date, res)
	msgsReport(c, rs, date, res)
	vmstatMetrics(c, res)
	return res
}

// ---- cpu ----

var (
	reLoadAvg  = regexp.MustCompile(`load averages?:\s*([0-9.,]+)[,\s]+([0-9.,]+)[,\s]+([0-9.,]+)`)
	reUsers    = regexp.MustCompile(`(\d+)\s+users?`)
	reUpDays   = regexp.MustCompile(`up\s+(\d+)\s+days?`)
	reUpHHMM   = regexp.MustCompile(`(?:up|days?,)\s+(\d+):(\d+)`)
	reUpMin    = regexp.MustCompile(`(\d+)\s+min`)
	reUpHrs    = regexp.MustCompile(`(\d+)\s+(?:hr|hour)s?`)
	reWinLoad  = regexp.MustCompile(`load=(\d+(?:\.\d+)?)`)
	reWinUpSec = regexp.MustCompile(`(?i)sec:\s*(\d+)`)
)

func parseFloatComma(s string) float64 {
	v, _ := strconv.ParseFloat(strings.Replace(strings.TrimRight(s, ","), ",", ".", 1), 64)
	return v
}

// parseUptime returns the uptime from a Unix "uptime" line.
func parseUptime(s string) (time.Duration, bool) {
	i := strings.Index(s, " up ")
	if i < 0 {
		if !strings.HasPrefix(strings.TrimSpace(s), "up ") {
			return 0, false
		}
		i = strings.Index(s, "up ") - 1
	}
	seg := s[i+1:]
	if j := reUsers.FindStringIndex(seg); j != nil {
		seg = seg[:j[0]]
	} else if j := strings.Index(seg, "load"); j > 0 {
		seg = seg[:j]
	}
	var d time.Duration
	if m := reUpDays.FindStringSubmatch(seg); m != nil {
		n, _ := strconv.Atoi(m[1])
		d += time.Duration(n) * 24 * time.Hour
	}
	if m := reUpHHMM.FindStringSubmatch(seg); m != nil {
		hh, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		d += time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute
	} else {
		if m := reUpHrs.FindStringSubmatch(seg); m != nil {
			n, _ := strconv.Atoi(m[1])
			d += time.Duration(n) * time.Hour
		}
		if m := reUpMin.FindStringSubmatch(seg); m != nil {
			n, _ := strconv.Atoi(m[1])
			d += time.Duration(n) * time.Minute
		}
	}
	return d, true
}

func fmtUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d:%02d", hours, mins)
}

func countPS(c *protocol.Client) int {
	ps, ok := c.Get("ps")
	if !ok {
		ps, ok = c.Get("procs")
	}
	if !ok {
		return -1
	}
	n := 0
	for i, ln := range strings.Split(ps, "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" {
			continue
		}
		n++
	}
	return n
}

func cpuReport(c *protocol.Client, rs []*Rule, date string, res *Result) {
	uptimeStr, hasUp := c.Get("uptime")
	win := isWindows(c.OS)
	var load float64
	haveLoad := false
	var up time.Duration
	haveUp := false
	users := -1
	if win {
		if cpu, ok := c.Get("cpu"); ok {
			if m := reWinLoad.FindStringSubmatch(cpu); m != nil {
				load, haveLoad = parseFloatComma(m[1]), true
			}
		}
		if hasUp {
			if m := reWinUpSec.FindStringSubmatch(uptimeStr); m != nil {
				n, _ := strconv.Atoi(m[1])
				up, haveUp = time.Duration(n)*time.Second, true
			}
		}
	} else if hasUp {
		if m := reLoadAvg.FindStringSubmatch(uptimeStr); m != nil {
			l1, l5, l15 := parseFloatComma(m[1]), parseFloatComma(m[2]), parseFloatComma(m[3])
			load, haveLoad = l5, true
			res.metric("cpu", "load1", l1)
			res.metric("cpu", "load5", l5)
			res.metric("cpu", "load15", l15)
		}
		up, haveUp = parseUptime(uptimeStr)
		if m := reUsers.FindStringSubmatch(uptimeStr); m != nil {
			users, _ = strconv.Atoi(m[1])
		}
	}
	if !haveLoad && !haveUp {
		return
	}
	color := protocol.Green
	var notes []string
	if haveLoad {
		if win {
			res.metric("cpu", "cpu_pct", load)
		}
		r := first(rs, "LOAD")
		warn, panic := 5.0, 10.0
		if r != nil {
			warn, panic = r.Warn, r.Panic
		}
		if win && (r == nil || r.Def) && warn < 20 {
			warn, panic = 80, 95 // Windows reports CPU %, not load average
		}
		switch {
		case load >= panic:
			color = protocol.Red
			notes = append(notes, fmt.Sprintf("&red Load is CRITICAL (%.2f >= %.2f)", load, panic))
		case load >= warn:
			color = protocol.Yellow
			notes = append(notes, fmt.Sprintf("&yellow Load is HIGH (%.2f >= %.2f)", load, warn))
		}
	}
	if haveUp {
		res.metric("cpu", "uptime_days", up.Hours()/24)
		if r := first(rs, "UP"); r != nil {
			if r.BootLimit > 0 && up < r.BootLimit {
				color = protocol.Worse(color, r.Color)
				notes = append(notes, fmt.Sprintf("&%s Machine recently rebooted", r.Color))
			}
			if r.TooLong > 0 && up > r.TooLong {
				color = protocol.Worse(color, r.Color)
				notes = append(notes, fmt.Sprintf("&%s Machine has been up more than %s", r.Color, fmtUptime(r.TooLong)))
			}
		}
	}
	nprocs := countPS(c)
	if nprocs >= 0 {
		res.metric("cpu", "procs", float64(nprocs))
	}
	var sb strings.Builder
	sb.WriteString(date)
	sb.WriteString(" up: ")
	if haveUp {
		sb.WriteString(fmtUptime(up))
	} else {
		sb.WriteString("?")
	}
	if users >= 0 {
		fmt.Fprintf(&sb, ", %d users", users)
	}
	if nprocs >= 0 {
		fmt.Fprintf(&sb, ", %d procs", nprocs)
	}
	if haveLoad {
		if win {
			fmt.Fprintf(&sb, ", load=%.0f%%", load)
		} else {
			fmt.Fprintf(&sb, ", load=%.2f", load)
		}
	}
	sb.WriteString("\n")
	if len(notes) > 0 {
		sb.WriteString("\n" + strings.Join(notes, "\n") + "\n")
	}
	if top, ok := c.Get("top"); ok && strings.TrimSpace(top) != "" {
		sb.WriteString("\n" + top + "\n")
	} else if hasUp {
		sb.WriteString("\n" + strings.TrimSpace(uptimeStr) + "\n")
	}
	res.add("cpu", color, sb.String())
}

// ---- disk / inode ----

type fsRow struct {
	mount string
	pct   float64
}

// parseDF parses df-style output using the header to locate the capacity
// and mount point columns. It copes with wrapped lines.
func parseDF(text string, pctCols []string, mountCols []string) []fsRow {
	lines := strings.Split(text, "\n")
	var hdr []string
	pctIdx, mntIdx := -1, -1
	var out []fsRow
	var carry []string
	for _, ln := range lines {
		f := strings.Fields(ln)
		if len(f) == 0 {
			continue
		}
		if pctIdx < 0 {
			for i, h := range f {
				for _, p := range pctCols {
					if strings.EqualFold(h, p) {
						pctIdx = i
					}
				}
				for _, m := range mountCols {
					if strings.EqualFold(h, m) && mntIdx < 0 {
						mntIdx = i
					}
				}
			}
			if pctIdx >= 0 {
				hdr = f
			}
			continue
		}
		if len(carry) > 0 {
			f = append(carry, f...)
			carry = nil
		}
		if len(f) <= pctIdx {
			carry = f // wrapped filesystem name
			continue
		}
		pctStr := strings.TrimSuffix(f[pctIdx], "%")
		pct, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			continue
		}
		mount := ""
		if mntIdx >= 0 && mntIdx < len(f) {
			mount = strings.Join(f[mntIdx:], " ")
		} else {
			mount = f[len(f)-1]
		}
		if mntIdx == 0 { // Windows: Filesystem column is the mount
			mount = f[0]
		}
		out = append(out, fsRow{mount: mount, pct: pct})
	}
	_ = hdr
	return out
}

func diskReport(c *protocol.Client, rs []*Rule, date, test, kind string, res *Result) {
	section := "df"
	pctCols := []string{"Capacity", "Use%"}
	mountCols := []string{"Mounted"}
	if kind == "INODE" {
		section = "inode"
		pctCols = []string{"IUse%", "%iused", "iuse%"}
	}
	text, ok := c.Get(section)
	if !ok && kind == "DISK" {
		text, ok = c.Get("disk") // Windows clients
		mountCols = []string{"Filesystem"}
	}
	if !ok || strings.TrimSpace(text) == "" {
		return
	}
	rows := parseDF(text, pctCols, mountCols)
	if len(rows) == 0 {
		return
	}
	rules := all(rs, kind)
	color := protocol.Green
	var notes []string
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].mount < rows[j].mount })
	for _, fs := range rows {
		warn, panic := 90.0, 95.0
		if kind == "INODE" {
			warn, panic = 70, 90
		}
		ignored := false
		for _, r := range rules {
			if r.Match.Match(fs.mount) {
				if r.Ign {
					ignored = true
				}
				warn, panic = r.Warn, r.Panic
				break
			}
		}
		if ignored {
			continue
		}
		res.metric(test, fs.mount, fs.pct)
		switch {
		case fs.pct >= panic:
			color = protocol.Red
			notes = append(notes, fmt.Sprintf("&red %s (%.0f%% used) has reached the PANIC level (%.0f%%)", fs.mount, fs.pct, panic))
		case fs.pct >= warn:
			color = protocol.Worse(color, protocol.Yellow)
			notes = append(notes, fmt.Sprintf("&yellow %s (%.0f%% used) has reached the WARNING level (%.0f%%)", fs.mount, fs.pct, warn))
		}
	}
	label := "Filesystems"
	if kind == "INODE" {
		label = "Inodes"
	}
	summary := label + " OK"
	if color != protocol.Green {
		summary = label + " NOT ok"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s - %s\n", date, summary)
	if len(notes) > 0 {
		sb.WriteString("\n" + strings.Join(notes, "\n") + "\n")
	}
	sb.WriteString("\n" + text + "\n")
	res.add(test, color, sb.String())
}

// ---- memory ----

type memInfo struct {
	physTotal, physUsed int64 // MB
	actTotal, actUsed   int64
	swapTotal, swapUsed int64
}

var reSwapUsage = regexp.MustCompile(`total = ([0-9.]+)([KMG]).*used = ([0-9.]+)([KMG])`)

func toMB(v float64, unit string) int64 {
	switch unit {
	case "K":
		return int64(v / 1024)
	case "G":
		return int64(v * 1024)
	}
	return int64(v)
}

func fieldsAfter(text, prefix string) []string {
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, prefix) {
			return strings.Fields(t[len(prefix):])
		}
	}
	return nil
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(s), "."), 10, 64)
	return v
}

func parseMemory(c *protocol.Client) (*memInfo, bool) {
	m := &memInfo{physTotal: -1, actTotal: -1, swapTotal: -1}
	if free, ok := c.Get("free"); ok { // Linux, values in KB
		mem := fieldsAfter(free, "Mem:")
		if len(mem) >= 3 {
			m.physTotal = atoi64(mem[0]) / 1024
			m.physUsed = atoi64(mem[1]) / 1024
			if strings.Contains(free, "available") && len(mem) >= 6 {
				avail := atoi64(mem[5]) / 1024
				m.actTotal = m.physTotal
				m.actUsed = max(m.physTotal-avail, 0)
				m.physUsed += atoi64(mem[4]) / 1024 // used + buff/cache, as xymon does
			} else if bc := fieldsAfter(free, "-/+ buffers/cache:"); len(bc) >= 1 {
				m.actTotal = m.physTotal
				m.actUsed = atoi64(bc[0]) / 1024
			}
		}
		if sw := fieldsAfter(free, "Swap:"); len(sw) >= 2 {
			m.swapTotal = atoi64(sw[0]) / 1024
			m.swapUsed = atoi64(sw[1]) / 1024
		}
		return m, m.physTotal > 0
	}
	if mem, ok := c.Get("memory"); ok { // Windows (BBWin / PowerShell), MB
		for _, p := range []struct {
			key        string
			tot, usedp *int64
		}{{"physical:", &m.physTotal, &m.physUsed}, {"page:", &m.swapTotal, &m.swapUsed}, {"virtual:", &m.actTotal, &m.actUsed}} {
			if f := fieldsAfter(mem, p.key); len(f) >= 2 {
				*p.tot, *p.usedp = atoi64(f[0]), atoi64(f[1])
			}
		}
		return m, m.physTotal > 0
	}
	if vm, ok := c.Get("meminfo"); ok {
		if strings.Contains(vm, "page size of") { // Darwin vm_stat
			var pg int64
			if f := regexp.MustCompile(`page size of (\d+) bytes`).FindStringSubmatch(vm); f != nil {
				pg = atoi64(f[1])
			}
			get := func(k string) int64 {
				if f := fieldsAfter(vm, k); len(f) > 0 {
					return atoi64(f[0])
				}
				return 0
			}
			free, active, inactive := get("Pages free:"), get("Pages active:"), get("Pages inactive:")
			spec, wired, comp := get("Pages speculative:"), get("Pages wired down:"), get("Pages occupied by compressor:")
			if pg > 0 {
				mb := func(p int64) int64 { return p * pg / 1024 / 1024 }
				m.physTotal = mb(free + active + inactive + spec + wired + comp)
				m.physUsed = mb(active + inactive + spec + wired + comp)
				m.actTotal = m.physTotal
				m.actUsed = mb(active + wired + comp)
			}
			if sw, ok := c.Get("swapusage"); ok {
				if f := reSwapUsage.FindStringSubmatch(sw); f != nil {
					t, _ := strconv.ParseFloat(f[1], 64)
					u, _ := strconv.ParseFloat(f[3], 64)
					m.swapTotal, m.swapUsed = toMB(t, f[2]), toMB(u, f[4])
				}
			}
			return m, m.physTotal > 0
		}
		// FreeBSD freebsd-meminfo: "Total:NNN\nFree:NNN" (MB)
		var total, free, actual int64 = -1, -1, -1
		for _, ln := range strings.Split(vm, "\n") {
			k, v, ok := strings.Cut(ln, ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "Total":
				total = atoi64(v)
			case "Free":
				free = atoi64(v)
			case "Actual":
				actual = atoi64(v)
			}
		}
		if total > 0 && free >= 0 {
			m.physTotal, m.physUsed = total, total-free
			if actual >= 0 {
				m.actTotal, m.actUsed = total, actual
			}
		}
		if sw, ok := c.Get("swapinfo"); ok {
			lines := strings.Split(strings.TrimSpace(sw), "\n")
			if len(lines) > 1 {
				f := strings.Fields(lines[len(lines)-1])
				if len(f) >= 3 {
					m.swapTotal, m.swapUsed = atoi64(f[1])/1024, atoi64(f[2])/1024
				}
			}
		}
		return m, m.physTotal > 0
	}
	return nil, false
}

func memoryReport(c *protocol.Client, rs []*Rule, date string, res *Result) {
	m, ok := parseMemory(c)
	if !ok {
		return
	}
	color := protocol.Green
	var rows []string
	row := func(label, rule string, used, total int64, defW, defP float64) {
		if total <= 0 {
			return
		}
		pct := float64(used) * 100 / float64(total)
		warn, panic := defW, defP
		if r := first(rs, rule); r != nil {
			warn, panic = r.Warn, r.Panic
		}
		c := protocol.Green
		switch {
		case pct >= panic:
			c = protocol.Red
		case pct >= warn:
			c = protocol.Yellow
		}
		color = protocol.Worse(color, c)
		rows = append(rows, fmt.Sprintf("&%s %-12s %10dM %10dM %10.0f%%", c, label, used, total, pct))
		res.metric("memory", strings.ToLower(label)+"_pct", pct)
		res.metric("memory", strings.ToLower(label)+"_used_mb", float64(used))
	}
	row("Physical", "MEMPHYS", m.physUsed, m.physTotal, 100, 101)
	row("Actual", "MEMACT", m.actUsed, m.actTotal, 90, 97)
	row("Swap", "MEMSWAP", m.swapUsed, m.swapTotal, 50, 80)
	summary := "Memory OK"
	switch color {
	case protocol.Yellow:
		summary = "Memory low"
	case protocol.Red:
		summary = "Memory CRITICAL"
	}
	text := fmt.Sprintf("%s - %s\n\n   %-12s %11s %11s %11s\n%s\n", date, summary, "Memory", "Used", "Total", "Percentage", strings.Join(rows, "\n"))
	res.add("memory", color, text)
}

// ---- procs ----

func psCommands(c *protocol.Client) ([]string, string, bool) {
	ps, ok := c.Get("ps")
	col := []string{"CMD", "COMMAND", "COMMAND_LINE"}
	if !ok {
		ps, ok = c.Get("procs")
		col = []string{"Name", "CommandLine", "CMD"}
	}
	if !ok {
		return nil, "", false
	}
	lines := strings.Split(ps, "\n")
	if len(lines) == 0 {
		return nil, ps, true
	}
	hdr := lines[0]
	start := -1
	for _, cn := range col {
		if i := strings.Index(hdr, cn); i >= 0 && (start < 0 || i < start) {
			start = i
		}
	}
	var cmds []string
	for _, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if start >= 0 && start < len(ln) {
			cmds = append(cmds, ln[start:])
		} else {
			cmds = append(cmds, ln)
		}
	}
	return cmds, ps, true
}

func procsReport(c *protocol.Client, rs []*Rule, date string, res *Result) {
	cmds, raw, ok := psCommands(c)
	if !ok {
		return
	}
	rules := all(rs, "PROC")
	color := protocol.Green
	var lines []string
	for _, r := range rules {
		n := 0
		for _, cmd := range cmds {
			if r.Match.Contains(cmd) {
				n++
			}
		}
		c := protocol.Green
		if n < r.Min || (r.Max >= 0 && n > r.Max) {
			c = r.Color
		}
		color = protocol.Worse(color, c)
		req := fmt.Sprintf("req. %d or more", r.Min)
		if r.Max >= 0 {
			req = fmt.Sprintf("req. between %d and %d", r.Min, r.Max)
		}
		lines = append(lines, fmt.Sprintf("&%s %s (found %d, %s)", c, r.Text, n, req))
		res.metric("procs", sanitize(r.Text), float64(n))
	}
	summary := "All processes OK"
	if len(rules) == 0 {
		summary = "No process checks defined"
	} else if color != protocol.Green {
		summary = "Processes NOT ok"
	}
	text := fmt.Sprintf("%s - %s\n", date, summary)
	if len(lines) > 0 {
		text += "\n" + strings.Join(lines, "\n") + "\n"
	}
	text += "\n" + raw + "\n"
	res.add("procs", color, text)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '/' || r == '|' {
			return '_'
		}
		return r
	}, s)
}

// ---- ports ----

func portsReport(c *protocol.Client, rs []*Rule, date string, res *Result) {
	rules := all(rs, "PORT")
	if len(rules) == 0 {
		return
	}
	raw, ok := c.Get("ports")
	if !ok {
		return
	}
	localIdx, remoteIdx, stateIdx := 3, 4, 5
	if isWindows(c.OS) {
		localIdx, remoteIdx, stateIdx = 1, 2, 3
	}
	type conn struct{ local, remote, state string }
	var conns []conn
	for _, ln := range strings.Split(raw, "\n") {
		f := strings.Fields(ln)
		if len(f) <= remoteIdx {
			continue
		}
		cn := conn{local: f[localIdx], remote: f[remoteIdx]}
		if len(f) > stateIdx {
			cn.state = f[stateIdx]
		}
		conns = append(conns, cn)
	}
	color := protocol.Green
	var lines []string
	for _, r := range rules {
		n := 0
		for _, cn := range conns {
			if (r.Local == nil || r.Local.Contains(cn.local)) && (r.Remote == nil || r.Remote.Contains(cn.remote)) &&
				(r.State == nil || r.State.Match(cn.state)) {
				n++
			}
		}
		c := protocol.Green
		if n < r.Min || (r.Max >= 0 && n > r.Max) {
			c = r.Color
		}
		color = protocol.Worse(color, c)
		lines = append(lines, fmt.Sprintf("&%s %s (found %d, req. %d or more)", c, r.Text, n, r.Min))
	}
	summary := "All ports OK"
	if color != protocol.Green {
		summary = "Ports NOT ok"
	}
	res.add("ports", color, fmt.Sprintf("%s - %s\n\n%s\n\n%s\n", date, summary, strings.Join(lines, "\n"), raw))
}

// ---- msgs (log files) ----

func msgsReport(c *protocol.Client, rs []*Rule, date string, res *Result) {
	secs := append(c.WithPrefix("msgs:"), c.WithPrefix("logfile:")...)
	if s, ok := c.Get("msg"); ok { // BBWin event log
		secs = append(secs, protocol.Section{Name: "msgs:eventlog", Content: s})
	}
	if len(secs) == 0 {
		return
	}
	rules := all(rs, "LOG")
	color := protocol.Green
	var sb strings.Builder
	for _, s := range secs {
		file := s.Name[strings.IndexByte(s.Name, ':')+1:]
		fileColor := protocol.Green
		var hits []string
		for _, ln := range strings.Split(s.Content, "\n") {
			if strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "<...SKIPPED...>") {
				continue
			}
			for _, r := range rules {
				if !r.Match.Match(file) || !r.Pattern.Contains(ln) {
					continue
				}
				if r.Ignore != nil && r.Ignore.Contains(ln) {
					continue
				}
				fileColor = protocol.Worse(fileColor, r.Color)
				hits = append(hits, fmt.Sprintf("&%s %s", r.Color, ln))
				break
			}
		}
		color = protocol.Worse(color, fileColor)
		if len(hits) > 0 {
			fmt.Fprintf(&sb, "\n%s\n%s\n", file, strings.Join(hits, "\n"))
		} else {
			fmt.Fprintf(&sb, "\n&green No entries in %s\n", file)
		}
	}
	summary := "System logs are OK"
	switch color {
	case protocol.Yellow:
		summary = "Warnings in system logs"
	case protocol.Red:
		summary = "Critical entries in system logs"
	}
	res.add("msgs", color, fmt.Sprintf("%s - %s\n%s", date, summary, sb.String()))
}

// ---- vmstat metrics ----

func vmstatMetrics(c *protocol.Client, res *Result) {
	vm, ok := c.Get("vmstat")
	if !ok {
		return
	}
	var hdr []string
	var last []string
	for _, ln := range strings.Split(vm, "\n") {
		f := strings.Fields(ln)
		if len(f) == 0 {
			continue
		}
		if contains(f, "us") && contains(f, "id") {
			hdr = f
			continue
		}
		if hdr != nil && len(f) == len(hdr) {
			if _, err := strconv.ParseFloat(f[0], 64); err == nil {
				last = f
			}
		}
	}
	if hdr == nil || last == nil {
		return
	}
	names := map[string]string{"us": "cpu_user", "sy": "cpu_system", "id": "cpu_idle", "wa": "cpu_wait", "st": "cpu_steal"}
	for i, h := range hdr {
		if n, ok := names[h]; ok {
			if v, err := strconv.ParseFloat(last[i], 64); err == nil {
				res.metric("cpu", n, v)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
