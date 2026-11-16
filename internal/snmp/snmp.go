// Package snmp polls devices natively over SNMP v1/v2c/v3 and reports
// "snmp" (system/custom OIDs) and "if" (interfaces) status columns plus
// interface traffic and custom OID metrics.
package snmp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"

	"gomon/internal/engine"
	"gomon/internal/model"
	"gomon/internal/protocol"
	"gomon/internal/store"
)

const (
	oidSysDescr    = ".1.3.6.1.2.1.1.1.0"
	oidSysUpTime   = ".1.3.6.1.2.1.1.3.0"
	oidSysName     = ".1.3.6.1.2.1.1.5.0"
	oidSysLocation = ".1.3.6.1.2.1.1.6.0"

	oidIfDescr       = ".1.3.6.1.2.1.2.2.1.2"
	oidIfAdminStatus = ".1.3.6.1.2.1.2.2.1.7"
	oidIfOperStatus  = ".1.3.6.1.2.1.2.2.1.8"
	oidIfInOctets    = ".1.3.6.1.2.1.2.2.1.10"
	oidIfInErrors    = ".1.3.6.1.2.1.2.2.1.14"
	oidIfOutOctets   = ".1.3.6.1.2.1.2.2.1.16"
	oidIfOutErrors   = ".1.3.6.1.2.1.2.2.1.20"
	oidIfName        = ".1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCInOctets  = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = ".1.3.6.1.2.1.31.1.1.1.10"
	oidIfHighSpeed   = ".1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias       = ".1.3.6.1.2.1.31.1.1.1.18"
)

type counter struct {
	v uint64
	t time.Time
}

type Poller struct {
	eng     *engine.Engine
	store   *store.Store
	log     *slog.Logger
	workers int

	mu      sync.Mutex
	lastRun map[int64]time.Time
	running map[int64]bool
	ctrs    map[string]counter // targetID|key -> last counter
}

func New(eng *engine.Engine, st *store.Store, log *slog.Logger, workers int) *Poller {
	if workers <= 0 {
		workers = 16
	}
	return &Poller{eng: eng, store: st, log: log, workers: workers,
		lastRun: map[int64]time.Time{}, running: map[int64]bool{}, ctrs: map[string]counter{}}
}

