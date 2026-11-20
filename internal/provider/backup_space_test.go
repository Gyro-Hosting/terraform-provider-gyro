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

// API 2.44.0 gyro_backup_space: order (confirm + Idempotency-Key, server_id), wait for set-up, password from /credentials,
// rename (label) + re-bind (PUT server) in place, plan change refused, import, destroy = DELETE ?confirm=true&immediate=true.

type bsMock struct {
	mu       sync.Mutex
	exists   bool
	cancel   bool
	gets     int
	nick     string
	server   int64
	orders   int
	idemSeen bool
	delQuery string
	calls    []string
}

func (x *bsMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	p := regexp.MustCompile(`^/api/v1/client/`).ReplaceAllString(r.URL.Path, "")
	x.calls = append(x.calls, r.Method+" "+p)
	conn := func() map[string]any {
		st, user := "active", "y-d48201"
		if x.gets <= 1 {
			st, user = "pending", ""
		}
		var sid any = x.server
		ips := []string{fmt.Sprintf("203.0.113.%d", x.server%100)}
		return map[string]any{"host": "203.0.113.12", "port": 2201, "user": user, "path": "/veri", "protocol": "sftp", "size_gb": 50, "size_plan": "50gb",
			"used_bytes": 1024, "used_percent": 0.1, "server_id": sid, "allowed_ips": ips, "state": st}
	}
	switch r.Method + " " + p {
	case "GET cloud/backup-spaces/plans":
		jsonOut(w, 200, map[string]any{"plans": []any{map[string]any{"id": 1925, "plan": "50gb", "name": "Yedekleme Alanı 50 GB", "region": "tr-ist-1", "protocol": "sftp", "size_gb": 50,
			"available": true, "prices": []any{map[string]any{"cycle": "monthly", "amount": 0.99, "list_amount": 1.29, "currency": "EUR"}}, "tax_included": false}},
			"servers": []any{map[string]any{"server_id": 47466, "name": "web-1", "ip": "203.0.113.66"}, map[string]any{"server_id": 47467, "name": "web-2", "ip": "203.0.113.67"}}})
	case "POST cloud/backup-spaces":
		if body["confirm"] != true || r.Header.Get("Idempotency-Key") == "" {
			jsonErr(w, 422, "confirm_required", "confirm + Idempotency-Key")
			return
		}
		if body["plan"] != "50gb" {
			jsonErr(w, 422, "product_not_orderable", "plan must name an orderable plan")
			return
		}
		x.orders++
		x.exists, x.gets = true, 0
		x.server = int64(body["server_id"].(float64))
		if n, ok := body["name"].(string); ok {
			x.nick = n
		}
		jsonOut(w, 201, map[string]any{"backup_space": map[string]any{"id": 48201, "status": "inprocess"}, "plan": "50gb"})
	case "GET cloud/backup-spaces/48201":
		if !x.exists {
			jsonErr(w, 404, "backup_space_not_found", "Backup space not found.")
			return
		}
		x.gets++
		st := "active"
		if x.gets <= 1 {
			st = "inprocess"
		}
		jsonOut(w, 200, map[string]any{"id": 48201, "status": st, "product_id": 1925, "nickname": x.nick, "cycle": "monthly", "due_date": "2026-12-20", "backup_space": conn()})
	case "GET cloud/backup-spaces/48201/credentials":
		if x.gets <= 1 {
			jsonErr(w, 409, "not_ready", "not set up yet")
			return
		}
		jsonOut(w, 200, map[string]any{"backup_space_id": 48201, "host": "203.0.113.12", "port": 2201, "user": "y-d48201", "password": "Gyro-Test-Parola", "path": "/veri", "protocol": "sftp"})
	case "PUT cloud/backup-spaces/48201/server":
		x.server = int64(body["server_id"].(float64))
		jsonOut(w, 200, map[string]any{"backup_space_id": 48201, "backup_space": conn()})
	case "PUT cloud/services/48201/label":
		x.nick, _ = body["nickname"].(string)
		jsonOut(w, 200, map[string]any{"nickname": x.nick})
	case "DELETE cloud/backup-spaces/48201":
		x.delQuery = r.URL.RawQuery
		if r.URL.Query().Get("confirm") != "true" {
			jsonErr(w, 422, "confirm_required", "confirm")
			return
		}
		x.exists = false
		jsonOut(w, 202, map[string]any{"status": "cancelling", "backup_space_id": 48201})
	default:
		jsonErr(w, 404, "not_found", p)
	}
}

