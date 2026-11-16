package netmon

import "testing"

func TestPlan(t *testing.T) {
	p := Plan("ssh:2222 !telnet ?smtp https://x/ cont;https://y/;ok httpstatus;https://z/;200|301 unknowntag")
	if !p.conn {
		t.Fatal("conn should be implied by network tests")
	}
	if len(p.services) != 3 || p.services[0].port != 2222 || !p.services[1].reverse || !p.services[2].dialup {
		t.Fatalf("services: %+v", p.services)
	}
	if len(p.urls) != 3 || p.urls[1].contains == nil || p.urls[2].codes == nil {
		t.Fatalf("urls: %+v", p.urls)
	}
	if Plan("noconn ssh").conn {
		t.Fatal("noconn ignored")
	}
	if Plan("").conn {
		t.Fatal("empty tags should not ping")
	}
}