func (p *Poller) Run(ctx context.Context) {
	sem := make(chan struct{}, p.workers)
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		targets, err := p.store.ListSNMPTargets(ctx)
		if err != nil && ctx.Err() == nil {
			p.log.Warn("list snmp targets", "err", err)
		}
		now := time.Now()
		for _, tg := range targets {
			if !tg.Enabled {
				continue
			}
			iv := time.Duration(max(tg.IntervalSeconds, 30)) * time.Second
			p.mu.Lock()
			due := !p.running[tg.ID] && now.Sub(p.lastRun[tg.ID]) >= iv
			if due {
				p.running[tg.ID] = true
				p.lastRun[tg.ID] = now
			}
			p.mu.Unlock()
			if !due {
				continue
			}
			sem <- struct{}{}
			go func(tg *model.SNMPTarget) {
				defer func() { <-sem; p.mu.Lock(); delete(p.running, tg.ID); p.mu.Unlock() }()
				p.PollAndReport(ctx, tg)
			}(tg)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func connect(tg *model.SNMPTarget, timeout time.Duration) (*gosnmp.GoSNMP, error) {
	port := tg.Port
	if port == 0 {
		port = 161
	}
	g := &gosnmp.GoSNMP{
		Target:             tg.Address,
		Port:               uint16(port),
		Community:          tg.Community,
		Timeout:            timeout,
		Retries:            1,
		MaxOids:            gosnmp.MaxOids,
		MaxRepetitions:     25,
		ExponentialTimeout: false,
	}
	switch tg.Version {
	case "1":
		g.Version = gosnmp.Version1
	case "3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.ContextName = tg.V3.ContextName
		sp := &gosnmp.UsmSecurityParameters{UserName: tg.V3.Username}
		switch tg.V3.SecurityLevel {
		case "authPriv":
			g.MsgFlags = gosnmp.AuthPriv
		case "authNoPriv":
			g.MsgFlags = gosnmp.AuthNoPriv
		default:
			g.MsgFlags = gosnmp.NoAuthNoPriv
		}
		if g.MsgFlags&gosnmp.AuthNoPriv != 0 {
			sp.AuthenticationPassphrase = tg.V3.AuthPassword
			sp.AuthenticationProtocol = map[string]gosnmp.SnmpV3AuthProtocol{
				"MD5": gosnmp.MD5, "SHA": gosnmp.SHA, "SHA224": gosnmp.SHA224, "SHA256": gosnmp.SHA256,
				"SHA384": gosnmp.SHA384, "SHA512": gosnmp.SHA512}[strings.ToUpper(tg.V3.AuthProtocol)]
			if sp.AuthenticationProtocol == 0 {
				sp.AuthenticationProtocol = gosnmp.SHA
			}
		}
		if g.MsgFlags&gosnmp.AuthPriv == gosnmp.AuthPriv {
			sp.PrivacyPassphrase = tg.V3.PrivPassword
			sp.PrivacyProtocol = map[string]gosnmp.SnmpV3PrivProtocol{
				"DES": gosnmp.DES, "AES": gosnmp.AES, "AES192": gosnmp.AES192, "AES256": gosnmp.AES256,
				"AES192C": gosnmp.AES192C, "AES256C": gosnmp.AES256C}[strings.ToUpper(tg.V3.PrivProtocol)]
			if sp.PrivacyProtocol == 0 {
				sp.PrivacyProtocol = gosnmp.AES
			}
		}
		g.SecurityParameters = sp
	default:
		g.Version = gosnmp.Version2c
	}
	if g.Community == "" && g.Version != gosnmp.Version3 {
		g.Community = "public"
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return g, nil
}

// SystemInfo is the result of a connectivity test.
type SystemInfo struct {
	Name     string        `json:"name"`
	Descr    string        `json:"descr"`
	Location string        `json:"location"`
	Uptime   time.Duration `json:"uptime"`
}

func pduString(v gosnmp.SnmpPDU) string {
	switch b := v.Value.(type) {
	case []byte:
		return strings.TrimSpace(string(b))
	case string:
		return b
	case nil:
		return ""
	default:
		return fmt.Sprint(b)
	}
}

func pduFloat(v gosnmp.SnmpPDU) (float64, bool) {
	switch v.Type {
	case gosnmp.OctetString:
		f, err := strconv.ParseFloat(strings.TrimSpace(pduString(v)), 64)
		return f, err == nil
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return 0, false
	}
	bi := gosnmp.ToBigInt(v.Value)
	if bi == nil {
		return 0, false
	}
	f, _ := new(big.Float).SetInt(bi).Float64()
	return f, true
}

func getSystem(g *gosnmp.GoSNMP) (*SystemInfo, error) {
	res, err := g.Get([]string{oidSysDescr, oidSysUpTime, oidSysName, oidSysLocation})
	if err != nil {
		return nil, err
	}
	si := &SystemInfo{}
	for _, v := range res.Variables {
		switch v.Name {
		case oidSysDescr:
			si.Descr = pduString(v)
		case oidSysName:
			si.Name = pduString(v)
		case oidSysLocation:
			si.Location = pduString(v)
		case oidSysUpTime:
			if f, ok := pduFloat(v); ok {
				si.Uptime = time.Duration(f*10) * time.Millisecond
			}
		}
	}
	if si.Descr == "" && si.Name == "" && si.Uptime == 0 {
		return nil, errors.New("no system MIB data returned")
	}
	return si, nil
}

// Test checks connectivity to a target and returns its system information.
func Test(tg *model.SNMPTarget) (*SystemInfo, error) {
	g, err := connect(tg, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer g.Conn.Close()
	return getSystem(g)
}

type iface struct {
	idx                string
	name, descr, alias string
	admin, oper        int
	inOct, outOct      uint64
	hc                 bool
	inErr, outErr      uint64
	speedMbps          uint64
	inBps, outBps      float64
	haveRate           bool
}

func walk(g *gosnmp.GoSNMP, oid string) ([]gosnmp.SnmpPDU, error) {
	if g.Version == gosnmp.Version1 {
		return g.WalkAll(oid)
	}
	return g.BulkWalkAll(oid)
}

func suffix(name, base string) string { return strings.TrimPrefix(strings.TrimPrefix(name, base), ".") }

func toU64(v gosnmp.SnmpPDU) uint64 {
	bi := gosnmp.ToBigInt(v.Value)
	if bi == nil || !bi.IsUint64() {
		return 0
	}
	return bi.Uint64()
}

func (p *Poller) rate(key string, v uint64, t time.Time) (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, ok := p.ctrs[key]
	p.ctrs[key] = counter{v: v, t: t}
	if !ok || v < prev.v {
		return 0, false
	}
	dt := t.Sub(prev.t).Seconds()
	if dt <= 0 {
		return 0, false
	}
	return float64(v-prev.v) / dt, true
}

func (p *Poller) interfaces(g *gosnmp.GoSNMP, tg *model.SNMPTarget, now time.Time) ([]*iface, error) {
	byIdx := map[string]*iface{}
	get := func(idx string) *iface {
		if i, ok := byIdx[idx]; ok {
			return i
		}
		i := &iface{idx: idx}
		byIdx[idx] = i
		return i
	}
	descr, err := walk(g, oidIfDescr)
	if err != nil {
		return nil, err
	}
	for _, v := range descr {
		get(suffix(v.Name, oidIfDescr)).descr = pduString(v)
	}
	type col struct {
		oid string
		set func(*iface, gosnmp.SnmpPDU)
	}
	cols := []col{
		{oidIfAdminStatus, func(i *iface, v gosnmp.SnmpPDU) { i.admin = int(toU64(v)) }},
		{oidIfOperStatus, func(i *iface, v gosnmp.SnmpPDU) { i.oper = int(toU64(v)) }},
		{oidIfInErrors, func(i *iface, v gosnmp.SnmpPDU) { i.inErr = toU64(v) }},
		{oidIfOutErrors, func(i *iface, v gosnmp.SnmpPDU) { i.outErr = toU64(v) }},
	}
	if g.Version != gosnmp.Version1 {
		cols = append(cols,
			col{oidIfName, func(i *iface, v gosnmp.SnmpPDU) { i.name = pduString(v) }},
			col{oidIfAlias, func(i *iface, v gosnmp.SnmpPDU) { i.alias = pduString(v) }},
			col{oidIfHighSpeed, func(i *iface, v gosnmp.SnmpPDU) { i.speedMbps = toU64(v) }},
			col{oidIfHCInOctets, func(i *iface, v gosnmp.SnmpPDU) { i.inOct, i.hc = toU64(v), true }},
			col{oidIfHCOutOctets, func(i *iface, v gosnmp.SnmpPDU) { i.outOct = toU64(v) }},
		)
	}
	for _, c := range cols {
		vals, err := walk(g, c.oid)
		if err != nil {
			continue // optional columns
		}
		for _, v := range vals {
			if i, ok := byIdx[suffix(v.Name, c.oid)]; ok {
				c.set(i, v)
			}
		}
	}
	// 32-bit fallback
	needIn := false
	for _, i := range byIdx {
		if !i.hc {
			needIn = true
		}
	}
	if needIn {
		for oid, isIn := range map[string]bool{oidIfInOctets: true, oidIfOutOctets: false} {
			vals, err := walk(g, oid)
			if err != nil {
				continue
			}
			for _, v := range vals {
				if i, ok := byIdx[suffix(v.Name, oid)]; ok && !i.hc {
					if isIn {
						i.inOct = toU64(v)
					} else {
						i.outOct = toU64(v)
					}
				}
			}
		}
	}
	out := make([]*iface, 0, len(byIdx))
	for _, i := range byIdx {
		if i.name == "" {
			i.name = i.descr
		}
		base := fmt.Sprintf("%d|%s|", tg.ID, i.idx)
		in, ok1 := p.rate(base+"in", i.inOct, now)
		outr, ok2 := p.rate(base+"out", i.outOct, now)
		if ok1 && ok2 {
			i.inBps, i.outBps, i.haveRate = in*8, outr*8, true
		}
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool {
		x, _ := strconv.Atoi(out[a].idx)
		y, _ := strconv.Atoi(out[b].idx)
		return x < y
	})
	return out, nil
}

func humanBps(v float64) string {
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func (p *Poller) submit(ctx context.Context, host, test string, c protocol.Color, text string, tg *model.SNMPTarget) {
	iv := time.Duration(max(tg.IntervalSeconds, 30)) * time.Second
	if err := p.eng.Submit(ctx, engine.Update{Host: host, Test: test, Color: c, Text: text, Lifetime: 3 * iv,
		Sender: "gomon-snmp", Transport: "snmp v" + tg.Version}); err != nil {
		p.log.Warn("submit snmp status", "host", host, "err", err)
	}
}

// PollAndReport polls one target and submits its statuses.
func (p *Poller) PollAndReport(ctx context.Context, tg *model.SNMPTarget) {
	host, err := p.eng.ResolveHost(ctx, tg.Host, tg.Address)
	if err != nil {
		if _, ok := p.eng.Host(tg.Host); !ok {
			_ = p.eng.SaveHost(ctx, &model.Host{Name: tg.Host, IP: tg.Address})
		}
		host = tg.Host
	}
	now := time.Now()
	stamp := now.Format(time.RFC1123)
	g, err := connect(tg, 5*time.Second)
	if err != nil {
		p.submit(ctx, host, "snmp", protocol.Red, fmt.Sprintf("%s - SNMP NOT ok\n\n&red cannot connect to %s: %v\n", stamp, tg.Address, err), tg)
		return
	}
	defer g.Conn.Close()
	si, err := getSystem(g)
	if err != nil {
		p.submit(ctx, host, "snmp", protocol.Red, fmt.Sprintf("%s - SNMP NOT ok\n\n&red no response from %s:%d (v%s): %v\n", stamp, tg.Address, tg.Port, tg.Version, err), tg)
		return
	}
	p.eng.RecordMetrics(host, "snmp", []protocol.Metric{{Name: "uptime_days", Value: si.Uptime.Hours() / 24}}, now)

	// custom OIDs, grouped by column
	type line struct {
		c    protocol.Color
		text string
	}
	byTest := map[string][]line{}
	if len(tg.OIDs) > 0 {
		oids := make([]string, 0, len(tg.OIDs))
		for _, o := range tg.OIDs {
			oid := o.OID
			if !strings.HasPrefix(oid, ".") {
				oid = "." + oid
			}
			oids = append(oids, oid)
		}
		vals := map[string]gosnmp.SnmpPDU{}
		for start := 0; start < len(oids); start += 20 {
			end := min(start+20, len(oids))
			res, err := g.Get(oids[start:end])
			if err != nil {
				break
			}
			for _, v := range res.Variables {
				vals[v.Name] = v
			}
		}
		for i, o := range tg.OIDs {
			test := o.Test
			if test == "" {
				test = "snmp"
			}
			v, ok := vals[oids[i]]
			f, numeric := 0.0, false
			if ok {
				f, numeric = pduFloat(v)
			}
			if !ok || (!numeric && v.Type != gosnmp.OctetString) {
				byTest[test] = append(byTest[test], line{protocol.Yellow, fmt.Sprintf("&yellow %s (%s): no value", o.Name, o.OID)})
				continue
			}
			if !numeric {
				byTest[test] = append(byTest[test], line{protocol.Green, fmt.Sprintf("&green %s: %s", o.Name, pduString(v))})
				continue
			}
			if o.Rate {
				r, ok := p.rate(fmt.Sprintf("%d|oid|%s", tg.ID, o.OID), uint64(f), now)
				if !ok {
					byTest[test] = append(byTest[test], line{protocol.Green, fmt.Sprintf("&green %s: collecting rate...", o.Name)})
					continue
				}
				f = r
			}
			c := protocol.Green
			cmp := func(th float64) bool {
				if o.Below {
					return f <= th
				}
				return f >= th
			}
			if o.Crit != nil && cmp(*o.Crit) {
				c = protocol.Red
			} else if o.Warn != nil && cmp(*o.Warn) {
				c = protocol.Yellow
			}
			byTest[test] = append(byTest[test], line{c, fmt.Sprintf("&%s %s : %g", c, o.Name, f)})
			p.eng.RecordMetrics(host, test, []protocol.Metric{{Name: o.Name, Value: f}}, now)
		}
	}

	snmpColor := protocol.Green
	var sb strings.Builder
	for _, l := range byTest["snmp"] {
		snmpColor = protocol.Worse(snmpColor, l.c)
	}
	summary := "SNMP OK"
	if snmpColor != protocol.Green {
		summary = "SNMP values NOT ok"
	}
	fmt.Fprintf(&sb, "%s - %s\n\nName: %s\nDescription: %s\nLocation: %s\nUptime: %s\nAgent: %s:%d (SNMP v%s)\n",
		stamp, summary, si.Name, si.Descr, si.Location, si.Uptime.Truncate(time.Second), tg.Address, tg.Port, tg.Version)
	if ls := byTest["snmp"]; len(ls) > 0 {
		sb.WriteString("\n")
		for _, l := range ls {
			sb.WriteString(l.text + "\n")
		}
	}
	p.submit(ctx, host, "snmp", snmpColor, sb.String(), tg)
	for test, ls := range byTest {
		if test == "snmp" {
			continue
		}
		c := protocol.Green
		var tb strings.Builder
		for _, l := range ls {
			c = protocol.Worse(c, l.c)
			tb.WriteString(l.text + "\n")
		}
		p.submit(ctx, host, test, c, fmt.Sprintf("%s - %s\n\n%s", stamp, map[bool]string{true: "OK", false: "NOT ok"}[c == protocol.Green], tb.String()), tg)
	}

	if !tg.PollInterfaces {
		return
	}
	ifs, err := p.interfaces(g, tg, now)
	if err != nil {
		p.submit(ctx, host, "if", protocol.Yellow, fmt.Sprintf("%s - interface table unavailable\n\n&yellow %v\n", stamp, err), tg)
		return
	}
	ifColor := protocol.Green
	var ib strings.Builder
	var down int
	fmt.Fprintf(&ib, "       %-24s %-6s %-6s %12s %12s %10s %s\n", "Interface", "Admin", "Oper", "In", "Out", "Errors", "Alias")
	var ms []protocol.Metric
	for _, i := range ifs {
		st := func(v int) string {
			switch v {
			case 1:
				return "up"
			case 2:
				return "down"
			case 3:
				return "test"
			case 5:
				return "dormant"
			case 7:
				return "lldown"
			}
			return strconv.Itoa(v)
		}
		mark := "&green"
		if i.admin == 2 {
			mark = "&clear"
		} else if i.oper != 1 && i.oper != 5 {
			// Only interfaces with a description are considered important.
			if i.alias != "" {
				mark = "&yellow"
				ifColor = protocol.Worse(ifColor, protocol.Yellow)
			} else {
				mark = "&clear"
			}
			down++
		}
		in, out := "-", "-"
		if i.haveRate {
			in, out = humanBps(i.inBps), humanBps(i.outBps)
			ms = append(ms, protocol.Metric{Name: i.name + ".in_bps", Value: i.inBps}, protocol.Metric{Name: i.name + ".out_bps", Value: i.outBps})
		}
		fmt.Fprintf(&ib, "%s %-24s %-6s %-6s %12s %12s %10d %s\n", mark, i.name, st(i.admin), st(i.oper), in, out, i.inErr+i.outErr, i.alias)
	}
	if len(ms) > 0 {
		p.eng.RecordMetrics(host, "if", ms, now)
	}
	summary = fmt.Sprintf("%d interfaces, %d down", len(ifs), down)
	p.submit(ctx, host, "if", ifColor, fmt.Sprintf("%s - %s\n\n%s", stamp, summary, ib.String()), tg)
}
