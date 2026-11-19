package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// API 2.43.0 gyro_storage_bucket_alert: PUT sets / replaces, Read drops it when the alert was removed elsewhere, import <service>/<bucket>, destroy = DELETE.

type alMock struct {
	mu     sync.Mutex
	alerts map[string]map[string]any
	calls  []string
}

var reAlPath = regexp.MustCompile(`/cloud/storage/5/buckets/([a-z0-9-]+)/alert$`)

func (x *alMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	x.calls = append(x.calls, r.Method+" "+r.URL.Path)
	mt := reAlPath.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		jsonErr(w, 404, "not_found", "x")
		return
	}
	b := mt[1]
	item := func() map[string]any {
		a, ok := x.alerts[b]
		if !ok {
			return map[string]any{"bucket": b, "enabled": false, "type": nil, "value": nil, "threshold_gb": nil, "state": "off", "last_sent_at": nil}
		}
		v := a["value"].(float64)
		th := v
		if a["type"] == "percent" {
			th = 250 * v / 100
		}
		return map[string]any{"bucket": b, "enabled": true, "type": a["type"], "value": v, "threshold_gb": th, "state": "armed", "last_sent_at": nil}
	}
	switch r.Method {
	case "GET":
		jsonOut(w, 202, map[string]any{"item": item()})
	case "PUT":
		t, _ := body["type"].(string)
		v, _ := body["value"].(float64)
		if (t == "percent" && (v < 1 || v > 100)) || (t == "gb" && (v < 1 || v > 250)) {
			jsonErr(w, 422, "tool_action_failed", "Yüzde 1 ile 100 arasında olmalı")
			return
		}
		x.alerts[b] = map[string]any{"type": t, "value": v}
		jsonOut(w, 202, map[string]any{"status": "successful", "item": item()})
	case "DELETE":
		delete(x.alerts, b)
		jsonOut(w, 202, map[string]any{"status": "successful", "item": item()})
	}
}

func TestAccMockStorageBucketAlert(t *testing.T) {
	x := &alMock{alerts: map[string]map[string]any{}}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(tp string, v int) string {
		return prov + fmt.Sprintf(`
resource "gyro_storage_bucket_alert" "dolu" {
  service_id = 5
  bucket     = "gyro-proje"
  type       = %q
  value      = %d
}`, tp, v)
	}
	const n = "gyro_storage_bucket_alert.dolu"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.alerts) != 0 {
				return fmt.Errorf("left over alerts: %d", len(x.alerts))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("percent", 101), ExpectError: regexp.MustCompile(`1 ile 100`)},
			{Config: cfg("tb", 1), ExpectError: regexp.MustCompile(`(?s)value must be one of`)},
			{Config: cfg("percent", 80), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(n, "id", "5/gyro-proje"),
				resource.TestCheckResourceAttr(n, "threshold_gb", "200"),
				resource.TestCheckResourceAttr(n, "state", "armed"),
				resource.TestCheckResourceAttr(n, "last_sent_at", ""),
			)},
			{Config: cfg("gb", 50), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(n, "type", "gb"),
				resource.TestCheckResourceAttr(n, "threshold_gb", "50"),
			)},
			{ResourceName: n, ImportState: true, ImportStateId: "5/gyro-proje", ImportStateVerify: true},
			// removed outside Terraform -> planned again
			{PreConfig: func() { x.mu.Lock(); delete(x.alerts, "gyro-proje"); x.mu.Unlock() }, Config: cfg("gb", 50),
				Check: resource.TestCheckResourceAttr(n, "value", "50")},
		},
	})
}
