package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// containerMock: the typed /cloud/containers routes of GyroAPI (GyroKapsayici) in memory, enough for the resource.
type containerMock struct {
	mu      sync.Mutex
	svcs    map[string]map[string]any // name -> spec
	rev     map[string]int
	secrets map[string]map[string]string
	domains map[string][]string
	calls   []string
	ntf     map[string]any
}

func newContainerMock() *containerMock {
	return &containerMock{svcs: map[string]map[string]any{}, rev: map[string]int{}, secrets: map[string]map[string]string{}, domains: map[string][]string{}}
}

func (m *containerMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/client/cloud/containers/")
	seg := strings.Split(p, "/")
	m.calls = append(m.calls, r.Method+" "+p)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	ok := func(d any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": d})
	}
	fail := func(code int, c string) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": c, "message": c}})
	}
	if seg[0] == "7" && len(seg) >= 2 && seg[1] == "notifications" {
		if m.ntf == nil {
			m.ntf = map[string]any{"email": 1, "sms": 0, "telegram": "", "webhook_url": "", "cooldown": 30, "resolved": 1, "secret": ""}
		}
		b01 := func(v any) int {
			if v == true || v == float64(1) {
				return 1
			}
			return 0
		}
		switch {
		case len(seg) == 2 && r.Method == "PUT":
			for _, k := range []string{"email", "sms", "resolved"} {
				if v, ok := body[k]; ok {
					m.ntf[k] = b01(v)
				}
			}
			for _, k := range []string{"telegram", "webhook_url", "events"} {
				if v, ok := body[k]; ok {
					m.ntf[k] = v
				}
			}
			if v, ok := body["cooldown"]; ok {
				m.ntf["cooldown"] = int(v.(float64))
			}
			if m.ntf["webhook_url"] != "" && m.ntf["secret"] == "" {
				m.ntf["secret"] = "whsec_mock1"
			}
			ok(map[string]any{"status": "successful"})
		case len(seg) == 2 && r.Method == "GET":
			ev := map[string]int{}
			for _, e := range strings.Split(fmt.Sprint(m.ntf["events"]), ",") {
				ev[e] = 1
			}
			ok(map[string]any{"settings": map[string]any{"email": m.ntf["email"], "sms": m.ntf["sms"], "telegram": m.ntf["telegram"], "webhook_url": m.ntf["webhook_url"],
				"events": ev, "cooldown": m.ntf["cooldown"], "resolved": m.ntf["resolved"], "webhook_secret_set": m.ntf["secret"] != ""}, "conditions": []any{}})
		case len(seg) == 3 && seg[2] == "webhook-secret":
			ok(map[string]any{"secret": m.ntf["secret"]})
		default:
			fail(404, "not_found")
		}
		return
	}
	if seg[0] == "7" && len(seg) == 1 && r.Method == "GET" { // API 2.27.0 environment detail
		ok(map[string]any{"code": "abcd12", "namespace": "t-canli-7", "mode": "dedicated", "host_suffix": "-abcd12.gyroapp.net", "suspended": false,
			"cluster_dns": map[string]any{"service_domain": "t-canli-7.svc.cluster.local"}, "egress_ips": []string{"192.0.2.10", "192.0.2.68"},
			"egress_note": "IPs may change", "egress": map[string]any{"hash": "aa11", "build_ips": []string{"192.0.2.112"}}, "ingress_ips": []string{"192.0.2.121"}})
		return
	}
	if seg[0] != "7" || len(seg) < 2 || seg[1] != "services" {
		fail(404, "service_not_found")
		return
	}
	view := func(n string) map[string]any {
		keys := []string{}
		for k := range m.secrets[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ds := []any{}
		for _, d := range m.domains[n] {
			ds = append(ds, map[string]any{"name": d, "status": "pending"})
		}
		sp := m.svcs[n]
		out := map[string]any{"service_id": 7, "name": n, "spec": sp, "revision": m.rev[n], "state": "running", "domains": ds, "secret_keys": keys}
		if sp["public"] == true {
			out["url"], out["host"] = "https://"+n+"-abcd12.gyroapp.net", n+"-abcd12.gyroapp.net"
		}
		out["namespace"] = "t-canli-7"
		if p, _ := sp["port"].(float64); p > 0 {
			out["internal"] = map[string]any{"address": n + ".t-canli-7.svc.cluster.local:80", "short": n, "port": 80}
		}
		return out
	}
	if len(seg) == 2 && r.Method == "POST" {
		n := body["name"].(string)
		if _, ex := m.svcs[n]; ex {
			fail(422, "svc_exists")
			return
		}
		delete(body, "name")
		body["name"] = n
		if body["run_as_user"] == float64(0) {
			body["run_as_user"] = float64(0)
		}
		if a, _ := body["autoscaling"].(map[string]any); a != nil && a["enabled"] == false {
			delete(body, "autoscaling")
		}
		m.svcs[n], m.rev[n] = body, 1
		ok(view(n))
		return
	}
	if len(seg) == 2 && r.Method == "GET" {
		l := []any{}
		for n := range m.svcs {
			l = append(l, map[string]any{"name": n})
		}
		ok(map[string]any{"services": l})
		return
	}
	n := seg[2]
	if _, ex := m.svcs[n]; !ex {
		fail(422, "tool_action_failed") // like the bridge: the module reports "service not found"
		return
	}
	switch {
	case len(seg) == 3 && r.Method == "GET":
		ok(view(n))
	case len(seg) == 3 && r.Method == "PATCH":
		sp := body["spec"].(map[string]any)
		onlySettings := true
		for k, v := range sp {
			if k != "autoscaling" && k != "alerts" && k != "replicas" && fmt.Sprint(m.svcs[n][k]) != fmt.Sprint(v) {
				onlySettings = false
			}
			m.svcs[n][k] = v
		}
		if a, _ := sp["autoscaling"].(map[string]any); a != nil && a["enabled"] == false {
			delete(m.svcs[n], "autoscaling")
		}
		if !onlySettings {
			m.rev[n]++
		}
		ok(view(n))
	case len(seg) == 3 && r.Method == "DELETE":
		if body["confirm"] != true {
			fail(422, "confirm_required")
			return
		}
		delete(m.svcs, n)
		ok(map[string]any{"deleted": true})
	case len(seg) == 4 && seg[3] == "secrets" && r.Method == "PUT":
		if m.secrets[n] == nil {
			m.secrets[n] = map[string]string{}
		}
		for k, v := range body["set"].(map[string]any) {
			m.secrets[n][k] = fmt.Sprint(v)
		}
		ok(view(n))
	case len(seg) == 4 && seg[3] == "secrets" && r.Method == "DELETE":
		for _, k := range strings.Split(r.URL.Query().Get("keys"), ",") {
			delete(m.secrets[n], k)
		}
		ok(view(n))
	case len(seg) == 4 && seg[3] == "domains" && r.Method == "POST":
		m.domains[n] = append(m.domains[n], body["domain"].(string))
		ok(map[string]any{"domain": map[string]any{"name": body["domain"], "status": "pending"}})
	case len(seg) == 5 && seg[3] == "domains" && r.Method == "DELETE":
		out := []string{}
		for _, d := range m.domains[n] {
			if d != seg[4] {
				out = append(out, d)
			}
		}
		m.domains[n] = out
		ok(map[string]any{"deleted": true})
	default:
		fail(404, "not_found")
	}
}

