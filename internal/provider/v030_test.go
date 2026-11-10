package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// v0.3.0: plan/region, deprecated server_type/location, state compatibility.

func srvCfg(prov, args string) string {
	return prov + fmt.Sprintf(`
resource "gyro_server" "s" {
%s
  image         = "debian-12"
  confirm_order = true
}
`, args)
}

var noChanges = resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}

// Legacy arguments still order the right product; plan/region are filled in, and moving the
// configuration from server_type/location to plan/region (or to product_id) changes nothing.
func TestAccMockLegacyToPlanRegion(t *testing.T) {
	m, prov := mock(t)
	m.Balance[1] = 100000 // room for the second (replacement) order
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: srvCfg(prov, "  server_type = \"lg-e24\"\n  location    = \"dc-a\""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_server.s", "product_id", "723"),
					resource.TestCheckResourceAttr("gyro_server.s", "plan", "eko-2-4"),
					resource.TestCheckResourceAttr("gyro_server.s", "region", "eu-de-1"),
				),
			},
			{Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"eu-de-1\""), ConfigPlanChecks: noChanges},
			{Config: srvCfg(prov, "  product_id = 723"), ConfigPlanChecks: noChanges},
			{Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"eu-de-1\""), ConfigPlanChecks: noChanges},
			{ // a real change of plan still replaces the server
				Config: srvCfg(prov, "  plan   = \"pro-2-8\"\n  region = \"eu-de-1\""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("gyro_server.s", plancheck.ResourceActionDestroyBeforeCreate)}},
				Check: resource.TestCheckResourceAttr("gyro_server.s", "product_id", "656"),
			},
		},
	})
	if n := m.OrderCount(); n != 2 {
		t.Fatalf("orders: %d, want 2 (initial + explicit plan change)", n)
	}
}

// The API answers with canonical names; aliases in the configuration must not cause a diff/replacement.
func TestAccMockAliasesNoDiff(t *testing.T) {
	m, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{ // region by city name
				Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"Falkenstein\""),
				Check:  resource.TestCheckResourceAttr("gyro_server.s", "region", "Falkenstein"),
			},
			{Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"Falkenstein\""), ConfigPlanChecks: noChanges},
		},
	})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{ // new names in the deprecated arguments
				Config: srvCfg(prov, "  server_type = \"eko-2-4\"\n  location    = \"eu-de-1\""),
				Check:  resource.TestCheckResourceAttr("gyro_server.s", "plan", "eko-2-4"),
			},
			{Config: srvCfg(prov, "  server_type = \"eko-2-4\"\n  location    = \"eu-de-1\""), ConfigPlanChecks: noChanges},
			{Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"eu-de-1\""), ConfigPlanChecks: noChanges},
		},
	})
	if n := m.OrderCount(); n != 2 {
		t.Fatalf("orders: %d, want 2", n)
	}
}

func TestAccMockProductsFilter(t *testing.T) {
	_, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: prov + `
data "gyro_products" "fi" { region = "eu-fi-1" }
data "gyro_products" "pro" { plan = "pro-2-8" }
data "gyro_products" "legacy" { location = "dc-c" }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.gyro_products.fi", "products.#", "1"),
				resource.TestCheckResourceAttr("data.gyro_products.fi", "products.0.plan", "standart-1-1"),
				resource.TestCheckResourceAttr("data.gyro_products.fi", "products.0.product_line", "cloud_server"),
				resource.TestCheckResourceAttr("data.gyro_products.pro", "products.#", "1"),
				resource.TestCheckResourceAttr("data.gyro_products.pro", "products.0.id", "656"),
				resource.TestCheckResourceAttr("data.gyro_products.legacy", "products.0.region", "eu-fi-1"),
			),
		}},
	})
}

// State written by the published 0.2.1 provider (downloaded from the Registry) upgrades without a diff,
// and the configuration can then move to plan/region. Needs network access to registry.terraform.io.
func TestAccMockUpgradeFrom021(t *testing.T) {
	if os.Getenv("GYRO_SKIP_UPGRADE_TEST") == "1" {
		t.Skip("GYRO_SKIP_UPGRADE_TEST=1")
	}
	old := map[string]resource.ExternalProvider{"gyro": {Source: "gyro-hosting/gyro", VersionConstraint: "0.2.1"}}
	for name, legacy := range map[string]string{
		"legacy_args": "  server_type = \"lg-e24\"\n  location    = \"dc-a\"",
		"product_id":  "  product_id = 723",
	} {
		t.Run(name, func(t *testing.T) {
			m, prov := mock(t)
			resource.Test(t, resource.TestCase{
				Steps: []resource.TestStep{
					{ExternalProviders: old, Config: srvCfg(prov, legacy),
						Check: resource.TestCheckResourceAttr("gyro_server.s", "product_id", "723")},
					{ProtoV6ProviderFactories: factories, Config: srvCfg(prov, legacy), ConfigPlanChecks: noChanges,
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr("gyro_server.s", "plan", "eko-2-4"),
							resource.TestCheckResourceAttr("gyro_server.s", "region", "eu-de-1"))},
					{ProtoV6ProviderFactories: factories, Config: srvCfg(prov, "  plan   = \"eko-2-4\"\n  region = \"eu-de-1\""), ConfigPlanChecks: noChanges},
				},
			})
			if n := m.OrderCount(); n != 1 {
				t.Fatalf("orders: %d, want 1", n)
			}
		})
	}
}
