package provider

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
)

// v0.41.0 (Gyro Public API 2.46.0): provider account_id sends X-Account-Id on every request; omitted = no header;
// one step per account (see the note in the steps).
func TestAccMockProviderAccountID(t *testing.T) {
	m := mockapi.New()
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cloud/locations") {
			mu.Lock()
			seen[r.Header.Get("X-Account-Id")]++
			mu.Unlock()
		}
		m.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GYRO_TOKEN", "wck_testA")
	t.Setenv("GYRO_ENDPOINT", srv.URL+"/api/v1/client")
	t.Setenv("GYRO_CONFIG_FILE", t.TempDir()+"/none.toml")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: "provider \"gyro\" {\n  account_id = 0\n}\ndata \"gyro_locations\" \"all\" {}\n", ExpectError: regexp.MustCompile(`Invalid account_id`)},
			// one provider configuration per step: the in-process test harness serves every alias from ONE provider server,
			// whose configure data is shared, so aliases with different account_id cannot be checked in one step here
			// (checked on dev with the real terraform binary: two aliases put each SSH key into its own account, 24.11).
			{Config: "provider \"gyro\" {}\ndata \"gyro_locations\" \"own\" {}\n",
				Check: resource.TestCheckResourceAttr("data.gyro_locations.own", "locations.#", "2")},
			{Config: "provider \"gyro\" {\n  account_id = 35553\n}\ndata \"gyro_locations\" \"musteri\" {}\n",
				Check: resource.TestCheckResourceAttr("data.gyro_locations.musteri", "locations.#", "2")},
		},
	})
	mu.Lock()
	defer mu.Unlock()
	if seen[""] == 0 || seen["35553"] == 0 {
		t.Fatalf("X-Account-Id per provider not seen: %v", seen)
	}
	for k := range seen {
		if k != "" && k != "35553" {
			t.Fatalf("unexpected X-Account-Id %q", k)
		}
	}
}