func TestAccMockBackupSpace(t *testing.T) {
	x := &bsMock{}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token    = \"wck_testA\"\n  poll_interval_seconds = 1\n}\n", srv.URL+"/api/v1/client")
	cfg := func(plan string, server int, name string) string {
		return prov + fmt.Sprintf(`
data "gyro_backup_space_plans" "p" {}

resource "gyro_backup_space" "yedek" {
  plan      = %q
  server_id = %d
  name      = %q
}

data "gyro_backup_space" "oku" {
  id = gyro_backup_space.yedek.service_id
}`, plan, server, name)
	}
	const n = "gyro_backup_space.yedek"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if x.exists {
				return fmt.Errorf("backup space left over")
			}
			if x.delQuery != "confirm=true&immediate=true" {
				return fmt.Errorf("delete query %q", x.delQuery)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("50 GB", 47466, "gyro-proje-yedek"), ExpectError: regexp.MustCompile(`a plan from GET /cloud/backup-spaces/plans`)},
			{Config: cfg("50gb", 47466, "gyro-proje-yedek"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(n, "id", "48201"),
				resource.TestCheckResourceAttr(n, "state", "active"),
				resource.TestCheckResourceAttr(n, "user", "y-d48201"),
				resource.TestCheckResourceAttr(n, "port", "2201"),
				resource.TestCheckResourceAttr(n, "path", "/veri"),
				resource.TestCheckResourceAttr(n, "size_gb", "50"),
				resource.TestCheckResourceAttr(n, "server_id", "47466"),
				resource.TestCheckResourceAttr(n, "allowed_ips.0", "203.0.113.66"),
				resource.TestCheckResourceAttr(n, "password", "Gyro-Test-Parola"),
				resource.TestCheckResourceAttr(n, "cycle", "monthly"),
				resource.TestCheckResourceAttr("data.gyro_backup_space_plans.p", "plans.0.plan", "50gb"),
				resource.TestCheckResourceAttr("data.gyro_backup_space_plans.p", "plans.0.prices.0.list_amount", "1.29"),
				resource.TestCheckResourceAttr("data.gyro_backup_space_plans.p", "servers.1.server_id", "47467"),
				resource.TestCheckResourceAttr("data.gyro_backup_space.oku", "user", "y-d48201"),
				resource.TestCheckResourceAttr("data.gyro_backup_space.oku", "used_bytes", "1024"),
				resource.TestCheckNoResourceAttr("data.gyro_backup_space.oku", "password"),
				func(*terraform.State) error {
					if x.orders != 1 {
						return fmt.Errorf("orders: %d", x.orders)
					}
					return nil
				},
			)},
			// plan change is refused (no replacement: that would delete every file)
			{Config: cfg("100gb", 47466, "gyro-proje-yedek"), ExpectError: regexp.MustCompile(`plan cannot be changed by Terraform`)},
			// rename + re-bind in place, no new order
			{Config: cfg("50gb", 47467, "gyro-proje-yedek-2"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(n, "id", "48201"),
				resource.TestCheckResourceAttr(n, "name", "gyro-proje-yedek-2"),
				resource.TestCheckResourceAttr(n, "server_id", "47467"),
				resource.TestCheckResourceAttr(n, "allowed_ips.0", "203.0.113.67"),
				func(*terraform.State) error {
					if x.orders != 1 {
						return fmt.Errorf("re-ordered: %d", x.orders)
					}
					return nil
				},
			)},
			{ResourceName: n, ImportState: true, ImportStateId: "48201", ImportStateVerify: true},
		},
	})
}
