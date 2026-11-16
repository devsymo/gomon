package hostscfg

import (
	"strings"
	"testing"

	"gomon/internal/model"
)

func TestParseAndFormat(t *testing.T) {
	in := `# comment
page linux Linux servers
192.0.2.10   web01.example.com   # conn ssh https://web01.example.com/
subpage db Databases
0.0.0.0      DB01.example.com    # conn pgsql
group-compress Misc
include other.cfg
not a host line
`
	entries, warnings := Parse(in)
	if len(entries) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	if entries[0].Page != "linux" || entries[0].Tags != "conn ssh https://web01.example.com/" {
		t.Fatalf("first: %+v", entries[0])
	}
	if entries[1].Name != "db01.example.com" || entries[1].Page != "linux/db" {
		t.Fatalf("second: %+v", entries[1])
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings: %v", warnings)
	}
	out := Format([]*model.Host{{Name: "web01", IP: "192.0.2.10", Tags: "conn", Page: "linux"}})
	if !strings.Contains(out, "page linux") || !strings.Contains(out, "192.0.2.10       web01\t# conn") {
		t.Fatalf("format: %q", out)
	}
}
