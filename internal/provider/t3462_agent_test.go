package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type agentRegMock struct {
	mu      sync.Mutex
	base    http.Handler
	ver     string
	updates int
	pol     map[string]any
	puts    int
}

var reAgentReg = regexp.MustCompile(`/cloud/(?:servers/601/agent(/update)?|containers/7/registry(/policy)?)$`)

func (x *agentRegMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reAgentReg.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	out := func(d any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": d})
	}
	switch {
	case mt[1] == "/update":
		x.updates++
		x.ver = "4.2"
		w.WriteHeader(202)
		out(map[string]any{"state": "updating", "task_id": 77})
	case r.URL.Path[len(r.URL.Path)-5:] == "agent":
		out(map[string]any{"version": x.ver, "latest": "4.2", "update_available": x.ver != "4.2", "private_network_supported": x.ver == "4.2", "network_applied": true, "supported": true,
			"update_command": "curl -fsSL https://example.invalid/u.sh | sh"})
	case mt[2] == "/policy":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		x.puts++
		x.pol = map[string]any{"enabled": b["enabled"], "keep": b["keep"], "untagged_days": b["untagged_days"]}
		out(map[string]any{"status": "successful", "data": map[string]any{"policy": x.pol}})
	default:
		out(map[string]any{"server": "registry.example", "policy": x.pol})
	}
}

// #3462 B5 + madde 11: gyro_server_agent updates an outdated agent (and shows it in the plan when it gets old again);
// gyro_container_registry_policy sets / reads / resets the clean-up policy.
func TestAccMockServerAgentAndRegistryPolicy(t *testing.T) {
	x := &agentRegMock{base: mockapi.New(), ver: "3.1", pol: map[string]any{"enabled": true, "keep": 10, "untagged_days": 7}}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n  poll_interval_seconds = 1\n  poll_timeout_minutes = 1\n}\n", srv.URL+"/api/v1/client")
	cfg := prov + `
resource "gyro_server_agent" "a" {
  server_id = 601
}
resource "gyro_container_registry_policy" "p" {
  service_id    = 7
  keep          = 5
  untagged_days = 3
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if fmt.Sprint(x.pol) != "map[enabled:true keep:10 untagged_days:7]" {
				return fmt.Errorf("policy not reset: %v", x.pol)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_agent.a", "version", "4.2"),
				resource.TestCheckResourceAttr("gyro_server_agent.a", "update_available", "false"),
				resource.TestCheckResourceAttr("gyro_server_agent.a", "private_network_supported", "true"),
				resource.TestCheckResourceAttr("gyro_container_registry_policy.p", "keep", "5"),
				resource.TestCheckResourceAttr("gyro_container_registry_policy.p", "enabled", "true"),
				func(*terraform.State) error {
					if x.updates != 1 {
						return fmt.Errorf("updates %d", x.updates)
					}
					return nil
				})},
			{Config: cfg, PlanOnly: true},
			// the agent gets old again (new release): plan shows an in-place change, apply updates
			{PreConfig: func() { x.mu.Lock(); x.ver = "4.1"; x.mu.Unlock() }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: cfg, Check: func(*terraform.State) error {
				if x.updates != 2 {
					return fmt.Errorf("updates %d, want 2", x.updates)
				}
				return nil
			}},
			{ResourceName: "gyro_container_registry_policy.p", ImportState: true, ImportStateId: "7", ImportStateVerify: true},
		},
	})
}
