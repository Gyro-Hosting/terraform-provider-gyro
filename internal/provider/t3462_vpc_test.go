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

// #3462 B6 + madde 13: gyro_vpc first subnet name / range; gyro_port_forward name (in place, only the changed field is sent).
func TestAccMockVPCFirstSubnetAndForwardName(t *testing.T) {
	x, prov := vpcProv(t)
	cfg := func(subName, pfName string) string {
		return prov + fmt.Sprintf(`
resource "gyro_vpc" "v" {
  name              = "gyro-proje"
  cidr              = "10.20.0.0/16"
  first_subnet_name = %q
  first_subnet_cidr = "10.20.1.0/24"
  gateway_enabled   = true
}
resource "gyro_port_forward" "ssh" {
  vpc_id      = gyro_vpc.v.id
  protocol    = "tcp"
  public_port = 2222
  target_ip   = "10.20.1.10"
  target_port = 22
  name        = %q
}
`, subName, pfName)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("veri", "a\tb"), PlanOnly: true, ExpectError: regexp.MustCompile(`Control characters`)},
			{Config: cfg("veri", "ssh-yonetim"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_vpc.v", "first_subnet_name", "veri"),
				resource.TestCheckResourceAttr("gyro_vpc.v", "first_subnet_cidr", "10.20.1.0/24"),
				resource.TestCheckResourceAttr("gyro_port_forward.ssh", "name", "ssh-yonetim"),
				func(*terraform.State) error {
					for _, s := range x.subnets {
						if s.Name == "veri" && s.CIDR == "10.20.1.0/24" {
							return nil
						}
					}
					return fmt.Errorf("first subnet not created as configured")
				})},
			{Config: cfg("veri", "ssh-yonetim"), PlanOnly: true},
			// first_subnet_name change after create: no diff, no replacement (create only)
			{Config: cfg("baska", "ssh-yonetim"), PlanOnly: true},
			// rename the forward in place: one PATCH with only name
			{Config: cfg("veri", "Gyro Proje SSH"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_port_forward.ssh", "name", "Gyro Proje SSH"),
				func(*terraform.State) error {
					if x.pfPatches != 1 {
						return fmt.Errorf("pf patches %d, want 1", x.pfPatches)
					}
					return nil
				})},
			{ResourceName: "gyro_vpc.v", ImportState: true, ImportStateVerify: true},
		},
	})
}

// addonMock: GET available / GET owned / POST buy / DELETE remove of service 501 (prepaid) and 502 (invoiced).
type addonMock struct {
	mu     sync.Mutex
	base   http.Handler
	owned  map[int64][]map[string]any
	next   int64
	buys   int
	polls  int
	idem   []string
	delete int
}

var reAddon = regexp.MustCompile(`/cloud/services/(50[12])/addons(?:/(available|\d+))?$`)