func TestAccMockContainerService(t *testing.T) {
	cm := newContainerMock()
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(image string, replicas int, secret string, domains string) string {
		return prov + fmt.Sprintf(`
resource "gyro_container_service" "web" {
  service_id  = 7
  name        = "web"
  image       = %q
  port        = 8080
  replicas    = %d
  public      = true
  health_path = "/healthz"
  env         = { MODE = "prod" }
  secrets     = { DB_PASSWORD = %q }
  domains     = [%s]
}
`, image, replicas, secret, domains)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("ghcr.io/acme/web:1", 2, "s1", `"www.example.com"`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_service.web", "id", "7/web"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "revision", "1"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "host", "web-abcd12.gyroapp.net"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "domains.#", "1"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "secrets.DB_PASSWORD", "s1"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "internal_address", "web.t-canli-7.svc.cluster.local:80"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "namespace", "t-canli-7"),
			)},
			{Config: cfg("ghcr.io/acme/web:1", 2, "s1", `"www.example.com"`), PlanOnly: true},
			{Config: cfg("ghcr.io/acme/web:2", 3, "s2", `"app.example.com"`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_service.web", "image", "ghcr.io/acme/web:2"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "replicas", "3"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "revision", "2"),
				resource.TestCheckResourceAttr("gyro_container_service.web", "domains.0", "app.example.com"),
			)},
			{PreConfig: func() {
				cm.mu.Lock()
				delete(cm.svcs, "web")
				delete(cm.domains, "web")
				delete(cm.secrets, "web")
				cm.mu.Unlock()
			}, // deleted outside Terraform -> recreated
				Config: cfg("ghcr.io/acme/web:2", 3, "s2", `"app.example.com"`), Check: resource.TestCheckResourceAttr("gyro_container_service.web", "revision", "1")},
			{ResourceName: "gyro_container_service.web", ImportState: true, ImportStateId: "7/web", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"secrets", "state"}},
			{Config: prov + `resource "gyro_container_service" "bad" {
  service_id = 8
  name       = "x"
  image      = "nginx"
}`, ExpectError: regexp.MustCompile(`service_not_found`)},
		},
	})
	for _, c := range cm.calls {
		if strings.Contains(c, "secrets") && strings.Contains(c, "s2") {
			t.Fatal("secret value in path")
		}
	}
}

