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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// GYR-12 (API 2.36.0) gyro_container_schedule + #3461 gyro_container_notifications keeps events it does not manage.

var allNtfEvents = []string{"down", "crashloop", "replicas_low", "scale_max", "capacity", "egress", "job_failed", "log_rule", "log_quota", "schedule_failed", "schedule_missed"}

type schedMock struct {
	mu    sync.Mutex
	sch   map[string]map[string]any
	n     int
	ntf   map[string]int // event -> 0/1 (the API returns every event it knows)
	cool  int
	calls []string
}

var reSchedPath = regexp.MustCompile(`/cloud/containers/9/(schedules|notifications)(?:/([a-z0-9-]+))?$`)

func (x *schedMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	mt := reSchedPath.FindStringSubmatch(r.URL.Path)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	bb, _ := json.Marshal(body)
	x.calls = append(x.calls, r.Method+" "+r.URL.Path+" "+string(bb))
	if mt == nil {
		jsonErr(w, 404, "not_found", "x")
		return
	}
	if mt[1] == "notifications" {
		if r.Method == "PUT" {
			if ev, ok := body["events"]; ok { // the full set, like Uyari::save
				on := map[string]bool{}
				for _, e := range strings.Split(fmt.Sprint(ev), ",") {
					on[strings.TrimSpace(e)] = true
				}
				for _, e := range allNtfEvents {
					x.ntf[e] = map[bool]int{true: 1, false: 0}[on[e]]
				}
			}
			if c, ok := body["cooldown"]; ok {
				x.cool = int(c.(float64))
			}
			jsonOut(w, 200, map[string]any{"status": "successful"})
			return
		}
		jsonOut(w, 200, map[string]any{"settings": map[string]any{"email": 1, "sms": 0, "telegram": "", "webhook_url": "", "events": x.ntf, "cooldown": x.cool, "resolved": 1}})
		return
	}
	view := func(s map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range s {
			if k == "headers" || k == "body" {
				continue
			}
			out[k] = v
		}
		if h, ok := s["headers"].(map[string]any); ok {
			hm := map[string]any{}
			for k := range h {
				hm[k] = "****"
			}
			out["headers"] = hm
		}
		st := "active"
		if s["paused"] == true {
			st = "paused"
		}
		out["state"], out["next_run"] = st, "2026-11-16T00:00:00Z"
		return out
	}
	apply := func(s, b map[string]any) {
		for k, v := range b {
			if k == "cron" || k == "rate_minutes" || k == "at" {
				delete(s, "cron")
				delete(s, "rate_minutes")
				delete(s, "at")
				s["kind"] = map[string]string{"cron": "cron", "rate_minutes": "rate", "at": "once"}[k]
			}
			if k == "cpu_m" || k == "memory_mi" {
				if v == float64(0) {
					delete(s, k)
					continue
				}
			}
			s[k] = v
		}
		if s["target"] == "http" {
			if s["method"] == nil {
				s["method"] = "GET"
			}
			if s["http_timeout_seconds"] == nil {
				s["http_timeout_seconds"] = 30
			}
			delete(s, "service")
			delete(s, "command")
		} else if s["timeout_seconds"] == nil {
			s["timeout_seconds"] = 1800
		}
		if s["at"] != nil {
			s["at"] = "2026-12-01T00:00:00Z" // the API answers in UTC
		}
	}
	switch {
	case mt[2] == "" && r.Method == "POST":
		x.n++
		id := fmt.Sprintf("s00000000%03d", x.n)
		s := map[string]any{"id": id, "name": body["name"], "timezone": "Europe/Istanbul", "retries": 0, "paused": false}
		delete(body, "name")
		apply(s, body)
		x.sch[id] = s
		jsonOut(w, 201, map[string]any{"schedule": view(s)})
	default:
		var s map[string]any
		for _, v := range x.sch {
			if v["id"] == mt[2] || v["name"] == mt[2] {
				s = v
			}
		}
		if s == nil {
			jsonErr(w, 404, "schedule_not_found", "Schedule not found.")
			return
		}
		switch r.Method {
		case "GET":
			jsonOut(w, 200, view(s))
		case "PATCH":
			apply(s, body)
			jsonOut(w, 200, map[string]any{"schedule": view(s)})
		case "DELETE":
			delete(x.sch, fmt.Sprint(s["id"]))
			jsonOut(w, 200, map[string]any{"deleted": true})
		}
	}
}

func schedProv(t *testing.T) (*schedMock, string) {
	x := &schedMock{sch: map[string]map[string]any{}, ntf: map[string]int{}, cool: 30}
	for _, e := range allNtfEvents {
		x.ntf[e] = 1
	}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	return x, fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
}
`, srv.URL+"/api/v1/client")
}

func TestAccMockContainerSchedule(t *testing.T) {
	x, prov := schedProv(t)
	cfg := func(svc, http string) string {
		return prov + `
resource "gyro_container_schedule" "report" {
  environment_id = 9
  name           = "nightly-report"
` + svc + `
}