func (x *addonMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reAddon.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	sid := int64(501)
	if mt[1] == "502" {
		sid = 502
	}
	out := func(code int, d any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": d})
	}
	switch {
	case mt[2] == "available":
		out(200, map[string]any{"service_id": sid, "addons": []any{
			map[string]any{"addon_id": 181, "name": "Ağ Güvenlik Duvarı", "options": []any{map[string]any{"id": 0, "name": "Standart"}}},
			map[string]any{"addon_id": 120, "name": "Ek IPv4", "options": []any{map[string]any{"id": 0, "name": "1 adet"}, map[string]any{"id": 1, "name": "2 adet"}}},
		}})
	case mt[2] == "" && r.Method == http.MethodGet:
		x.polls++
		l := x.owned[sid]
		for _, a := range l { // becomes active on the second read
			if a["status"] == "waiting" && x.polls > 1 {
				a["status"] = "active"
			}
		}
		if l == nil {
			l = []map[string]any{}
		}
		out(200, map[string]any{"service_id": sid, "addons": l})
	case mt[2] == "" && r.Method == http.MethodPost:
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["confirm"] != true || r.Header.Get("Idempotency-Key") == "" {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"confirm_required","message":"confirm"}}`))
			return
		}
		x.buys++
		x.idem = append(x.idem, r.Header.Get("Idempotency-Key"))
		x.next++
		rec := 9000 + x.next
		name, opt := "Ağ Güvenlik Duvarı", "Standart"
		if b["addon_id"] == float64(120) {
			name, opt = "Ek IPv4", "2 adet"
		}
		bill, rm, st := "prepaid", true, "active"
		if sid == 502 {
			bill, rm, st = "invoice", false, "waiting"
		}
		x.owned[sid] = append(x.owned[sid], map[string]any{"id": rec, "addon_id": b["addon_id"], "option_id": b["option_id"], "name": name, "option": opt,
			"quantity": b["quantity"], "status": st, "billing": bill, "removable": rm})
		out(202, map[string]any{"service_id": sid, "addon_record_id": rec, "status": map[bool]string{true: "active", false: "accepted"}[sid == 501]})
	case r.Method == http.MethodDelete:
		x.delete++
		if sid == 502 {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"not_removable","message":"Only prepaid add-ons can be removed through the API"}}`))
			return
		}
		keep := []map[string]any{}
		for _, a := range x.owned[sid] {
			if fmt.Sprint(a["id"]) != mt[2] {
				keep = append(keep, a)
			}
		}
		x.owned[sid] = keep
		out(200, map[string]any{"removed": true})
	}
}

// #3462 madde 9: gyro_server_addon by name and by id; waits until active; prepaid destroy removes, invoiced destroy warns.
func TestAccMockServerAddon(t *testing.T) {
	x := &addonMock{base: mockapi.New(), owned: map[int64][]map[string]any{}}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n  poll_interval_seconds = 1\n  poll_timeout_minutes = 1\n}\n", srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.owned[501]) != 0 {
				return fmt.Errorf("prepaid add-on left: %v", x.owned[501])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: prov + `
resource "gyro_server_addon" "fw" {
  service_id = 501
  addon      = "güvenlik duvarı"
}
resource "gyro_server_addon" "ip" {
  service_id = 502
  addon_id   = 120
  option     = "2 ADET"
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_addon.fw", "addon_id", "181"),
				resource.TestCheckResourceAttr("gyro_server_addon.fw", "status", "active"),
				resource.TestCheckResourceAttr("gyro_server_addon.fw", "billing", "prepaid"),
				resource.TestCheckResourceAttr("gyro_server_addon.fw", "removable", "true"),
				resource.TestCheckResourceAttr("gyro_server_addon.ip", "option_id", "1"),
				resource.TestCheckResourceAttr("gyro_server_addon.ip", "status", "active"),
				resource.TestCheckResourceAttr("gyro_server_addon.ip", "billing", "invoice"),
				func(*terraform.State) error {
					if x.buys != 2 || len(x.idem) != 2 || x.idem[0] == x.idem[1] {
						return fmt.Errorf("buys %d idem %v", x.buys, x.idem)
					}
					return nil
				})},
			{Config: prov + `
resource "gyro_server_addon" "fw" {
  service_id = 501
  addon      = "güvenlik duvarı"
}
resource "gyro_server_addon" "ip" {
  service_id = 502
  addon_id   = 120
  option     = "2 ADET"
}
`, PlanOnly: true},
			{ResourceName: "gyro_server_addon.fw", ImportState: true, ImportStateIdFunc: func(s *terraform.State) (string, error) {
				return s.RootModule().Resources["gyro_server_addon.fw"].Primary.ID, nil
			}, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"addon"}},
			{Config: prov + `
resource "gyro_server_addon" "x" {
  service_id = 501
  addon      = "yok"
}
`, ExpectError: regexp.MustCompile(`not available for service 501`)},
		},
	})
	if !strings.Contains(fmt.Sprint(x.delete), "2") {
		t.Fatalf("deletes %d, want 2 (prepaid removed + invoiced attempted)", x.delete)
	}
}
