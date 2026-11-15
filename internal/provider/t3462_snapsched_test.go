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

type snapSchedMock struct {
	mu   sync.Mutex
	base http.Handler
	sc   map[string]any // nil = not configured
	puts []map[string]any
}

var reSnapSched = regexp.MustCompile(`/cloud/servers/701/snapshot-schedule$`)

func (x *snapSchedMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !reSnapSched.MatchString(r.URL.Path) {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	out := func(d any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": d})
	}
	switch r.Method {
	case http.MethodPut:
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		x.puts = append(x.puts, b)
		if x.sc == nil {
			x.sc = map[string]any{"configured": true, "enabled": true, "frequency": "daily", "hour": 3, "minute": 0, "keep": 7}
		}
		if b["keep"] == float64(9) {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"tool_action_failed","message":"The number of snapshots to keep must be between 1 and the server's snapshot limit."}}`))
			return
		}
		for k, v := range b {
			x.sc[k] = v
		}
		if x.sc["frequency"] != "weekly" {
			x.sc["weekday"] = nil
		}
		x.sc["next_run_at"] = "2026-11-16T00:00:00Z"
		fallthrough
	case http.MethodGet:
		if x.sc == nil {
			out(map[string]any{"server_id": 701, "configured": false, "enabled": false, "snapshot_limit": 3})
			return
		}
		v := map[string]any{"server_id": 701, "snapshot_limit": 3, "last_status": nil, "last_error": nil, "last_run_at": nil, "time_zone": "Europe/Istanbul"}
		for k, val := range x.sc {
			v[k] = val
		}
		out(v)
	case http.MethodDelete:
		x.sc = nil
		out(map[string]any{"server_id": 701, "configured": false, "deleted": true})
	}
}

// #3462 madde 10: gyro_server_snapshot_schedule - create with defaults, weekly change in place, weekday rules, API refusal, import, destroy.
func TestAccMockSnapshotSchedule(t *testing.T) {
	x := &snapSchedMock{base: mockapi.New()}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(body string) string { return prov + "resource \"gyro_server_snapshot_schedule\" \"s\" {\n  server_id = 701\n" + body + "}\n" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if x.sc != nil {
				return fmt.Errorf("schedule not removed")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("  frequency = \"weekly\"\n"), PlanOnly: true, ExpectError: regexp.MustCompile(`weekday required`)},
			{Config: cfg("  weekday = \"monday\"\n"), PlanOnly: true, ExpectError: regexp.MustCompile(`weekday only for weekly`)},
			{Config: cfg(""), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "frequency", "daily"),
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "hour", "3"),
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "keep", "7"),
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "snapshot_limit", "3"),
				resource.TestCheckNoResourceAttr("gyro_server_snapshot_schedule.s", "weekday"))},
			{Config: cfg(""), PlanOnly: true},
			{Config: cfg("  frequency = \"weekly\"\n  weekday = \"monday\"\n  hour = 4\n  minute = 30\n  keep = 2\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "weekday", "monday"),
				resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "minute", "30"),
				func(*terraform.State) error {
					if p := x.puts[len(x.puts)-1]; p["weekday"] != "monday" || p["keep"] != float64(2) {
						return fmt.Errorf("put %v", p)
					}
					return nil
				})},
			{Config: cfg("  frequency = \"weekly\"\n  weekday = \"monday\"\n  hour = 4\n  minute = 30\n  keep = 9\n"), ExpectError: regexp.MustCompile(`snapshot limit`)},
			{ResourceName: "gyro_server_snapshot_schedule.s", ImportState: true, ImportStateId: "701", ImportStateVerify: true},
			{Config: cfg("  frequency = \"weekly\"\n  weekday = \"monday\"\n  hour = 4\n  minute = 30\n  keep = 2\n  enabled = false\n"),
				Check: resource.TestCheckResourceAttr("gyro_server_snapshot_schedule.s", "enabled", "false")},
		},
	})
}