resource "gyro_container_schedule" "ping" {
  environment_id = 9
  name           = "ping-app"
` + http + `
}
`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.sch) != 0 {
				return fmt.Errorf("left over schedules: %d", len(x.sch))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg(`  cron = "0 3 * * *"
  rate_minutes = 5
  service = "api"`, `  cron = "*/15 * * * *"
  url  = "https://api.example.com/cron"`), ExpectError: regexp.MustCompile(`exactly one of cron, rate_minutes, at`)},
			{Config: cfg(`  cron = "0 3 * * *"
  service = "api"
  url = "https://x.example.com/"`, `  cron = "*/15 * * * *"
  url  = "https://api.example.com/cron"`), ExpectError: regexp.MustCompile(`either service`)},
			{Config: cfg(`  cron    = "0 3 * * *"
  service = "api"
  command = ["sh", "-c", "npm run report && echo ok"]
  retries = 2`, `  rate_minutes = 5
  url          = "https://api.example.com/cron"
  method       = "POST"
  headers      = { Authorization = "Bearer t0ken" }
  body         = "{}"`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "target", "service"),
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "timezone", "Europe/Istanbul"),
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "timeout_seconds", "1800"),
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "command.2", "npm run report && echo ok"),
				resource.TestCheckResourceAttr("gyro_container_schedule.ping", "target", "http"),
				resource.TestCheckResourceAttr("gyro_container_schedule.ping", "headers.Authorization", "Bearer t0ken"),
				resource.TestCheckResourceAttr("gyro_container_schedule.ping", "http_timeout_seconds", "30"),
				resource.TestCheckResourceAttr("gyro_container_schedule.ping", "state", "active"),
			)},
			{Config: cfg(`  cron    = "0 3 * * *"
  service = "api"
  command = ["sh", "-c", "npm run report && echo ok"]
  retries = 2`, `  rate_minutes = 5
  url          = "https://api.example.com/cron"
  method       = "POST"
  headers      = { Authorization = "Bearer t0ken" }
  body         = "{}"`), PlanOnly: true},
			// change in place: timing + time zone + pause; http -> every 10 minutes
			{Config: cfg(`  cron     = "30 2 * * 1-5"
  timezone = "UTC"
  service  = "api"
  command  = ["npm", "run", "report"]
  paused   = true`, `  rate_minutes = 10
  url          = "https://api.example.com/cron"
  headers      = { Authorization = "Bearer n3w" }`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "cron", "30 2 * * 1-5"),
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "state", "paused"),
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "retries", "0"),
				resource.TestCheckResourceAttr("gyro_container_schedule.ping", "rate_minutes", "10"),
			)},
			{Config: cfg(`  at      = "2026-12-01T03:00"
  service = "api"`, `  cron = "*/15 * * * *"
  url  = "https://api.example.com/cron"`), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_container_schedule.report", "at", "2026-12-01T03:00"), // as written, not the UTC answer
				resource.TestCheckNoResourceAttr("gyro_container_schedule.report", "cron"),
			)},
			{Config: cfg(`  at      = "2026-12-01T03:00"
  service = "api"`, `  cron = "*/15 * * * *"
  url  = "https://api.example.com/cron"`), PlanOnly: true},
			{ResourceName: "gyro_container_schedule.report", ImportState: true, ImportStateId: "9/nightly-report", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"at", "next_run", "command"}, Config: cfg(`  at      = "2026-12-01T03:00"
  service = "api"`, `  cron = "*/15 * * * *"
  url  = "https://api.example.com/cron"`)},
		},
	})
	sent := ""
	for _, c := range x.calls {
		if strings.HasPrefix(c, "POST") && strings.Contains(c, "ping-app") {
			sent = c
		}
	}
	if !strings.Contains(sent, `"Authorization":"Bearer t0ken"`) || !strings.Contains(sent, `"target":"http"`) {
		t.Fatalf("http create body: %s", sent)
	}
}

// #3461: a change that does not configure "events" (e.g. cooldown) must keep every event switched on, including the
// ones added after the provider was written (job_failed, log_*, schedule_*).
func TestAccMockContainerNotifyKeepsEvents(t *testing.T) {
	x, prov := schedProv(t)
	cfg := func(extra string) string {
		return prov + `
resource "gyro_container_notifications" "n" {
  service_id = 9
` + extra + `
}
`
	}
	on := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got := []string{}
			for e, v := range x.ntf {
				if v == 1 {
					got = append(got, e)
				}
			}
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				return fmt.Errorf("events on the platform: %v, want %v", got, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(`  cooldown = 15`), Check: resource.ComposeAggregateTestCheckFunc(on(allNtfEvents...),
				resource.TestCheckResourceAttr("gyro_container_notifications.n", "events.#", "11"))},
			{Config: cfg(`  cooldown = 20`), Check: resource.ComposeAggregateTestCheckFunc(on(allNtfEvents...),
				resource.TestCheckResourceAttr("gyro_container_notifications.n", "cooldown", "20"))},
			{Config: cfg(`  cooldown = 20`), PlanOnly: true},
			{Config: cfg(`  cooldown = 20
  events   = ["down", "job_failed", "schedule_failed"]`), Check: on("down", "job_failed", "schedule_failed")},
			{Config: cfg(`  cooldown = 25
  events   = ["down", "job_failed", "schedule_failed"]`), Check: on("down", "job_failed", "schedule_failed")},
			{Config: cfg(`  events = ["down", "no_such_event"]`), ExpectError: regexp.MustCompile(`unknown event "no_such_event"`)},
		},
	})
	for _, c := range x.calls { // the cooldown-only changes did not send "events"
		if strings.HasPrefix(c, "PUT") && strings.Contains(c, `"cooldown":20`) && strings.Contains(c, `"events"`) && !strings.Contains(c, "job_failed") {
			t.Fatalf("events sent without being configured: %s", c)
		}
	}
}
