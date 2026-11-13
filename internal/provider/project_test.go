package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// API 2.18.0: projects. A small wrapper around the mock API serves /cloud/projects*, /cloud/services/{id}/project and
// the project field of GET /cloud/services/{id}; POST /cloud/servers and /cloud/load-balancers with project_id are recorded.
type projMock struct {
	mu       sync.Mutex
	next     int64
	projects map[int64]map[string]any
	svc      map[int64]int64 // service id -> project id
	pending  int64           // project_id of the last order, applied to the next unknown service id
	base     http.Handler
}

var (
	reProj    = regexp.MustCompile(`/cloud/projects(?:/(\d+))?$`)
	reSvcProj = regexp.MustCompile(`/cloud/services/(\d+)/project$`)
	reSvcGet  = regexp.MustCompile(`/cloud/services/(\d+)$`)
)

func (x *projMock) write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func (x *projMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r.Method == http.MethodPost && (regexp.MustCompile(`/cloud/(servers|load-balancers)$`).MatchString(r.URL.Path)) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		if v, ok := body["project_id"].(float64); ok {
			if _, ok := x.projects[int64(v)]; !ok && v != 0 {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"code":"project_not_found","message":"Project not found."}}`))
				return
			}
			x.pending = int64(v)
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		x.base.ServeHTTP(w, r)
		return
	}
	if mt := reProj.FindStringSubmatch(r.URL.Path); mt != nil {
		id, _ := strconv.ParseInt(mt[1], 10, 64)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == http.MethodGet && mt[1] == "":
			out := []any{map[string]any{"id": 0, "name": "General", "is_default": true, "color": "gray", "icon": "inbox"}}
			for _, p := range x.projects {
				out = append(out, p)
			}
			x.write(w, 200, out)
		case r.Method == http.MethodPost && mt[1] == "":
			x.next++
			p := map[string]any{"id": x.next, "name": body["name"], "description": body["description"], "color": body["color"], "icon": body["icon"], "is_default": false, "service_count": 0}
			x.projects[x.next] = p
			x.write(w, 201, p)
		case x.projects[id] == nil:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"project_not_found","message":"Project not found."}}`))
		case r.Method == http.MethodGet:
			n := 0
			for _, pid := range x.svc {
				if pid == id {
					n++
				}
			}
			x.projects[id]["service_count"] = n
			x.write(w, 200, x.projects[id])
		case r.Method == http.MethodPatch:
			for _, k := range []string{"name", "description", "color", "icon"} {
				if v, ok := body[k]; ok {
					x.projects[id][k] = v
				}
			}
			x.write(w, 200, x.projects[id])
		case r.Method == http.MethodDelete:
			delete(x.projects, id)
			for s, pid := range x.svc {
				if pid == id {
					x.svc[s] = 0
				}
			}
			x.write(w, 200, map[string]any{"id": id, "deleted": true})
		}
		return
	}
	if mt := reSvcProj.FindStringSubmatch(r.URL.Path); mt != nil && r.Method == http.MethodPut {
		sid, _ := strconv.ParseInt(mt[1], 10, 64)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		pid := int64(body["project_id"].(float64))
		if pid != 0 && x.projects[pid] == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"project_not_found","message":"Project not found."}}`))
			return
		}
		x.svc[sid] = pid
		x.write(w, 200, map[string]any{"id": sid, "project": map[string]any{"id": pid}})
		return
	}
	if mt := reSvcGet.FindStringSubmatch(r.URL.Path); mt != nil && r.Method == http.MethodGet {
		sid, _ := strconv.ParseInt(mt[1], 10, 64)
		pid, ok := x.svc[sid]
		if !ok {
			pid = x.pending
			x.svc[sid] = pid
			x.pending = 0
		}
		x.write(w, 200, map[string]any{"id": sid, "status": "active", "project": map[string]any{"id": pid}})
		return
	}
	x.base.ServeHTTP(w, r)
}

func TestAccMockProjects(t *testing.T) {
	x := &projMock{projects: map[int64]map[string]any{}, svc: map[int64]int64{}, base: mockapi.New()}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
	cfg := func(name, color, serverProject string) string {
		return prov + fmt.Sprintf(`
resource "gyro_project" "a" {
  name  = %q
  color = %q
  icon  = "rocket-takeoff"
}
resource "gyro_project" "b" {
  name        = "Gyro Test"
  description = "Mobile backend"
}
resource "gyro_server" "s" {
  plan          = "eko-2-4"
  region        = "eu-de-1"
  image         = "debian-12"
  confirm_order = true
  project_id    = %s
}
resource "gyro_service_project" "dns" {
  service_id = 9001
  project_id = tonumber(gyro_project.b.id)
}
`, name, color, serverProject)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("Gyro Proje", "blue", "tonumber(gyro_project.a.id)"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_project.a", "name", "Gyro Proje"),
				resource.TestCheckResourceAttr("gyro_project.b", "color", "blue"),
				resource.TestCheckResourceAttr("gyro_project.b", "icon", "folder"),
				resource.TestCheckResourceAttrPair("gyro_server.s", "project_id", "gyro_project.a", "id"),
				resource.TestCheckResourceAttrPair("gyro_service_project.dns", "project_id", "gyro_project.b", "id"),
			)},
			{ResourceName: "gyro_project.b", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"service_count"}},
			// rename + recolour in place; server moves to project b without replacement
			{Config: cfg("Gyro Proje 2", "teal", "tonumber(gyro_project.b.id)"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_project.a", "name", "Gyro Proje 2"),
				resource.TestCheckResourceAttr("gyro_project.a", "color", "teal"),
				resource.TestCheckResourceAttrPair("gyro_server.s", "project_id", "gyro_project.b", "id"),
				func(s *terraform.State) error {
					id, _ := strconv.ParseInt(s.RootModule().Resources["gyro_server.s"].Primary.ID, 10, 64)
					b, _ := strconv.ParseInt(s.RootModule().Resources["gyro_project.b"].Primary.ID, 10, 64)
					if x.svc[id] != b {
						return fmt.Errorf("server %d is in project %d, want %d", id, x.svc[id], b)
					}
					return nil
				},
			)},
			// back to the default project
			{Config: cfg("Gyro Proje 2", "teal", "0"), Check: resource.TestCheckResourceAttr("gyro_server.s", "project_id", "0")},
		},
	})
	if len(x.projects) != 0 {
		t.Fatalf("projects left after destroy: %d", len(x.projects))
	}
	if x.svc[9001] != 0 {
		t.Fatalf("gyro_service_project destroy must move the service back to the default project, got %d", x.svc[9001])
	}
}
