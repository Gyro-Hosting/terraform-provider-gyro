package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// API 2.2.0+: GET/PUT /cloud/services/{id}/label. The mock keeps labels per service id; ids >= 99900 are "other account" (404).
type labelMock struct {
	mu     sync.Mutex
	labels map[int64]map[string]any
	puts   int
	base   http.Handler
}

var reSvcLabel = regexp.MustCompile(`/cloud/services/(\d+)/label$`)

func (x *labelMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reSvcLabel.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	sid, _ := strconv.ParseInt(mt[1], 10, 64)
	w.Header().Set("Content-Type", "application/json")
	if sid >= 99900 {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"service_not_found","message":"Service not found."}}`))
		return
	}
	cur := x.labels[sid]
	if cur == nil {
		cur = map[string]any{"service_id": sid, "nickname": nil, "tags": []any{}}
	}
	if r.Method == http.MethodPut {
		x.puts++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if n, ok := body["nickname"]; ok {
			if s, _ := n.(string); s == "" {
				cur["nickname"] = nil
			} else {
				cur["nickname"] = s
			}
		}
		if t, ok := body["tags"]; ok {
			out := []any{}
			seen := map[string]bool{}
			list, _ := t.([]any)
			for _, it := range list {
				m, _ := it.(map[string]any)
				name, _ := m["name"].(string)
				if seen[strings.ToLower(name)] {
					continue
				}
				seen[strings.ToLower(name)] = true
				c, _ := m["color"].(string)
				if c == "" {
					c = "teal" // stable default of the API
				}
				out = append(out, map[string]any{"name": name, "color": c})
			}
			cur["tags"] = out
		}
		if cur["nickname"] == nil && len(cur["tags"].([]any)) == 0 {
			delete(x.labels, sid)
		} else {
			x.labels[sid] = cur
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": cur})
}

func TestAccMockServiceLabel(t *testing.T) {
	x := &labelMock{labels: map[int64]map[string]any{}, base: mockapi.New()}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: prov + `
resource "gyro_service_label" "api" {
  service_id = 12345
  nickname   = "api-gateway"
  tags = [
    { name = "production", color = "green" },
    { name = "api" },
  ]
}
resource "gyro_service_label" "ecs" {
  service_id = 12346
  tags       = [{ name = "staging", color = "orange" }]
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_service_label.api", "id", "12345"),
				resource.TestCheckResourceAttr("gyro_service_label.api", "nickname", "api-gateway"),
				resource.TestCheckResourceAttr("gyro_service_label.api", "tags.#", "2"),
				resource.TestCheckResourceAttr("gyro_service_label.api", "tags.0.color", "green"),
				resource.TestCheckResourceAttr("gyro_service_label.api", "tags.1.color", "teal"),
				resource.TestCheckNoResourceAttr("gyro_service_label.ecs", "nickname"),
				resource.TestCheckResourceAttr("gyro_service_label.ecs", "tags.0.name", "staging"),
			)},
			{ResourceName: "gyro_service_label.api", ImportState: true, ImportStateVerify: true},
			// rename + drop a tag in place
			{Config: prov + `
resource "gyro_service_label" "api" {
  service_id = 12345
  nickname   = "api.gateway"
  tags       = [{ name = "production", color = "green" }]
}
resource "gyro_service_label" "ecs" {
  service_id = 12346
  nickname   = "gyro-ecs-1"
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_service_label.api", "nickname", "api.gateway"),
				resource.TestCheckResourceAttr("gyro_service_label.api", "tags.#", "1"),
				resource.TestCheckResourceAttr("gyro_service_label.ecs", "nickname", "gyro-ecs-1"),
				resource.TestCheckNoResourceAttr("gyro_service_label.ecs", "tags.#"),
			)},
			// another account's service -> clear 404 error
			{Config: prov + `
resource "gyro_service_label" "x" {
  service_id = 99901
  nickname   = "nope"
}
`, ExpectError: regexp.MustCompile(`service_not_found|not found`)},
		},
	})
	if len(x.labels) != 0 {
		t.Fatalf("labels left after destroy: %v", x.labels)
	}
}
