package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// API 2.12.0: gyro_server_rdns (PTR of one server address; tr-ist-1: default PTR on every address, custom names active at once (2.17.2)).
func TestAccMockServerRdns(t *testing.T) {
	m, prov := mock(t)
	m.Ist = true
	cfg := func(host string) string {
		return prov + fmt.Sprintf(`
resource "gyro_server" "s" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-22.04"
  confirm_order = true
}
resource "gyro_server_rdns" "r" {
  server_id = tonumber(gyro_server.s.id)
  ip        = "45.87.174.${100 + tonumber(gyro_server.s.id) %% 100}"
  hostname  = %q
}
`, host)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("mail.example.com"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_rdns.r", "hostname", "mail.example.com"),
				resource.TestCheckResourceAttr("gyro_server_rdns.r", "status", "active"),
				resource.TestCheckResourceAttr("gyro_server_rdns.r", "default", "false"),
			)},
			{Config: cfg("smtp.example.com"), Check: resource.TestCheckResourceAttr("gyro_server_rdns.r", "hostname", "smtp.example.com")},
			{ResourceName: "gyro_server_rdns.r", ImportState: true, ImportStateVerify: true},
		},
	})
}
