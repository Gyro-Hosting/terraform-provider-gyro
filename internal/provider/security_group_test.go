package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// gyro_security_group + gyro_server_security_groups against the in-process mock API (GyroAPI 2.9.0).
func TestAccMockSecurityGroup(t *testing.T) {
	_, prov := mock(t)
	cfg := func(name, ssh string, attach string) string {
		return prov + fmt.Sprintf(`
resource "gyro_server" "web" {
  plan          = "eko-2-4"
  region        = "eu-de-1"
  image         = "debian-12"
  hostname      = "sg.example.com"
  confirm_order = true
}
resource "gyro_security_group" "web" {
  name        = %q
  description = "web tier"
  rule {
    type = "https"
  }
  rule {
    type        = "ssh"
    source      = [%q]
    description = "office"
  }
  rule {
    type   = "custom_tcp"
    port   = "8000-8100"
    source = ["198.51.100.0/24", "203.0.113.7"]
  }
  rule {
    direction = "out"
    type      = "all"
  }
}
resource "gyro_security_group" "admin" {
  name = "admin"
  rule {
    type   = "rdp"
    source = ["203.0.113.10/32"]
  }
}
resource "gyro_server_security_groups" "web" {
  server_id          = tonumber(gyro_server.web.id)
  security_group_ids = [%s]
}
`, name, ssh, attach)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: cfg("web", "203.0.113.10", "gyro_security_group.web.id"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("gyro_security_group.web", "id", regexp.MustCompile(`^sg-[0-9a-f]{8}$`)),
					resource.TestCheckResourceAttr("gyro_security_group.web", "rule.#", "4"),
					resource.TestCheckResourceAttr("gyro_security_group.web", "rule.0.port", "443"),
					resource.TestCheckResourceAttr("gyro_security_group.web", "rule.0.source.0", "0.0.0.0/0"),
					resource.TestCheckResourceAttr("gyro_security_group.web", "rule.1.source.0", "203.0.113.10"), // user spelling kept (API: /32)
					resource.TestMatchResourceAttr("gyro_security_group.web", "rule.1.rule_id", regexp.MustCompile(`^gfr-`)),
					resource.TestCheckResourceAttr("gyro_server_security_groups.web", "security_group_ids.#", "1"),
				),
			},
			{ // no diff: /32 normalisation and computed ports
				Config:   cfg("web", "203.0.113.10", "gyro_security_group.web.id"),
				PlanOnly: true,
			},
			{ // rename keeps the id, second group attached in order
				Config: cfg("web-tier", "203.0.113.0/24", "gyro_security_group.admin.id, gyro_security_group.web.id"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_security_group.web", "name", "web-tier"),
					resource.TestCheckResourceAttr("gyro_security_group.web", "rule.1.source.0", "203.0.113.0/24"),
					resource.TestCheckResourceAttr("gyro_server_security_groups.web", "security_group_ids.#", "2"),
					resource.TestCheckResourceAttrPair("gyro_server_security_groups.web", "security_group_ids.0", "gyro_security_group.admin", "id"),
				),
			},
			{
				ResourceName:            "gyro_security_group.web",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"rule.1.source.0", "rule.2.source.1"},
			},
			{
				ResourceName:      "gyro_server_security_groups.web",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
