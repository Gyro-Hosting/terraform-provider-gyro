package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// API 2.15.0: gyro_server_template captures a tr-ist-1 server; gyro_server with image = custom:<id> orders a server from it
// (POST /cloud/templates/{id}/servers, same money rules). Rename is in place; destroy deletes the template.
func TestAccMockServerTemplate(t *testing.T) {
	m, prov := mock(t)
	m.Ist = true
	cfg := func(name string, clone bool) string {
		c := prov + fmt.Sprintf(`
resource "gyro_server" "src" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-24.04"
  confirm_order = true
}
resource "gyro_server_template" "golden" {
  server_id   = tonumber(gyro_server.src.id)
  name        = %q
  description = "web golden image"
}
`, name)
		if clone {
			c += `
resource "gyro_server" "clone" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = gyro_server_template.golden.image
  hostname      = "web2.example.com"
  confirm_order = true
}
`
		}
		return c
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(s *terraform.State) error {
			if n := m.TemplateCount(); n != 0 {
				return fmt.Errorf("%d template(s) left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg("golden", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_server_template.golden", "status", "ready"),
					resource.TestCheckResourceAttr("gyro_server_template.golden", "image", "custom:1"),
					resource.TestCheckResourceAttr("gyro_server_template.golden", "mode", "snapshot"),
					resource.TestCheckResourceAttr("gyro_server_template.golden", "min_disk_gb", "35"),
					resource.TestCheckResourceAttr("gyro_server_template.golden", "base_image", "ubuntu-24.04"),
					resource.TestCheckResourceAttr("gyro_server.clone", "image", "custom:1"),
					resource.TestCheckResourceAttr("gyro_server.clone", "status", "active"),
					func(s *terraform.State) error {
						for _, c := range m.Calls {
							if strings.HasSuffix(c, "/cloud/templates/1/servers") {
								return nil
							}
						}
						return fmt.Errorf("server was not ordered from the template: %v", m.Calls)
					},
				),
			},
			{ // rename in place: no new template, no new order
				Config: cfg("golden-v2", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_server_template.golden", "name", "golden-v2"),
					resource.TestCheckResourceAttr("gyro_server_template.golden", "id", "1"),
				),
			},
			{ResourceName: "gyro_server_template.golden", ImportState: true, ImportStateVerify: true},
		},
	})
}
