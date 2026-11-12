package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// API 2.16.0: gyro_server_ip (additional IPv4 of a tr-ist-1 server). The ips endpoints are served by a small wrapper around the mock API.
type ipMock struct {
	mu   sync.Mutex
	next int
	ips  map[string][]string // server id -> extra ips
	base http.Handler
	dels int
}

var reIPPath = regexp.MustCompile(`/cloud/servers/(\d+)/ips(?:/([0-9.]+))?$`)

func (x *ipMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reIPPath.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	srv, ip := mt[1], mt[2]
	list := func(added []string) {
		ex := []map[string]any{}
		for _, a := range x.ips[srv] {
			ex = append(ex, map[string]any{"ip": a, "prefix": 32, "added_at": "2026-11-12T12:00:00+03:00", "reverse_dns": strings.ReplaceAll(a, ".", "-") + ".ip.gyrohosting.com"})
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(201)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"server_id": srv, "primary_ip": "31.58.248.195", "extra_ips": ex, "count": len(ex),
			"max": 5, "available": 5 - len(ex), "applied_on_server": true, "added": added,
			"pricing": map[string]any{"billing": "prepaid", "per_ip_monthly_eur": 2.0, "per_ip_hourly_eur": 0.002976, "minimum_hours": 6}}})
	}
	switch {
	case r.Method == http.MethodGet && ip == "":
		list(nil)
	case r.Method == http.MethodPost && ip == "":
		if len(x.ips[srv]) >= 5 {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"tool_action_failed","message":"limit"}}`))
			return
		}
		x.next++
		a := fmt.Sprintf("31.58.248.%d", 200+x.next)
		x.ips[srv] = append(x.ips[srv], a)
		list([]string{a})
	case r.Method == http.MethodDelete && ip != "":
		if r.URL.Query().Get("confirm") != "true" {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"confirm_required","message":"confirm"}}`))
			return
		}
		out := []string{}
		for _, a := range x.ips[srv] {
			if a != ip {
				out = append(out, a)
			}
		}
		x.ips[srv] = out
		x.dels++
		list(nil)
	default:
		w.WriteHeader(405)
	}
}

func TestAccMockServerIP(t *testing.T) {
	m := mockapi.New()
	m.Ist = true
	x := &ipMock{ips: map[string][]string{}, base: m}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
	cfg := func(n int) string {
		return prov + fmt.Sprintf(`
resource "gyro_server" "s" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-24.04"
  confirm_order = true
}
resource "gyro_server_ip" "a" {
  server_id = tonumber(gyro_server.s.id)
}
%s`, map[bool]string{true: "resource \"gyro_server_ip\" \"b\" {\n  server_id = tonumber(gyro_server.s.id)\n}\n", false: ""}[n > 1])
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(2), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("gyro_server_ip.a", "ip", regexp.MustCompile(`^31\.58\.248\.\d+$`)),
				resource.TestCheckResourceAttr("gyro_server_ip.b", "applied_on_server", "true"),
				resource.TestCheckResourceAttr("gyro_server_ip.a", "minimum_hours", "6"),
				resource.TestCheckResourceAttr("gyro_server_ip.a", "monthly_eur", "2"),
				resource.TestMatchResourceAttr("gyro_server_ip.a", "reverse_dns", regexp.MustCompile(`^31-58-248-\d+\.ip\.gyrohosting\.com$`)),
				func(s *terraform.State) error {
					a := s.RootModule().Resources["gyro_server_ip.a"].Primary.Attributes["ip"]
					b := s.RootModule().Resources["gyro_server_ip.b"].Primary.Attributes["ip"]
					if a == b || !strings.Contains(s.RootModule().Resources["gyro_server_ip.a"].Primary.ID, "/"+a) {
						return fmt.Errorf("ids %s %s", a, b)
					}
					return nil
				},
			)},
			{ResourceName: "gyro_server_ip.a", ImportState: true, ImportStateVerify: true},
			{Config: cfg(1), Check: func(*terraform.State) error {
				if x.dels != 1 {
					return fmt.Errorf("expected one release, got %d", x.dels)
				}
				return nil
			}},
		},
	})
}
