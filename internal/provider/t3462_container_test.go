package provider

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// #3462 (tur 1): the API left spec.alerts out when min_ready is 0 - an imported service still reads { min_ready = 0 }, plan empty.
func TestAccMockContainerServiceNilAlertsImport(t *testing.T) {
	cm := newContainerMock()
	cm.noZeroAlerts = true
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := prov + `
resource "gyro_container_service" "api" {
  service_id   = 7
  name         = "api"
  image        = "ghcr.io/acme/api:1"
  port         = 8080
  notification = { min_ready = 0 }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr("gyro_container_service.api", "notification.min_ready", "0")},
			{ResourceName: "gyro_container_service.api", ImportState: true, ImportStateId: "7/api", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"state", "secrets_wo_keys"}},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// #3462 B1: secrets_wo never reaches the state; sent on create and when secrets_wo_version changes; dropped keys are deleted.
func TestAccMockContainerServiceSecretsWO(t *testing.T) {
	cm := newContainerMock()
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	cfg := func(wo string, ver int) string {
		return prov + fmt.Sprintf(`
resource "gyro_container_service" "api" {
  service_id         = 7
  name               = "api"
  image              = "ghcr.io/acme/api:1"
  port               = 8080
  secrets_wo         = { %s }
  secrets_wo_version = %d
}
`, wo, ver)
	}
	noValue := func(s *terraform.State) error {
		rs := s.RootModule().Resources["gyro_container_service.api"]
		for k, v := range rs.Primary.Attributes {
			if strings.HasPrefix(k, "secrets_wo.") || strings.Contains(v, "gizli-") {
				return fmt.Errorf("secret value in state: %s", k)
			}
		}
		return nil
	}
	sec := func(want string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			cm.mu.Lock()
			defer cm.mu.Unlock()
			got := []string{}
			for k, v := range cm.secrets["api"] {
				got = append(got, k+"="+v)
			}
			if fmt.Sprint(sortedS(got)) != want {
				return fmt.Errorf("remote secrets %v, want %s", sortedS(got), want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(`DB_PASS = "gizli-1", API_KEY = "gizli-a"`, 1), Check: resource.ComposeAggregateTestCheckFunc(noValue, sec("[API_KEY=gizli-a DB_PASS=gizli-1]"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "secrets_wo_keys.#", "2"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "secrets_wo_keys.0", "API_KEY"))},
			// value changes but the version does not: nothing is sent, plan empty
			{Config: cfg(`DB_PASS = "gizli-2", API_KEY = "gizli-a"`, 1), PlanOnly: true},
			// version bump: new values sent, API_KEY dropped -> deleted
			{Config: cfg(`DB_PASS = "gizli-2"`, 2), Check: resource.ComposeAggregateTestCheckFunc(noValue, sec("[DB_PASS=gizli-2]"),
				resource.TestCheckResourceAttr("gyro_container_service.api", "secrets_wo_keys.#", "1"))},
			{Config: cfg(`DB_PASS = "gizli-2"`, 2), PlanOnly: true},
		},
	})
}

func sortedS(l []string) []string {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && l[j] < l[j-1]; j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
	return l
}
