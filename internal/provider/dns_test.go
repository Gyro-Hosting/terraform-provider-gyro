package provider

import "testing"

func TestDNSFQDNAndNorm(t *testing.T) {
	cases := [][3]string{{"@", "Example.com", "example.com."}, {"www", "example.com", "www.example.com."}, {"www.example.com", "example.com", "www.example.com."}, {"x.other.net.", "example.com", "x.other.net."}}
	for _, c := range cases {
		if g := dnsFQDN(c[0], c[1]); g != c[2] {
			t.Fatalf("fqdn %v -> %s", c, g)
		}
	}
	n := [][4]string{{"MX", "10 mx1", "example.com", "10 mx1.example.com"}, {"MX", "10 mx1.example.com.", "example.com", "10 mx1.example.com"},
		{"TXT", "\"a\" \"b\"", "example.com", "ab"}, {"TXT", "v=spf1 -all", "example.com", "v=spf1 -all"}, {"CNAME", "@", "example.com", "example.com"},
		{"SRV", "10 5 5060 sip", "example.com", "10 5 5060 sip.example.com"}, {"AAAA", "2001:DB8::1", "example.com", "2001:db8::1"}}
	for _, c := range n {
		if g := dnsNorm(c[0], c[1], c[2]); g != c[3] {
			t.Fatalf("norm %v -> %q", c, g)
		}
	}
}
