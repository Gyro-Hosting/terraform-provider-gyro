package provider

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const testPub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFaA/eWywO0ibNUDeE3EjFbGU0y4og3sMQAiwQYSi6Hv test"

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"gyro": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProviderSchema(t *testing.T) {
	p := New("test")()
	var resp fwprovider.SchemaResponse
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if !resp.Schema.Attributes["token"].IsSensitive() {
		t.Fatal("token must be sensitive")
	}
	if n := len(p.Resources(context.Background())); n != 30 {
		t.Fatalf("resources: %d", n)
	}
	if n := len(p.DataSources(context.Background())); n != 7 {
		t.Fatalf("data sources: %d", n)
	}
}

func mock(t *testing.T) (*mockapi.API, string) {
	m := mockapi.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
}

// Acceptance test against the in-process mock API (TF_ACC=1, real terraform binary).
func TestAccMockServerLifecycle(t *testing.T) {
	m, prov := mock(t)
	var serverID int64
	cfg := func(host, power string) string {
		return prov + fmt.Sprintf(`
data "gyro_products" "de" {
  region         = "eu-de-1"
  available_only = true
}
data "gyro_locations" "all" {}
data "gyro_images" "debian" { os_flavor = "debian" }

resource "gyro_ssh_key" "me" {
  name       = "laptop"
  public_key = %q
}

resource "gyro_server" "web" {
  plan          = "eko-2-4"
  region        = "eu-de-1"
  image         = data.gyro_images.debian.images[0].slug
  hostname      = %q
  ssh_key_ids   = [tonumber(gyro_ssh_key.me.id)]
  power_state   = %q
  confirm_order = true
}
`, testPub, host, power)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(s *terraform.State) error {
			st, cancel, _, _, ok := m.ServerStatus(serverID)
			if !ok || st != "cancelled" {
				return fmt.Errorf("server %d not cancelled on destroy (status=%s cancel=%s)", serverID, st, cancel)
			}
			if m.KeyExists(1) {
				return fmt.Errorf("ssh key not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg("web-1.example.com", "running"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gyro_products.de", "products.#", "2"),
					resource.TestCheckResourceAttr("data.gyro_products.de", "products.0.plan", "eko-2-4"),
					resource.TestCheckResourceAttr("data.gyro_products.de", "products.0.region", "eu-de-1"),
					resource.TestCheckResourceAttr("data.gyro_locations.all", "locations.#", "2"),
					resource.TestCheckResourceAttr("data.gyro_locations.all", "locations.0.region", "eu-de-1"),
					resource.TestCheckResourceAttr("data.gyro_images.debian", "images.0.slug", "debian-12"),
					resource.TestCheckResourceAttrSet("gyro_ssh_key.me", "fingerprint"),
					resource.TestCheckResourceAttr("gyro_server.web", "status", "active"),
					resource.TestCheckResourceAttr("gyro_server.web", "power_state", "running"),
					resource.TestCheckResourceAttr("gyro_server.web", "product_id", "723"),
					resource.TestCheckResourceAttr("gyro_server.web", "plan", "eko-2-4"),
					resource.TestCheckResourceAttr("gyro_server.web", "region", "eu-de-1"),
					resource.TestCheckResourceAttr("gyro_server.web", "server_type", "lg-e24"),
					resource.TestCheckResourceAttr("gyro_server.web", "location", "dc-a"),
					resource.TestCheckResourceAttr("gyro_server.web", "cores", "2"),
					resource.TestCheckResourceAttrSet("gyro_server.web", "ipv4"),
					func(s *terraform.State) error {
						id, err := strconv.ParseInt(s.RootModule().Resources["gyro_server.web"].Primary.ID, 10, 64)
						serverID = id
						return err
					},
				),
			},
			{ // in-place: rename + power off (no replacement, no second order)
				Config: cfg("web-2.example.com", "stopped"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_server.web", "hostname", "web-2.example.com"),
					resource.TestCheckResourceAttr("gyro_server.web", "power_state", "stopped"),
					func(*terraform.State) error {
						if _, _, h, p, _ := m.ServerStatus(serverID); h != "web-2.example.com" || p != "stopped" {
							return fmt.Errorf("API state: %s %s", h, p)
						}
						if m.Balance[1] != 5000-553.49*1.2 {
							return fmt.Errorf("charged more than one order: balance %.2f", m.Balance[1])
						}
						return nil
					},
				),
			},
			{ResourceName: "gyro_server.web", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"ssh_key_ids", "user_data", "confirm_order", "image"}},
			{ResourceName: "gyro_ssh_key.me", ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAccMockConfirmRequired(t *testing.T) {
	m, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: prov + `
resource "gyro_server" "x" {
  product_id    = 723
  image         = "debian-12"
  confirm_order = false
}`,
			ExpectError: regexp.MustCompile(`Order not confirmed`),
		}},
	})
	if m.Balance[1] != 5000 {
		t.Fatal("confirm_order=false must not spend money")
	}
}

func TestAccMockInsufficientBalance(t *testing.T) {
	_, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`wck_testA`).ReplaceAllString(prov, "wck_testB") + `
resource "gyro_server" "x" {
  product_id    = 723
  image         = "debian-12"
  confirm_order = true
}`,
			ExpectError: regexp.MustCompile(`insufficient_balance`),
		}},
	})
}

// TestAccDevReadOnly runs against a real installation (GYRO_ACC_DEV=1, GYRO_ENDPOINT, GYRO_TOKEN):
// data sources + an SSH key round trip. It never orders a server.
func TestAccDevReadOnly(t *testing.T) {
	if os.Getenv("GYRO_ACC_DEV") != "1" {
		t.Skip("set GYRO_ACC_DEV=1 with GYRO_ENDPOINT and GYRO_TOKEN to run against a real API")
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
provider "gyro" {}
data "gyro_products" "all" {}
data "gyro_locations" "all" {}
data "gyro_images" "all" {}
resource "gyro_ssh_key" "acc" {
  name       = "tf-acc"
  public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOkLmCzuTdmhIFjbK4ZXdmFMp1Rw7yW12U6JGl8pO8lB tf-acc"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("data.gyro_products.all", "products.#", regexp.MustCompile(`^[1-9][0-9]*$`)),
				resource.TestMatchResourceAttr("data.gyro_locations.all", "locations.#", regexp.MustCompile(`^[1-9]`)),
				resource.TestMatchResourceAttr("data.gyro_images.all", "images.#", regexp.MustCompile(`^[1-9]`)),
				resource.TestCheckResourceAttrSet("gyro_ssh_key.acc", "fingerprint"),
			),
		}, {ResourceName: "gyro_ssh_key.acc", ImportState: true, ImportStateVerify: true}},
	})
}
