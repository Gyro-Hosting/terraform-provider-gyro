package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// #3462-A: gyro_container_environment keeps the panel name when `name` is not configured and reads `plan` on import.
type envLabelMock struct {
	mu     sync.Mutex
	nick   string
	extra  int64
	labels int
	alive  bool
}

func (x *envLabelMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/client/")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == "POST" && p == "cloud/containers":
		x.alive = true
		if n, ok := body["name"].(string); ok {
			x.nick = n
		}
		jsonOut(w, 201, map[string]any{"environments": []any{map[string]any{"id": 17100}}})
	case p == "cloud/containers/plans":
		jsonOut(w, 200, []any{map[string]any{"id": 1215, "plan": "serverless"}, map[string]any{"id": 1211, "plan": "pro"}})
	case p == "cloud/services/17100" && r.Method == "GET":
		if !x.alive {
			jsonErr(w, 404, "not_found", "x")
			return
		}
		jsonOut(w, 200, map[string]any{"id": 17100, "status": "active", "nickname": x.nick, "product_id": 1211})
	case p == "cloud/services/17100/label" && r.Method == "PUT":
		x.labels++
		x.nick, _ = body["nickname"].(string)
		jsonOut(w, 200, map[string]any{"nickname": x.nick})
	case p == "cloud/containers/17100/nodes":
		x.extra = int64(body["extra_nodes"].(float64))
		jsonOut(w, 200, map[string]any{"status": "accepted"})
	case p == "cloud/containers/17100" && r.Method == "GET":
		jsonOut(w, 200, map[string]any{"code": "ab12cd", "namespace": "t-canli-17100", "mode": "dedicated", "egress_ips": []string{"192.0.2.10"}, "extra_nodes": x.extra,
			"node_count": 4 + x.extra, "egress_hold_hours": 0})
	case p == "cloud/containers/17100" && r.Method == "DELETE":
		x.alive = false
		jsonOut(w, 200, map[string]any{"status": "cancelled"})
	default:
		jsonErr(w, 404, "not_found", p)
	}
}

func TestAccMockContainerEnvNameAndPlanImport(t *testing.T) {
	x := &envLabelMock{}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token    = \"wck_testA\"\n  poll_interval_seconds = 1\n}\n", srv.URL+"/api/v1/client")
	cfg := func(extra string) string {
		return prov + "resource \"gyro_container_environment\" \"e\" {\n  plan = \"pro\"\n" + extra + "}\n"
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(""), Check: resource.TestCheckResourceAttr("gyro_container_environment.e", "plan", "pro")},
			// the customer names it in the panel; Terraform has no name: an update must not clear it
			{PreConfig: func() { x.mu.Lock(); x.nick = "gyro-proje"; x.mu.Unlock() }, Config: cfg("  extra_nodes = 1\n"), Check: func(*terraform.State) error {
				if x.nick != "gyro-proje" || x.labels != 0 {
					return fmt.Errorf("panel name lost: %q (label calls %d)", x.nick, x.labels)
				}
				return nil
			}},
			{Config: cfg("  extra_nodes = 1\n"), PlanOnly: true},
			// import: plan comes from the product, name from the panel; the plan afterwards is empty
			{ResourceName: "gyro_container_environment.e", ImportState: true, ImportStateId: "17100", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"egress_approval_hours"}},
			{Config: cfg("  name = \"gyro-proje-2\"\n  extra_nodes = 1\n"), Check: func(*terraform.State) error {
				if x.nick != "gyro-proje-2" || x.labels != 1 {
					return fmt.Errorf("rename: %q (label calls %d)", x.nick, x.labels)
				}
				return nil
			}},
		},
	})
}

// #3462-A: gyro_storage keeps the panel name when `name` is not configured.
type storageLabelMock struct {
	mu     sync.Mutex
	nick   string
	labels int
	alive  bool
}

func (x *storageLabelMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/client/")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == "POST" && p == "cloud/storage":
		x.alive = true
		jsonOut(w, 201, map[string]any{"storage": map[string]any{"id": 3100}})
	case p == "cloud/storage/3100" && r.Method == "GET":
		if !x.alive {
			jsonErr(w, 404, "not_found", "x")
			return
		}
		jsonOut(w, 200, map[string]any{"id": 3100, "status": "active", "product_id": 923, "nickname": x.nick, "cycle": "monthly", "due_date": "2026-12-15",
			"storage": map[string]any{"plan": "standard-1tb", "kind": "standard", "size_gb": 1024, "max_buckets": 100, "included_traffic_gb_month": 1024, "endpoint": "https://s3.example.com", "region": "tr-ist-1"}})
	case p == "cloud/services/3100/label" && r.Method == "PUT":
		x.labels++
		x.nick, _ = body["nickname"].(string)
		jsonOut(w, 200, map[string]any{"nickname": x.nick})
	case p == "cloud/storage/3100" && r.Method == "DELETE":
		x.alive = false
		jsonOut(w, 200, map[string]any{"status": "cancelled"})
	default:
		jsonErr(w, 404, "not_found", p)
	}
}

func TestAccMockStorageServiceKeepsName(t *testing.T) {
	x := &storageLabelMock{}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token    = \"wck_testA\"\n  poll_interval_seconds = 1\n}\n", srv.URL+"/api/v1/client")
	cfg := func(extra string) string {
		return prov + "resource \"gyro_storage\" \"s\" {\n  plan = \"standard-1tb\"\n" + extra + "}\n"
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("")},
			{PreConfig: func() { x.mu.Lock(); x.nick = "gyro-proje-yedek"; x.mu.Unlock() }, Config: cfg("  delete_immediately = false\n"), Check: func(*terraform.State) error {
				if x.nick != "gyro-proje-yedek" || x.labels != 0 {
					return fmt.Errorf("panel name lost: %q (label calls %d)", x.nick, x.labels)
				}
				return nil
			}},
			{Config: cfg("  delete_immediately = false\n"), PlanOnly: true},
			{Config: cfg("  name = \"gyro-proje\"\n  delete_immediately = false\n"), Check: resource.TestCheckResourceAttr("gyro_storage.s", "name", "gyro-proje")},
		},
	})
}

// #3462-A: gyro_container_service with notification { min_ready = 0 }: import reads the 0, the plan afterwards is empty.
func TestAccMockContainerServiceMinReadyZeroImport(t *testing.T) {
	cm := newContainerMock()
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := prov + `
resource "gyro_container_service" "api" {
  service_id   = 7
  name         = "api"
  image        = "ghcr.io/acme/api:1"
  port         = 8080
  notification = { min_ready = 0 }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr("gyro_container_service.api", "notification.min_ready", "0")},
			{ResourceName: "gyro_container_service.api", ImportState: true, ImportStateId: "7/api", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"state"}},
			{Config: cfg, PlanOnly: true},
		},
	})
}
