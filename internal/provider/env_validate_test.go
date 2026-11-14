package provider

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// GT-19: invisible / invalid env names are rejected at plan time (no API call, no tainted resource).
func TestAccMockContainerEnvKeyValidation(t *testing.T) {
	cm := newContainerMock()
	srv := httptest.NewServer(cm)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	svc := func(env string) string {
		return prov + `resource "gyro_container_service" "web" {
  service_id = 7
  name       = "web"
  image      = "nginx"
  port       = 80
  env        = ` + env + `
}`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: svc(`{ "​ZW" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`invisible character U\+200B`)},
			{Config: svc(`{ "APP MODE" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`whitespace`)},
			{Config: svc(`{ "1ABC" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`letters, digits and _`)},
			{Config: svc(`{ GYRO_X = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`reserved`)},
			{Config: svc(`{ APP_MODE = "\u200bprod" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`starts with an invisible\s+character U\+200B`)},
			{Config: svc(`{ APP_MODE = "\ufeffprod" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`invisible\s+character U\+FEFF`)},
			{Config: svc(`{ "APP_MODE\t" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`whitespace`)},
			{Config: svc(`{ " APP_MODE" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`whitespace`)},
			{Config: svc(`{ "\u00a0APP_MODE" = "x" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`U\+00A0`)},
			{Config: svc(`{ APP_MODE = "prod" }`), Check: resource.TestCheckResourceAttr("gyro_container_service.web", "env.APP_MODE", "prod")},
		},
	})
	if len(cm.calls) == 0 {
		t.Fatal("valid config did not reach the API")
	}
}
