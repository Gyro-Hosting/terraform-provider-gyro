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

// API 2.17.5: gyro_server_storage_mount. The storage-mounts endpoints are served by a small wrapper around the mock API.
type mountMock struct {
	mu   sync.Mutex
	next int
	rows map[int]map[string]any
	base http.Handler
	dels int
}

var reMount = regexp.MustCompile(`/cloud/servers/(\d+)/storage-mounts(?:/(\d+))?$`)

func (x *mountMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt := reMount.FindStringSubmatch(r.URL.Path)
	if mt == nil {
		x.base.ServeHTTP(w, r)
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	var srv, mid int
	fmt.Sscan(mt[1], &srv)
	if mt[2] != "" {
		fmt.Sscan(mt[2], &mid)
	}
	out := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
	}
	switch {
	case r.Method == http.MethodPost && mid == 0:
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["bucket"] == "" || b["storage_service_id"] == nil {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_bucket","message":"bucket"}}`))
			return
		}
		x.next++
		mp, _ := b["mount_point"].(string)
		if mp == "" {
			mp = "/mnt/" + b["bucket"].(string)
		}
		prot := fmt.Sprint(b["storage_service_id"]) == "17999" // API 2.20.0: Protected Storage -> read-only default, rw needs accept_versioning
		ro, has := b["read_only"].(bool)
		if !has {
			ro = prot
		}
		if prot && !ro && b["accept_versioning"] != true {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":{"code":"versioning_ack_required","message":"accept_versioning"}}`))
			return
		}
		row := map[string]any{"id": x.next, "server_id": srv, "storage_service_id": b["storage_service_id"], "bucket": b["bucket"], "mount_point": mp,
			"read_only": ro, "protected": prot, "name": b["bucket"], "state": "installing", "automatic": true, "remove_command": "sudo gyro-s3mount remove " + b["bucket"].(string)}
		x.rows[x.next] = row
		c := map[string]any{}
		for k, v := range row {
			c[k] = v
		}
		c["install_command"] = "curl -fsSL https://www.gyrohosting.com/x.sh -o /tmp/g.sh && sudo bash /tmp/g.sh install 0123 https://www.gyrohosting.com"
		out(202, map[string]any{"storage_mount": c})
	case r.Method == http.MethodGet && mid > 0:
		row, ok := x.rows[mid]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"mount_not_found","message":"nf"}}`))
			return
		}
		row["state"] = "active"
		out(200, map[string]any{"storage_mount": row})
	case r.Method == http.MethodDelete && mid > 0:
		row, ok := x.rows[mid]
		if !ok {
			w.WriteHeader(404)
			return
		}
		row["state"] = "removed"
		x.dels++
		out(202, map[string]any{"storage_mount": row})
	default:
		w.WriteHeader(405)
	}
}

func TestAccMockStorageMount(t *testing.T) {
	m := mockapi.New()
	x := &mountMock{rows: map[int]map[string]any{}, base: m}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
	cfg := func(ro bool) string {
		return prov + fmt.Sprintf(`
resource "gyro_server_storage_mount" "kl" {
  server_id          = 17547
  storage_service_id = 17999
  bucket             = "arsiv-ab12"
}
resource "gyro_server_storage_mount" "m" {
  server_id          = 17547
  storage_service_id = 17546
  bucket             = "yedekler"
  read_only          = %v
}
resource "gyro_server_storage_mount" "w" {
  server_id          = 17547
  storage_service_id = 17546
  bucket             = "web"
  mount_point        = "/var/www/assets"
}
`, ro)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_storage_mount.m", "mount_point", "/mnt/yedekler"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.m", "read_only", "false"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.kl", "read_only", "true"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.kl", "protected", "true"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.w", "mount_point", "/var/www/assets"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.w", "automatic", "true"),
				resource.TestMatchResourceAttr("gyro_server_storage_mount.m", "install_command", regexp.MustCompile(`^curl -fsSL `)),
				resource.TestMatchResourceAttr("gyro_server_storage_mount.m", "id", regexp.MustCompile(`^17547/\d+$`)),
			)},
			{ResourceName: "gyro_server_storage_mount.w", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"install_command", "state", "accept_versioning"}},
			// read_only change forces a new mount (old one removed)
			{Config: cfg(true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_storage_mount.m", "read_only", "true"),
				func(*terraform.State) error {
					if x.dels != 1 {
						return fmt.Errorf("expected one removal, got %d", x.dels)
					}
					return nil
				},
			)},
		},
	})
}

func TestAccMockStorageMountProtectedAck(t *testing.T) {
	m := mockapi.New()
	x := &mountMock{rows: map[int]map[string]any{}, base: m}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
}
`, srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: prov + `
resource "gyro_server_storage_mount" "rw" {
  server_id          = 17547
  storage_service_id = 17999
  bucket             = "arsiv-ab12"
  read_only          = false
}`, ExpectError: regexp.MustCompile(`accept_versioning`)},
			{Config: prov + `
resource "gyro_server_storage_mount" "rw" {
  server_id          = 17547
  storage_service_id = 17999
  bucket             = "arsiv-ab12"
  read_only          = false
  accept_versioning  = true
}`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_storage_mount.rw", "read_only", "false"),
				resource.TestCheckResourceAttr("gyro_server_storage_mount.rw", "protected", "true"),
			)},
		},
	})
}
