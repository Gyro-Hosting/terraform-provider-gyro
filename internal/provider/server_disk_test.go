package provider

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// gyro_server_disk (additional disks of a tr-ist-1 server). The disks endpoints are served by a small wrapper around the mock API.
type diskMock struct {
	mu    sync.Mutex
	next  int64
	disks map[string]map[int64]int64 // server id -> disk id -> GB
	cls   map[int64]string
	base  http.Handler
	dels  int
	grows int
}

var reDiskPath = regexp.MustCompile(`/cloud/servers/(\d+)/disks(?:/(\d+))?$`)

func (x *diskMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reDiskPath.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	srv := mt[1]
	did, _ := strconv.ParseInt(mt[2], 10, 64)
	if x.disks[srv] == nil {
		x.disks[srv] = map[int64]int64{}
	}
	var body struct {
		SizeGB int64  `json:"size_gb"`
		Class  string `json:"class"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	list := func(st int) {
		ds := []map[string]any{}
		for i := int64(2001); i <= 2010; i++ {
			if gb, ok := x.disks[srv][i]; ok {
				px := 0.09
				if x.cls[i] == "hdd" {
					px = 0.035
				}
				ds = append(ds, map[string]any{"id": i, "name": fmt.Sprintf("Additional disk %d", len(ds)+1), "class": x.cls[i], "size_gb": gb, "monthly_eur": math.Round(float64(gb)*px*10000) / 10000})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"server_id": srv, "disks": ds, "count": len(ds), "max_disks": 4, "step_gb": 10,
			"classes": map[string]any{"ssd": map[string]any{"per_gb_monthly_eur": 0.09}, "hdd": map[string]any{"per_gb_monthly_eur": 0.035}},
			"pricing": map[string]any{"billing": "prepaid", "per_gb_monthly_eur": 0.09, "per_gb_hourly_eur": 0.09 / 672, "minimum_hours": 1}}})
	}
	bad := func(code string) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"x"}}`))
	}
	switch {
	case r.Method == http.MethodGet && did == 0:
		list(200)
	case r.Method == http.MethodPost && did == 0:
		if body.SizeGB < 10 || body.SizeGB%10 != 0 || len(x.disks[srv]) >= 4 {
			bad("tool_action_failed")
			return
		}
		x.next++
		x.disks[srv][2000+x.next] = body.SizeGB
		if body.Class == "" {
			body.Class = "ssd"
		}
		x.cls[2000+x.next] = body.Class
		list(201)
	case r.Method == http.MethodPut && did > 0:
		if cur, ok := x.disks[srv][did]; !ok || body.SizeGB <= cur {
			bad("tool_action_failed")
			return
		}
		x.disks[srv][did] = body.SizeGB
		x.grows++
		list(200)
	case r.Method == http.MethodDelete && did > 0:
		if r.URL.Query().Get("confirm") != "true" {
			bad("confirm_required")
			return
		}
		delete(x.disks[srv], did)
		x.dels++
		list(200)
	default:
		w.WriteHeader(405)
	}
}

func TestAccMockServerDisk(t *testing.T) {
	m := mockapi.New()
	m.Ist = true
	x := &diskMock{disks: map[string]map[int64]int64{}, cls: map[int64]string{}, base: m}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
	cfg := func(a, b int) string {
		s := prov + `
resource "gyro_server" "s" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-24.04"
  confirm_order = true
}
` + fmt.Sprintf(`resource "gyro_server_disk" "a" {
  server_id = tonumber(gyro_server.s.id)
  size_gb   = %d
}
`, a)
		if b > 0 {
			s += fmt.Sprintf(`resource "gyro_server_disk" "b" {
  server_id = tonumber(gyro_server.s.id)
  size_gb   = %d
  class     = "hdd"
}
`, b)
		}
		return s
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(20, 10), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_disk.a", "size_gb", "20"),
				resource.TestCheckResourceAttr("gyro_server_disk.a", "per_gb_monthly_eur", "0.09"),
				resource.TestCheckResourceAttr("gyro_server_disk.a", "class", "ssd"),
				resource.TestCheckResourceAttr("gyro_server_disk.b", "class", "hdd"),
				resource.TestCheckResourceAttr("gyro_server_disk.b", "per_gb_monthly_eur", "0.035"),
				resource.TestCheckResourceAttr("gyro_server_disk.a", "minimum_hours", "1"),
				resource.TestCheckResourceAttr("gyro_server_disk.a", "monthly_eur", "1.8"),
				func(s *terraform.State) error {
					a := s.RootModule().Resources["gyro_server_disk.a"].Primary.Attributes["disk_id"]
					b := s.RootModule().Resources["gyro_server_disk.b"].Primary.Attributes["disk_id"]
					if a == b || a == "" {
						return fmt.Errorf("disk ids %q %q", a, b)
					}
					return nil
				},
			)},
			{ResourceName: "gyro_server_disk.a", ImportState: true, ImportStateVerify: true},
			{Config: cfg(40, 10), Check: func(*terraform.State) error {
				if x.grows != 1 {
					return fmt.Errorf("expected one grow, got %d", x.grows)
				}
				return nil
			}},
			{Config: cfg(30, 10), ExpectError: regexp.MustCompile(`Disk cannot shrink`)},
			{Config: cfg(40, 0), Check: func(*terraform.State) error {
				if x.dels != 1 {
					return fmt.Errorf("expected one removal, got %d", x.dels)
				}
				return nil
			}},
		},
	})
}
