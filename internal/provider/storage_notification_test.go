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

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// GYR-18 (API 2.37.0) gyro_storage_bucket_notification: create shows the secret once, extensions are stored lower case (no diff),
// enabled=false -> status disabled, import by <service>/<webhook>, destroy sends confirm.

type evMock struct {
	mu    sync.Mutex
	hooks map[int]map[string]any
	n     int
	calls []string
}

var reEvPath = regexp.MustCompile(`/cloud/storage/5/(?:buckets/([a-z0-9-]+)/events|events/(\d+))$`)

func (x *evMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	bb, _ := json.Marshal(body)
	x.calls = append(x.calls, r.Method+" "+r.URL.Path+" "+string(bb))
	mt := reEvPath.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		jsonErr(w, 404, "not_found", "x")
		return
	}
	norm := func(h map[string]any, b map[string]any) {
		for _, k := range []string{"url", "prefix", "description", "status"} {
			if v, ok := b[k]; ok {
				h[k] = v
			}
		}
		if v, ok := b["events"].([]any); ok && len(v) > 0 {
			h["events"] = v
		}
		if v, ok := b["suffix"].([]any); ok {
			out := []any{}
			for _, s := range v {
				out = append(out, strings.ToLower(fmt.Sprint(s)))
			}
			h["suffix"] = out
		}
		if h["status"] == "disabled" {
			h["disabled_reason"] = "user"
		} else {
			h["disabled_reason"] = nil
		}
	}
	if mt[1] != "" && r.Method == "POST" {
		if u := fmt.Sprint(body["url"]); !strings.HasPrefix(u, "https://") {
			jsonErr(w, 422, "tool_action_failed", "Hedef adres https:// ile başlayan geçerli bir adres olmalı")
			return
		}
		x.n++
		h := map[string]any{"id": x.n, "bucket": mt[1], "events": []any{"storage.object.created", "storage.object.deleted", "storage.objects.deleted_bulk"}, "suffix": []any{}, "status": "active"}
		norm(h, body)
		x.hooks[x.n] = h
		out := map[string]any{}
		for k, v := range h {
			out[k] = v
		}
		out["secret"] = strings.Repeat("ab", 32)
		jsonOut(w, 202, map[string]any{"item": out, "applied": true})
		return
	}
	var id int
	fmt.Sscan(mt[2], &id)
	h, ok := x.hooks[id]
	if !ok {
		jsonErr(w, 422, "tool_action_failed", "Webhook bulunamadı.")
		return
	}
	switch r.Method {
	case "GET":
		jsonOut(w, 202, map[string]any{"item": h})
	case "PATCH":
		norm(h, body)
		jsonOut(w, 202, map[string]any{"item": h, "applied": true})
	case "DELETE":
		if body["confirm"] != true {
			jsonErr(w, 422, "confirm_required", "confirm")
			return
		}
		delete(x.hooks, id)
		jsonOut(w, 202, map[string]any{"deleted": true, "id": id})
	}
}

func TestAccMockStorageBucketNotification(t *testing.T) {
	x := &evMock{hooks: map[int]map[string]any{}}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(extra string) string {
		return prov + `
resource "gyro_storage_bucket_notification" "olay" {
  service_id = 5
  bucket     = "gyro-proje"
` + extra + `
}`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.hooks) != 0 {
				return fmt.Errorf("left over webhooks: %d", len(x.hooks))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg(`  url = "http://ornek.com/x"`), ExpectError: regexp.MustCompile(`https://`)},
			{Config: cfg(`  url    = "https://ornek.com/s3-olay"
  prefix = "resimler/"
  suffix = [".JPG", ".png"]`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "webhook_id", "1"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "id", "5/1"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "secret", strings.Repeat("ab", 32)),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "events.#", "3"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "suffix.0", ".JPG"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "status", "active"),
			)},
			{Config: cfg(`  url     = "https://ornek.com/s3-olay-2"
  events  = ["storage.object.created"]
  prefix  = "resimler/"
  suffix  = [".JPG", ".png"]
  enabled = false`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "status", "disabled"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "disabled_reason", "user"),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "secret", strings.Repeat("ab", 32)),
				resource.TestCheckResourceAttr("gyro_storage_bucket_notification.olay", "events.#", "1"),
			)},
			{ResourceName: "gyro_storage_bucket_notification.olay", ImportState: true, ImportStateId: "5/1", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"secret", "suffix", "suffix.0"}},
		},
	})
}
