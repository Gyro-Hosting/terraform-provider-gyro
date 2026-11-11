package provider

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// API 2.10.0: region tr-ist-1 (Gyro Cloud Istanbul) with the same gyro_server resource. Images come from
// gyro_images with region = "tr-ist-1"; up to 10 SSH keys at creation; no user_data.
func TestAccMockIstanbulServer(t *testing.T) {
	m, prov := mock(t)
	m.Ist = true
	var serverID int64
	cfg := func(power string) string {
		return prov + fmt.Sprintf(`
data "gyro_products" "ist" {
  region         = "tr-ist-1"
  available_only = true
}
data "gyro_images" "ist" {
  region    = "tr-ist-1"
  os_flavor = "ubuntu"
}
data "gyro_images" "all" {}

resource "gyro_ssh_key" "a" {
  name       = "laptop"
  public_key = %q
}
resource "gyro_ssh_key" "b" {
  name       = "ci"
  public_key = %q
}

resource "gyro_server" "ist" {
  plan          = "standart-2-4"
  region        = "istanbul"
  image         = "ubuntu-22.04"
  hostname      = "ist-1.example.com"
  ssh_key_ids   = [tonumber(gyro_ssh_key.a.id), tonumber(gyro_ssh_key.b.id)]
  power_state   = %q
  confirm_order = true
}
`, testPub, testPub2, power)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(s *terraform.State) error {
			if st, _, _, _, ok := m.ServerStatus(serverID); !ok || st != "cancelled" {
				return fmt.Errorf("server %d not cancelled on destroy (%s)", serverID, st)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg("running"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gyro_products.ist", "products.#", "1"),
					resource.TestCheckResourceAttr("data.gyro_products.ist", "products.0.plan", "standart-2-4"),
					resource.TestCheckResourceAttr("data.gyro_images.ist", "images.#", "2"),
					resource.TestCheckResourceAttr("data.gyro_images.ist", "images.0.slug", "ubuntu-22.04"),
					resource.TestCheckResourceAttr("data.gyro_images.all", "images.#", "2"),
					resource.TestCheckResourceAttr("gyro_server.ist", "status", "active"),
					resource.TestCheckResourceAttr("gyro_server.ist", "product_id", "1107"),
					resource.TestCheckResourceAttr("gyro_server.ist", "plan", "standart-2-4"),
					resource.TestCheckResourceAttr("gyro_server.ist", "region", "istanbul"),
					resource.TestCheckResourceAttr("gyro_server.ist", "cores", "2"),
					func(s *terraform.State) error {
						id, err := strconv.ParseInt(s.RootModule().Resources["gyro_server.ist"].Primary.ID, 10, 64)
						serverID = id
						return err
					},
				),
			},
			{ // in place: power off, no second order
				Config: cfg("stopped"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_server.ist", "power_state", "stopped"),
					func(*terraform.State) error {
						if m.Balance[1] != 5000-239.49*1.2 {
							return fmt.Errorf("charged more than one order: balance %.2f", m.Balance[1])
						}
						return nil
					},
				),
			},
		},
	})
}

const testPub2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMaSGq7HLrgtZRMTcLNJJhso8ARquHKDNSf5C/3KxKTv ci"
