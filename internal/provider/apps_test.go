package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// API 2.14.1: data source gyro_apps (GET /cloud/apps) with filters category / popular_tr / os.
func TestAccMockAppsDataSource(t *testing.T) {
	_, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: prov + `
data "gyro_apps" "all" {}
data "gyro_apps" "tr" { popular_tr = true }
data "gyro_apps" "u22" { os = "ubuntu-22.04" }
data "gyro_apps" "web" { category = "web" }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.gyro_apps.all", "apps.#", "3"),
				resource.TestCheckResourceAttr("data.gyro_apps.tr", "apps.#", "2"),
				resource.TestCheckResourceAttr("data.gyro_apps.u22", "apps.#", "1"),
				resource.TestCheckResourceAttr("data.gyro_apps.u22", "apps.0.id", "docker"),
				resource.TestCheckResourceAttr("data.gyro_apps.web", "apps.0.min_ram_gb", "1"),
				resource.TestCheckResourceAttr("data.gyro_apps.web", "apps.0.popular_tr", "true"),
			),
		}},
	})
}
