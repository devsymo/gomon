package analysis

import (
	"strings"
	"testing"
	"time"

	"gomon/internal/protocol"
)

const linuxClient = `client web01.linux linux
[date]
Mon Nov 16 20:00:00 UTC 2026
[uname]
Linux web01 6.1.0 #1 SMP x86_64 GNU/Linux
[uptime]
 20:00:00 up 3 days,  2:03,  2 users,  load average: 0.15, 7.50, 0.05
[df]
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/sda1         10000000  9600000    400000      96% /
/dev/mapper/very-long-volume-name-that-wraps
                  10000000  1000000   9000000      10% /data
tmpfs              1000000        0   1000000       0% /run/user
[inode]
Filesystem      Inodes  IUsed   IFree IUse% Mounted on
/dev/sda1       655360  65536  589824   10% /
[free]
               total        used        free      shared  buff/cache   available
Mem:         8000000     2000000     1000000       10000     5000000     5500000
Swap:        2000000     1500000      500000
[ps]
    PID    PPID USER     STARTED S PRI %CPU     TIME %MEM   RSZ   VSZ CMD
      1       0 root      Nov 13 S  19  0.0 00:00:05  0.1  1234  5678 /sbin/init
    100       1 root      Nov 13 S  19  0.0 00:00:01  0.1  1234  5678 /usr/sbin/sshd -D
    200       1 www       Nov 13 S  19  0.0 00:00:01  0.1  1234  5678 nginx: worker process
[ports]
Active Internet connections (servers and established)
Proto Recv-Q Send-Q Local Address           Foreign Address         State
tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN
tcp        0      0 10.0.0.1:22             10.0.0.9:51234          ESTABLISHED
[vmstat]
procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----
 r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st
 1  0      0 100000  10000 500000    0    0     1     2   10   20  5  2 92  1  0
 0  0      0 100000  10000 500000    0    0     1     2   10   20 10  3 85  2  0
[msgs:/var/log/messages]
Nov 16 19:59:00 web01 kernel: something normal
Nov 16 19:59:30 web01 app: ERROR database unreachable
`

func find(res *Result, test string) *Report {
	for i := range res.Reports {
		if res.Reports[i].Test == test {
			return &res.Reports[i]
		}
	}
	return nil
}

func TestAnalyzeLinux(t *testing.T) {
	c, err := protocol.ParseClient(linuxClient)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultAnalysisCfg + `
HOST=web01
	PROC sshd 1
	PROC nginx 2
	PORT LOCAL=%[.:]22$ STATE=LISTEN TEXT=ssh
	LOG /var/log/messages ERROR COLOR=yellow
`
	rules, err := Parse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := Analyze(c, rules, HostInfo{Name: "web01"})

	cpu := find(res, "cpu")
	if cpu == nil || cpu.Color != protocol.Yellow || !strings.Contains(cpu.Text, "up: 3 days, 2 users, 3 procs, load=7.50") {
		t.Fatalf("cpu: %+v", cpu)
	}
	disk := find(res, "disk")
	if disk == nil || disk.Color != protocol.Red || !strings.Contains(disk.Text, "&red / (96% used)") {
		t.Fatalf("disk: %+v", disk)
	}
	var sawData bool
	for _, m := range res.Metrics["disk"] {
		if m.Name == "/data" && m.Value == 10 {
			sawData = true
		}
	}
	if !sawData {
		t.Fatalf("wrapped df line not parsed: %+v", res.Metrics["disk"])
	}
	mem := find(res, "memory")
	// swap 75% -> yellow (50/80)
	if mem == nil || mem.Color != protocol.Yellow {
		t.Fatalf("memory: %+v", mem)
	}
	procs := find(res, "procs")
	if procs == nil || procs.Color != protocol.Red || !strings.Contains(procs.Text, "&red nginx (found 1, req. 2 or more)") {
		t.Fatalf("procs: %+v", procs)
	}
	ports := find(res, "ports")
	if ports == nil || ports.Color != protocol.Green {
		t.Fatalf("ports: %+v", ports)
	}
	msgs := find(res, "msgs")
	if msgs == nil || msgs.Color != protocol.Yellow {
		t.Fatalf("msgs: %+v", msgs)
	}
	var idle float64
	for _, m := range res.Metrics["cpu"] {
		if m.Name == "cpu_idle" {
			idle = m.Value
		}
	}
	if idle != 85 {
		t.Fatalf("vmstat idle %v", idle)
	}
	if find(res, "inode") == nil {
		t.Fatal("no inode report")
	}
}

func TestUptime(t *testing.T) {
	cases := map[string]time.Duration{
		" 10:00:00 up 3 days,  2:03,  2 users,  load average: 0.00":    74*time.Hour + 3*time.Minute,
		" 10:00:00 up 15 min,  1 user,  load average: 0.00":            15 * time.Minute,
		"10:00  up 1 day, 3 mins, 2 users, load averages: 1.2 1.3 1.4": 24*time.Hour + 3*time.Minute,
		" 10:00:00 up  4:05,  1 user,  load average: 0.00":             4*time.Hour + 5*time.Minute,
	}
	for in, want := range cases {
		got, ok := parseUptime(in)
		if !ok || got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestRuleSelectors(t *testing.T) {
	rules, err := Parse(`
HOST=%^db
	LOAD 1 2
CLASS=win32
	LOAD 50 60
DEFAULT
	LOAD 5 10
`)
	if err != nil {
		t.Fatal(err)
	}
	if r := first(rules.For(HostInfo{Name: "db1"}), "LOAD"); r.Warn != 1 {
		t.Fatalf("db1 got %v", r.Warn)
	}
	if r := first(rules.For(HostInfo{Name: "x", OS: "win32"}), "LOAD"); r.Warn != 50 {
		t.Fatalf("win got %v", r.Warn)
	}
	if r := first(rules.For(HostInfo{Name: "web"}), "LOAD"); r.Warn != 5 {
		t.Fatalf("default got %v", r.Warn)
	}
}

func TestClientLocal(t *testing.T) {
	cl := ParseClientLocal(DefaultClientLocalCfg + "\n[web01]\nlog:/var/log/nginx/error.log:4096\n")
	if s := cl.For("web01", "linux", "linux"); !strings.Contains(s, "nginx") {
		t.Fatalf("host section: %q", s)
	}
	if s := cl.For("other", "", "linux"); !strings.Contains(s, "/var/log/messages") {
		t.Fatalf("os section: %q", s)
	}
}
