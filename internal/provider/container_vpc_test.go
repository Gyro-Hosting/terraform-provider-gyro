package provider

import (
	"encoding/json"
	"fmt"
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

// VPC V2 (API 2.34.0): container environment <-> VPC subnet, and port forward source_cidrs (PATCH in place).
// API 2.38.0: the mock environment is serverless (charged 2.99 EUR/month): PUT without "confirm": true is refused like the API does.
type cvMock struct {
	mu     sync.Mutex
	vpc    *vpcMock
	att    map[int64]map[string]any
	polls  map[int64]int
	puts   int
	patchs int
	dels   int
}

var reCV = regexp.MustCompile(`/cloud/containers/(\d+)/vpc$`)

func (x *cvMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reCV.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.vpc.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	env, _ := strconv.ParseInt(mt[1], 10, 64)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if env == 404 {
		jsonErr(w, 404, "not_found", "env")
		return
	}
	a := x.att[env]
	pricing := map[string]any{"charged": true, "billing": "prepaid_hourly", "monthly_eur": 2.99, "list_monthly_eur": 3.49, "minimum_hours": 1}
	view := func() map[string]any {
		if a == nil {
			return map[string]any{"attached": false, "state": "off", "mode": "serverless", "supported": true, "status": "accepted", "tool": "ecs-vpc", "pricing": pricing}
		}
		st := "attaching"
		x.polls[env]++
		if x.polls[env] > 1 {
			st = "ready"
		}
		ex, _ := a["expose"].([]any)
		if ex == nil {
			ex = []any{}
		}
		out := []any{}
		for _, e := range ex {
			m := e.(map[string]any)
			out = append(out, map[string]any{"service": m["service"], "port": m["port"], "address": fmt.Sprintf("10.10.0.16:%v", m["port"])})
		}
		return map[string]any{"attached": true, "state": st, "mode": "serverless", "supported": true, "pricing": pricing, "vpc_id": a["vpc_id"], "subnet_id": a["subnet_id"],
			"vpc_cidr": "10.10.0.0/16", "subnet_cidr": "10.10.0.0/24", "subnet_gateway": "10.10.0.1", "source_range": "10.10.0.16/28", "endpoint_ip": "10.10.0.16",
			"node_ips": []any{"10.10.0.17"}, "expose": out, "status": "accepted", "tool": "ecs-vpc"}
	}
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, 200, view())
	case http.MethodPut:
		if a != nil {
			jsonErr(w, 422, "tool_action_failed", "already attached")
			return
		}
		if body["confirm"] != true {
			jsonErr(w, 422, "confirm_required", "Attaching a serverless container environment to a VPC is charged")
			return
		}
		x.puts++
		x.att[env] = map[string]any{"vpc_id": body["vpc_id"], "subnet_id": body["subnet_id"], "expose": body["expose"]}
		a = x.att[env]
		jsonOut(w, 200, map[string]any{"status": "successful", "vpc": view()})
	case http.MethodPatch:
		if a == nil {
			jsonErr(w, 422, "tool_action_failed", "not attached")
			return
		}
		x.patchs++
		a["expose"] = body["expose"]
		jsonOut(w, 200, map[string]any{"status": "successful", "vpc": view()})
	case http.MethodDelete:
		if body["confirm"] != true {
			jsonErr(w, 422, "confirm_required", "confirm")
			return
		}
		x.dels++
		delete(x.att, env)
		jsonOut(w, 200, map[string]any{"status": "successful", "vpc": map[string]any{"attached": false, "state": "detaching"}})
	}
}

func cvProv(t *testing.T) (*cvMock, string) {
	x := &cvMock{vpc: newVPCMock(mockapi.New()), att: map[int64]map[string]any{}, polls: map[int64]int{}}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	return x, fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
}

