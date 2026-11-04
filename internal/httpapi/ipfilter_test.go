package httpapi

import (
	"net"
	"testing"
)

func TestNormalizeCIDR(t *testing.T) {
	cases := map[string]string{
		"192.168.1.10":    "192.168.1.10/32",
		"192.168.1.0/24":  "192.168.1.0/24",
		"10.0.0.5/8":      "10.0.0.0/8",
		" 192.168.1.31  ": "192.168.1.31/32",
	}
	for in, want := range cases {
		got, ok := normalizeCIDR(in)
		if !ok || got != want {
			t.Fatalf("normalizeCIDR(%q) = (%q,%v), want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "nope", "999.1.1.1"} {
		if _, ok := normalizeCIDR(bad); ok {
			t.Fatalf("normalizeCIDR(%q) accepted a bad CIDR", bad)
		}
	}
}

func TestIPFilterSemantics(t *testing.T) {
	f := newIPFilter()
	f.allow = []*net.IPNet{mustCIDR("192.168.1.0/24")}
	f.deny = []*net.IPNet{mustCIDR("192.168.1.66/32")}

	for _, tc := range []struct {
		ip    string
		allow bool
	}{
		{"127.0.0.1", true},     // loopback always allowed
		{"192.168.1.20", true},  // inside allowlist
		{"192.168.1.66", false}, // deny wins over allow
		{"10.0.0.5", false},     // not in a non-empty allowlist
	} {
		ok, _ := f.allowed(net.ParseIP(tc.ip))
		if ok != tc.allow {
			t.Fatalf("allowed(%s) = %v, want %v", tc.ip, ok, tc.allow)
		}
	}

	// Empty allow list means "no allowlist".
	g := newIPFilter()
	g.deny = []*net.IPNet{mustCIDR("10.1.0.0/16")}
	if ok, _ := g.allowed(net.ParseIP("10.2.3.4")); !ok {
		t.Fatal("empty allowlist rejected an address")
	}
	if ok, _ := g.allowed(net.ParseIP("10.1.2.3")); ok {
		t.Fatal("deny rule did not reject")
	}
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}
