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
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// imageMock: GyroAPI 2.22.0 registry import routes of one environment (#7, code ab12cd) in memory.
type imageMock struct {
	mu     sync.Mutex
	images map[string]map[string]any // repo:tag -> image
	jobs   []map[string]any
	polls  int
	bodies []map[string]any
}

func (m *imageMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/client/cloud/containers/")
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
	switch {
	case p == "7/registry/imports" && r.Method == "POST":
		m.bodies = append(m.bodies, body)
		src := body["source"].(string)
		repo, _ := body["repo"].(string)
		tag, _ := body["tag"].(string)
		if repo == "" {
			repo = "nginx"
		}
		if tag == "" {
			tag = "1.27"
		}
		if _, ex := m.images[repo+":"+tag]; ex && body["overwrite"] != true {
			fail(422, "tool_action_failed")
			return
		}
		st := "done"
		if strings.Contains(src, "yok") {
			st = "failed"
		}
		j := map[string]any{"id": fmt.Sprint("imp-", len(m.jobs)), "source": "docker.io/library/" + src, "target": "registry.ist.gyrohosting.com/ab12cd/" + repo + ":" + tag,
			"repo": "ab12cd/" + repo, "tag": tag, "state": "running", "digest": "sha256:" + strings.Repeat("a", 64), "bytes_total": 4096, "_final": st}
		m.jobs = append([]map[string]any{j}, m.jobs...)
		ok(map[string]any{"job": j})
	case p == "7/registry/imports" && r.Method == "GET":
		m.polls++
		for _, j := range m.jobs {
			if j["state"] == "running" {
				j["state"] = j["_final"]
				if j["state"] == "done" {
					m.images[strings.TrimPrefix(j["repo"].(string), "ab12cd/")+":"+j["tag"].(string)] = map[string]any{"repo": j["repo"], "tag": j["tag"], "digest": j["digest"], "bytes": 4096}
				} else {
					j["error"], j["message"] = "source_not_found", "image or tag not found in the source registry"
				}
			}
		}
		ok(map[string]any{"jobs": m.jobs})
	case p == "7/registry/images" && r.Method == "GET":
		l := []any{}
		for _, im := range m.images {
			l = append(l, im)
		}
		ok(map[string]any{"images": l})
	case p == "7/registry/images" && r.Method == "DELETE":
		if r.URL.Query().Get("confirm") != "true" {
			fail(422, "confirm_required")
			return
		}
		delete(m.images, r.URL.Query().Get("repo")+":"+r.URL.Query().Get("tag"))
		ok(map[string]any{"status": "successful"})
	default:
		fail(404, "service_not_found")
	}
}

func TestAccMockContainerImage(t *testing.T) {
	importPoll = 10 * time.Millisecond
	im := &imageMock{images: map[string]map[string]any{}}
	srv := httptest.NewServer(im)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(extra string) string {
		return prov + `resource "gyro_container_image" "nginx" {
  service_id = 7
  source     = "nginx:1.27"
` + extra + `}
`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(""), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_image.nginx", "id", "7/nginx:1.27"),
				resource.TestCheckResourceAttr("gyro_container_image.nginx", "image", "registry.ist.gyrohosting.com/ab12cd/nginx:1.27"),
				resource.TestCheckResourceAttr("gyro_container_image.nginx", "repo", "nginx"),
				resource.TestCheckResourceAttr("gyro_container_image.nginx", "bytes", "4096"),
			)},
			{Config: cfg(""), PlanOnly: true},
			// credentials change: no re-import
			{Config: cfg("  username = \"gyro-ornek\"\n  password = \"tok\"\n"), Check: resource.TestCheckResourceAttr("gyro_container_image.nginx", "id", "7/nginx:1.27")},
			// new tag: replace (new import, old tag deleted)
			{Config: cfg("  tag = \"kopya\"\n  username = \"gyro-ornek\"\n  password = \"tok\"\n"), Check: resource.TestCheckResourceAttr("gyro_container_image.nginx", "image", "registry.ist.gyrohosting.com/ab12cd/nginx:kopya")},
			// deleted outside Terraform -> imported again
			{PreConfig: func() { im.mu.Lock(); delete(im.images, "nginx:kopya"); im.mu.Unlock() },
				Config: cfg("  tag = \"kopya\"\n  username = \"gyro-ornek\"\n  password = \"tok\"\n"), Check: resource.TestCheckResourceAttr("gyro_container_image.nginx", "tag", "kopya")},
			{Config: prov + `resource "gyro_container_image" "bad" {
  service_id = 7
  source     = "yok/uygulama:1"
  tag        = "x"
}`, ExpectError: regexp.MustCompile(`source_not_found`)},
		},
	})
	if len(im.images) != 0 {
		t.Fatalf("destroy must delete the tags: %v", im.images)
	}
	if im.bodies[2]["password"] != "tok" || im.bodies[0]["password"] != nil {
		t.Fatalf("credentials sent only when set: %v", im.bodies)
	}
}