func TestAccMockContainerAutoscalingNotifications(t *testing.T) {
	cm := newContainerMock()
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	svc := func(as string) string {
		return prov + `
resource "gyro_container_service" "api" {
  service_id = 7
  name       = "api"
  image      = "ghcr.io/acme/api:1"
  port       = 8080
` + as + `
}
`
	}
	ntf := `
resource "gyro_container_notifications" "n" {
  service_id  = 7
  email       = true
  telegram    = "123456789"
  webhook_url = "https://hooks.example.com/gyro"
  events      = ["down", "crashloop", "capacity"]
  cooldown    = 15
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: svc(`  autoscaling = { min = 1, max = 4 }
  notification = { min_ready = 1 }`) + ntf, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_service.api", "autoscaling.max", "4"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "autoscaling.cpu_percent", "70"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "notification.min_ready", "1"),
				resource.TestCheckResourceAttr("gyro_container_notifications.n", "events.#", "3"),
				resource.TestCheckResourceAttr("gyro_container_notifications.n", "webhook_secret", "whsec_mock1"),
				resource.TestCheckResourceAttr("gyro_container_notifications.n", "cooldown", "15"),
			)},
			{Config: svc(`  autoscaling = { min = 1, max = 4 }
  notification = { min_ready = 1 }`) + ntf, PlanOnly: true},
			{Config: svc(`  autoscaling = { min = 2, max = 6, cpu_percent = 60, memory_percent = 80 }`) + ntf, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_service.api", "autoscaling.min", "2"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "autoscaling.memory_percent", "80"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "revision", "1"), // settings are not a revision
			)},
			{Config: svc(`  replicas = 3`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckNoResourceAttr("gyro_container_service.api", "autoscaling.min"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "replicas", "3"),
			)},
			{ResourceName: "gyro_container_service.api", ImportState: true, ImportStateId: "7/api", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"state"}},
		},
	})
	sent := false
	for _, c := range cm.calls {
		if strings.HasPrefix(c, "PUT 7/notifications") {
			sent = true
		}
	}
	if !sent {
		t.Fatal("notifications were never written")
	}
}

func TestAccMockContainerEnvironmentDataSource(t *testing.T) {
	srv := httptest.NewServer(newContainerMock())
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{Config: prov + `data "gyro_container_environment" "e" { service_id = 7 }`, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "namespace", "t-canli-7"),
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "service_domain", "t-canli-7.svc.cluster.local"),
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "egress_ips.#", "2"),
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "egress_ips.1", "192.0.2.68"),
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "build_egress_ips.0", "192.0.2.112"),
			resource.TestCheckResourceAttr("data.gyro_container_environment.e", "mode", "dedicated"),
		)}},
	})
}
