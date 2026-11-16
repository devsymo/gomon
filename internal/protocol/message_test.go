package protocol

import (
	"fmt"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) {
	st, err := ParseStatus("status+2h/group:ops www,example,com.http red Mon Nov 16 web is down\nmore detail\n")
	if err != nil {
		t.Fatal(err)
	}
	if st.Host != "www.example.com" || st.Test != "http" || st.Color != Red {
		t.Fatalf("bad parse: %+v", st)
	}
	if st.Lifetime != 2*time.Hour || st.Group != "ops" {
		t.Fatalf("bad modifiers: %+v", st)
	}
	if st.Summary() != "Mon Nov 16 web is down" {
		t.Fatalf("summary %q", st.Summary())
	}
}

func TestParseStatusRelayed(t *testing.T) {
	st, err := ParseStatus("status host1.cpu green ok\nline\nStatus message received from 10.0.0.5\n")
	if err != nil {
		t.Fatal(err)
	}
	if st.Sender != "10.0.0.5" || st.Text != "ok\nline\n" {
		t.Fatalf("got %+v", st)
	}
}

func TestCombo(t *testing.T) {
	msgs := SplitCombo("combo\nstatus a.x green one\n\nstatus b.y red two\nmore\n\nstatus c.z yellow three\n")
	if len(msgs) != 3 {
		t.Fatalf("got %d: %q", len(msgs), msgs)
	}
	st, _ := ParseStatus(msgs[1])
	if st.Host != "b" || st.Text != "two\nmore\n" {
		t.Fatalf("got %+v", st)
	}
}

func TestExtCombo(t *testing.T) {
	a, b := "status a.x green\n", "status b.y red\n"
	// header "extcombo 18 35 50\n" is 18 bytes long
	full := fmt.Sprintf("extcombo %d %d %d\n", 18, 18+len(a), 18+len(a)+len(b)) + a + b
	parts, err := SplitExtCombo(full)
	if err != nil || len(parts) != 2 || parts[0] != a || parts[1] != b {
		t.Fatalf("got %q %v", parts, err)
	}
}

func TestParseClient(t *testing.T) {
	c, err := ParseClient("client web01,example,com.linux linux\n[date]\nMon\n[uptime]\n 10:00 up 3 days\n[msgs:/var/log/messages]\nfoo\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "web01.example.com" || c.OS != "linux" || c.Class != "linux" {
		t.Fatalf("got %+v", c)
	}
	if v, _ := c.Get("uptime"); v != " 10:00 up 3 days" {
		t.Fatalf("uptime %q", v)
	}
	if len(c.WithPrefix("msgs:")) != 1 {
		t.Fatal("msgs section missing")
	}
}

func TestTrends(t *testing.T) {
	m := ParseTrends("[temp.rrd]\nDS:cpu:GAUGE:600:0:U 45\nDS:case:GAUGE:600:0:U 30.5\n[other,x.rrd]\nDS:v:COUNTER:600:0:U 7\n")
	if len(m["temp"]) != 2 || m["temp"][1].Value != 30.5 || len(m["other,x"]) != 1 {
		t.Fatalf("got %+v", m)
	}
}

func TestDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"30": 30 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "-1": -1} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v", in, got, err)
		}
	}
}

func TestNCV(t *testing.T) {
	m := ParseNCV("&green Queue length : 12\nlatency=3.5ms\nnot a metric\n", 10)
	if len(m) != 2 || m[0].Name != "Queue_length" || m[1].Value != 3.5 {
		t.Fatalf("got %+v", m)
	}
}
