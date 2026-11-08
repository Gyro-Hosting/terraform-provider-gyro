package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Reseller flow against the mock: end-customer, its key, its webhook, services data source; then destroy.
func TestAccMockResellerFlow(t *testing.T) {
	m, prov := mock(t)
	cfg := func(name, events string) string {
		return prov + fmt.Sprintf(`
resource "gyro_reseller_customer" "acme" {
  name         = %q
  external_ref = "acme-001"
  allow_order  = true
}
resource "gyro_api_key" "acme_ci" {
  name        = "acme-ci"
  customer_id = tonumber(gyro_reseller_customer.acme.id)
  scopes      = ["cloud_read", "cloud_write", "services_read"]
  rate_limit  = 60
}
resource "gyro_webhook" "acme" {
  url         = "https://hooks.example.com/gyro"
  events      = [%s]
  customer_id = tonumber(gyro_reseller_customer.acme.id)
}
data "gyro_services" "all" {}
`, name, events)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if h, c, k := m.Counts(); h+c+k != 0 {
				return fmt.Errorf("left behind: hooks=%d customers=%d keys=%d", h, c, k)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg("Acme Ltd", `"service.active"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_reseller_customer.acme", "status", "active"),
					resource.TestMatchResourceAttr("gyro_api_key.acme_ci", "token", regexp.MustCompile(`^wck_`)),
					resource.TestCheckResourceAttr("gyro_webhook.acme", "enabled", "true"),
					resource.TestMatchResourceAttr("gyro_webhook.acme", "secret", regexp.MustCompile(`^[0-9a-f]{64}$`)),
					resource.TestCheckResourceAttr("data.gyro_services.all", "services.#", "0"),
				),
			},
			{ // in-place: rename customer, change webhook events (key unchanged)
				Config: cfg("Acme Limited", `"service.active", "invoice.paid"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_reseller_customer.acme", "name", "Acme Limited"),
					resource.TestCheckResourceAttr("gyro_webhook.acme", "events.#", "2"),
				),
			},
			{ResourceName: "gyro_reseller_customer.acme", ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAccMockWebhookSSRF(t *testing.T) {
	_, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:      prov + `resource "gyro_webhook" "bad" { url = "https://127.0.0.1/x" }`,
			ExpectError: regexp.MustCompile(`invalid_url`),
		}},
	})
}

// TestAccDevReseller runs the reseller resources against a real installation (reseller key in GYRO_TOKEN,
// GYRO_ACC_DEV_RESELLER=1, GYRO_WEBHOOK_URL = a public https receiver). Free: no order is placed.
func TestAccDevReseller(t *testing.T) {
	if os.Getenv("GYRO_ACC_DEV_RESELLER") != "1" {
		t.Skip("set GYRO_ACC_DEV_RESELLER=1, GYRO_ENDPOINT, GYRO_TOKEN (reseller) and GYRO_WEBHOOK_URL")
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "gyro" {}
resource "gyro_reseller_customer" "c" {
  name         = "tf-acc customer"
  external_ref = "tf-acc-%d"
}
resource "gyro_api_key" "k" {
  name        = "tf-acc"
  customer_id = tonumber(gyro_reseller_customer.c.id)
  scopes      = ["cloud_read", "services_read"]
}
resource "gyro_webhook" "w" {
  url    = %q
  events = ["service.active"]
}
data "gyro_services" "all" {}
`, time.Now().Unix(), os.Getenv("GYRO_WEBHOOK_URL")),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("gyro_api_key.k", "token", regexp.MustCompile(`^wck_[0-9A-Za-z]{32}$`)),
				resource.TestMatchResourceAttr("gyro_webhook.w", "secret", regexp.MustCompile(`^[0-9a-f]{64}$`)),
				resource.TestCheckResourceAttrSet("data.gyro_services.all", "services.#"),
			),
		}},
	})
}