func cvCfg(prov, expose, sources string) string {
	return prov + `
resource "gyro_vpc" "main" {
  name            = "gyro-proje"
  cidr            = "10.10.0.0/16"
  gateway_enabled = true
}

resource "gyro_container_vpc_attachment" "app" {
  environment_id = 17000
  vpc_id         = gyro_vpc.main.id
  subnet_id      = gyro_vpc.main.default_subnet_id
` + expose + `
}

resource "gyro_port_forward" "web" {
  vpc_id       = gyro_vpc.main.id
  protocol     = "tcp"
  public_port  = 8443
  target_ip    = "10.10.0.20"
  target_port  = 443
` + sources + `
}
`
}

func TestAccMockContainerVPC(t *testing.T) {
	x, prov := cvProv(t)
	var pfID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.att) != 0 || len(x.vpc.vpcs) != 0 {
				return fmt.Errorf("left over: attachments=%d vpcs=%d", len(x.att), len(x.vpc.vpcs))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cvCfg(prov, "  expose {\n    service = \"api\"\n    port    = 8080\n  }", ""), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "state", "ready"),
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "source_range", "10.10.0.16/28"),
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "endpoint_ip", "10.10.0.16"),
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "node_ips.0", "10.10.0.17"),
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "monthly_price_eur", "2.99"),
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "expose.0.port", "8080"),
				resource.TestCheckResourceAttr("gyro_port_forward.web", "source_cidrs.#", "0"),
				func(s *terraform.State) error {
					pfID = s.RootModule().Resources["gyro_port_forward.web"].Primary.Attributes["forward_id"]
					return nil
				},
			)},
			{ResourceName: "gyro_container_vpc_attachment.app", ImportState: true, ImportStateId: "17000", ImportStateVerify: true},
			// expose changes in place (PATCH), port forward sources in place (PATCH, same forward id)
			{Config: cvCfg(prov, "  expose {\n    service = \"api\"\n    port    = 8080\n  }\n  expose {\n    service = \"web\"\n    port    = 80\n  }", `  source_cidrs = ["203.0.113.10/32", "198.51.100.0/24"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "expose.#", "2"),
					resource.TestCheckResourceAttr("gyro_port_forward.web", "source_cidrs.#", "2"),
					resource.TestCheckTypeSetElemAttr("gyro_port_forward.web", "source_cidrs.*", "198.51.100.0/24"),
					func(s *terraform.State) error {
						if x.puts != 1 || x.patchs != 1 || x.vpc.pfPatches != 1 {
							return fmt.Errorf("puts=%d patches=%d pfPatches=%d", x.puts, x.patchs, x.vpc.pfPatches)
						}
						if got := s.RootModule().Resources["gyro_port_forward.web"].Primary.Attributes["forward_id"]; got != pfID {
							return fmt.Errorf("port forward was replaced (%s -> %s)", pfID, got)
						}
						return nil
					},
				)},
			{Config: cvCfg(prov, "  expose {\n    service = \"api\"\n    port    = 8080\n  }\n  expose {\n    service = \"web\"\n    port    = 80\n  }", `  source_cidrs = ["203.0.113.10/32", "198.51.100.0/24"]`), PlanOnly: true},
			{Config: cvCfg(prov, ``, `  source_cidrs = ["203.0.113.10"]`), ExpectError: regexp.MustCompile(`(?s)single address is /32`)},
			{Config: cvCfg(prov, ``, `  source_cidrs = ["198.51.100.5/24"]`), ExpectError: regexp.MustCompile(`(?s)network address`)},
			{Config: cvCfg(prov, ``, `  source_cidrs = ["10.0.0.0/8"]`), ExpectError: regexp.MustCompile(`(?s)only public`)},
			// back to everyone
			{Config: cvCfg(prov, ``, `  source_cidrs = []`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_vpc_attachment.app", "expose.#", "0"),
				resource.TestCheckResourceAttr("gyro_port_forward.web", "source_cidrs.#", "0"),
			)},
			{Config: prov, Check: func(*terraform.State) error {
				if x.dels != 1 {
					return fmt.Errorf("detach calls=%d", x.dels)
				}
				return nil
			}},
		},
	})
}
