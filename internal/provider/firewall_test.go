package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// gyro_firewall against the in-process mock API (GyroAPI 2.8.0 policy endpoints).
func TestAccMockFirewall(t *testing.T) {
	_, prov := mock(t)
	cfg := func(def, sshSrc string, extra string) string {
		return prov + fmt.Sprintf(`
resource "gyro_server" "web" {
  plan          = "eko-2-4"
  region        = "eu-de-1"
  image         = "debian-12"
  hostname      = "fw.example.com"
  confirm_order = true
}
resource "gyro_firewall" "web" {
  server_id      = tonumber(gyro_server.web.id)
  default_action = %q
  rule {
    action   = "allow"
    port     = "80,443"
    description = "web"
  }
  rule {
    action = "allow"
    port   = "22"
    source = %q
  }
  %s
}
`, def, sshSrc, extra)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: cfg("deny", "203.0.113.10", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_firewall.web", "default_action", "deny"),
					resource.TestCheckResourceAttr("gyro_firewall.web", "status", "active"),
					resource.TestCheckResourceAttr("gyro_firewall.web", "rule.#", "2"),
					resource.TestCheckResourceAttr("gyro_firewall.web", "rule.1.source", "203.0.113.10"), // user spelling kept (API: /32)
					resource.TestCheckResourceAttr("gyro_firewall.web", "rule.0.protocol", "tcp"),
				),
			},
			{ // plan is empty: /32 normalisation does not cause a diff
				Config:   cfg("deny", "203.0.113.10", ""),
				PlanOnly: true,
			},
			{
				Config: cfg("allow", "203.0.113.0/24", `rule {
    action    = "deny"
    protocol  = "tcp"
    port      = "25"
    direction = "out"
  }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_firewall.web", "default_action", "allow"),
					resource.TestCheckResourceAttr("gyro_firewall.web", "rule.#", "3"),
					resource.TestCheckResourceAttr("gyro_firewall.web", "rule.2.direction", "out"),
				),
			},
			{
				ResourceName:            "gyro_firewall.web",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"rule.1.source"},
			},
		},
	})
}
