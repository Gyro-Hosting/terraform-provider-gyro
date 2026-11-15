package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// gyro_server_public_ip (v0.33.0): DELETE {confirm_ip, allow_no_egress} -> off, POST -> on, destroy = on again.
type pipMock struct {
	mu        sync.Mutex
	base      http.Handler
	on        bool
	polls     int
	confirmIP string
	noEgress  bool
}

func (x *pipMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/cloud/servers/77/public-ip") {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	var b map[string]any
	_ = json.NewDecoder(r.Body).Decode(&b)
	switch r.Method {
	case http.MethodGet:
		x.polls++
		jsonOut(w, 200, map[string]any{"server_id": 77, "ip": "203.0.113.77", "enabled": x.on, "extra_ips": 0, "private_ready": true})
	case http.MethodDelete:
		x.confirmIP, _ = b["confirm_ip"].(string)
		x.noEgress, _ = b["allow_no_egress"].(bool)
		if x.confirmIP != "203.0.113.77" {
			jsonErr(w, 422, "confirm_mismatch", "confirm_ip")
			return
		}
		x.on = false
		jsonOut(w, 202, map[string]any{"server_id": 77, "enabled": false, "task_id": 1})
	case http.MethodPost:
		x.on = true
		jsonOut(w, 202, map[string]any{"server_id": 77, "enabled": true, "task_id": 2})
	}
}

func TestAccMockServerPublicIP(t *testing.T) {
	x := &pipMock{base: mockapi.New(), on: true}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n  poll_interval_seconds = 1\n  poll_timeout_minutes = 1\n}\n", srv.URL+"/api/v1/client")
	cfg := func(on bool) string {
		return prov + fmt.Sprintf("resource \"gyro_server_public_ip\" \"db\" {\n  server_id = 77\n  enabled = %t\n  allow_no_egress = true\n}\n", on)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if !x.on {
				return fmt.Errorf("destroy must turn the public IP back on")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg(false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_public_ip.db", "enabled", "false"),
				resource.TestCheckResourceAttr("gyro_server_public_ip.db", "ip", "203.0.113.77"),
				func(*terraform.State) error {
					if x.confirmIP != "203.0.113.77" || !x.noEgress {
						return fmt.Errorf("DELETE body: confirm_ip=%q allow_no_egress=%t", x.confirmIP, x.noEgress)
					}
					return nil
				})},
			{Config: cfg(false), PlanOnly: true},
			{Config: cfg(true), Check: resource.TestCheckResourceAttr("gyro_server_public_ip.db", "enabled", "true")},
			{ResourceName: "gyro_server_public_ip.db", ImportState: true, ImportStateId: "77", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"allow_no_egress"}},
			{Config: cfg(false)},
		},
	})
}
