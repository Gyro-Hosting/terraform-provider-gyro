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

// pipelineMock: GyroAPI 2.23.0 pipeline routes of environment #7 (code ab12cd) in memory.
type pipelineMock struct {
	mu      sync.Mutex
	p       map[string]any
	secrets map[string]string
	token   string
}

func (m *pipelineMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/client/cloud/containers/")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	ok := func(d any) { json.NewEncoder(w).Encode(map[string]any{"data": d}) }
	nf := func() {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found", "message": "pipeline_not_found"}})
	}
	view := func() map[string]any {
		v := map[string]any{}
		for k, x := range m.p {
			v[k] = x
		}
		names := []string{}
		for k := range m.secrets {
			names = append(names, k)
		}
		v["secret_names"], v["has_token"] = names, m.token != ""
		return v
	}
	merge := func() {
		for k, x := range body {
			switch k {
			case "token":
				if x == "-" {
					m.token = ""
				} else {
					m.token = fmt.Sprint(x)
				}
			case "confirm":
			case "notify":
				n := map[string]any{}
				for _, i := range x.([]any) {
					n[fmt.Sprint(i)] = true
				}
				m.p["notify"] = n
			default:
				m.p[k] = x
			}
		}
		for k, d := range map[string]any{"cpu_m": 2000, "memory_mi": 4096, "timeout_min": 20, "verify_min": 5} {
			if _, ok := m.p[k]; !ok {
				m.p[k] = d
			}
		}
	}
	switch {
	case p == "7/pipelines" && r.Method == "POST":
		m.p = map[string]any{"id": "pabcd1234", "webhook_url": "https://ecs-api.ist.gyrohosting.com/hook/ab12cd/pabcd1234", "registry_image": "registry.ist.gyrohosting.com/ab12cd/web"}
		m.secrets = map[string]string{}
		merge()
		v := view()
		v["webhook_secret"] = "gph_secret"
		ok(map[string]any{"pipeline": v})
	case m.p == nil:
		nf()
	case p == "7/pipelines/pabcd1234" && r.Method == "GET":
		ok(view())
	case p == "7/pipelines/pabcd1234/webhook":
		ok(map[string]any{"webhook_url": m.p["webhook_url"], "webhook_secret": "gph_secret"})
	case p == "7/pipelines/pabcd1234" && r.Method == "PATCH":
		merge()
		ok(map[string]any{"pipeline": view()})
	case p == "7/pipelines/pabcd1234/secrets" && r.Method == "PUT":
		for k, v := range body["set"].(map[string]any) {
			m.secrets[k] = fmt.Sprint(v)
		}
		ok(map[string]any{})
	case strings.HasPrefix(p, "7/pipelines/pabcd1234/secrets/") && r.Method == "DELETE":
		delete(m.secrets, strings.TrimPrefix(p, "7/pipelines/pabcd1234/secrets/"))
		ok(map[string]any{})
	case p == "7/pipelines/pabcd1234" && r.Method == "DELETE":
		if body["confirm"] != true {
			w.WriteHeader(422)
			return
		}
		m.p = nil
		ok(map[string]any{"deleted": true})
	default:
		nf()
	}
}

func TestAccMockContainerPipeline(t *testing.T) {
	pm := &pipelineMock{}
	srv := httptest.NewServer(pm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(extra string) string {
		return prov + `resource "gyro_container_pipeline" "web" {
  service_id = 7
  name       = "web"
  repo       = "https://github.com/gyro-ornek/uygulama"
  image      = "web"
  targets    = ["web"]
` + extra + `}
`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("  token = \"tok1\"\n  secrets = { NPM_TOKEN = \"s1\" }\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_pipeline.web", "id", "7/pabcd1234"),
				resource.TestCheckResourceAttr("gyro_container_pipeline.web", "webhook_secret", "gph_secret"),
				resource.TestCheckResourceAttr("gyro_container_pipeline.web", "branch", "main"),
				resource.TestCheckResourceAttr("gyro_container_pipeline.web", "cpu_m", "2000"),
				func(*terraform.State) error {
					if pm.token != "tok1" || pm.secrets["NPM_TOKEN"] != "s1" {
						return fmt.Errorf("token/secret not sent")
					}
					return nil
				})},
			{Config: cfg("  token = \"tok1\"\n  secrets = { NPM_TOKEN = \"s1\" }\n"), PlanOnly: true},
			{Config: cfg("  tags = \"v*\"\n  approval = true\n  commands = [\"npm ci\"]\n  build_image = \"node:22-alpine\"\n  secrets = { OTHER = \"x\" }\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_container_pipeline.web", "tags", "v*"),
					resource.TestCheckResourceAttr("gyro_container_pipeline.web", "commands.0", "npm ci"),
					func(*terraform.State) error {
						if pm.token != "" || pm.secrets["OTHER"] != "x" || pm.secrets["NPM_TOKEN"] != "" {
							return fmt.Errorf("token %q secrets %v", pm.token, pm.secrets)
						}
						return nil
					})},
			{ResourceName: "gyro_container_pipeline.web", ImportState: true, ImportStateId: "7/pabcd1234", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"token", "secrets"}},
		},
	})
}
