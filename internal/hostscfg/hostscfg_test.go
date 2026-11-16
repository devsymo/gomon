package hostscfg

import (
	"strings"
	"testing"

	"gomon/internal/model"
)

func TestParseAndFormat(t *testing.T) {
	in := `# comment
page linux <b>Linux</b> servers
group Web tier
192.0.2.10   web01.example.com   # conn ssh https://web01.example.com/
subpage db Databases
group-compress Primary
0.0.0.0      DB01.example.com    # conn pgsql
subparent db replicas Read replicas
group-only cpu|disk Replica nodes
0.0.0.0      db02.example.com
include other.cfg
not a host line
`
	r := Parse(in)
	if len(r.Entries) != 3 {
		t.Fatalf("entries: %+v", r.Entries)
	}
	e := r.Entries
	if e[0].Page != "linux" || e[0].Group != "Web tier" || e[0].Tags != "conn ssh https://web01.example.com/" {
		t.Fatalf("first: %+v", e[0])
	}
	if e[1].Name != "db01.example.com" || e[1].Page != "linux/db" || e[1].Group != "Primary" {
		t.Fatalf("second: %+v", e[1])
	}
	if e[2].Page != "linux/db/replicas" || e[2].Group != "Replica nodes" {
		t.Fatalf("third: %+v", e[2])
	}
	if r.Titles["linux"] != "Linux servers" || r.Titles["linux/db/replicas"] != "Read replicas" {
		t.Fatalf("titles: %v", r.Titles)
	}
	if len(r.Warnings) != 2 {
		t.Fatalf("warnings: %v", r.Warnings)
	}

	var hosts []*model.Host
	for _, x := range e {
		hosts = append(hosts, &model.Host{Name: x.Name, IP: x.IP, Tags: x.Tags, Page: x.Page, Group: x.Group})
	}
	out := Format(hosts, r.Titles)
	for _, want := range []string{"page linux Linux servers", "group Web tier", "subpage db Databases",
		"subparent db replicas Read replicas", "192.0.2.10       web01.example.com\t# conn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("format missing %q:\n%s", want, out)
		}
	}
	// round trip
	r2 := Parse(out)
	if len(r2.Entries) != 3 || r2.Entries[2].Page != "linux/db/replicas" || r2.Entries[1].Group != "Primary" {
		t.Fatalf("round trip: %+v", r2.Entries)
	}
}
