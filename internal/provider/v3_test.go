package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Typed product lines against the mock: mailbox + alias, bucket + access key, DNS record, one-click app on a server.
func TestAccMockProductLines(t *testing.T) {
	_, prov := mock(t)
	cfg := func(quota int, gotoAddr string) string {
		return prov + fmt.Sprintf(`
resource "gyro_mail_mailbox" "info" {
  service_id = 2000
  local_part = "info"
  name       = "Info"
  quota_mb   = %d
  password   = "S3cure-pass!x"
}
resource "gyro_mail_alias" "sales" {
  service_id = 2000
  local_part = "sales"
  goto       = [%q]
}
resource "gyro_storage_bucket" "assets" {
  service_id = 3000
  name       = "assets-bucket"
}
resource "gyro_storage_key" "ci" {
  service_id = 3000
  label      = "ci"
}
resource "gyro_domain_record" "www" {
  domain = "example.com"
  type   = "A"
  name   = "www"
  value  = "192.0.2.10"
}
resource "gyro_server" "app" {
  product_id    = 723
  image         = "debian-12"
  confirm_order = true
}
resource "gyro_app_install" "wp" {
  server_id = tonumber(gyro_server.app.id)
  app       = "wordpress"
}
`, quota, gotoAddr)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: cfg(2048, "ops@example.net"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_mail_mailbox.info", "email", "info@example.com"),
					resource.TestCheckResourceAttr("gyro_mail_mailbox.info", "quota_mb", "2048"),
					resource.TestCheckResourceAttr("gyro_mail_alias.sales", "address", "sales@example.com"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.assets", "vhost_url", "https://assets-bucket.s3.example"),
					resource.TestMatchResourceAttr("gyro_storage_key.ci", "secret_key", regexp.MustCompile(`^s{40}$`)),
					resource.TestCheckResourceAttr("gyro_domain_record.www", "identity", "1"),
					resource.TestCheckResourceAttr("gyro_app_install.wp", "status", "ready"),
					resource.TestCheckResourceAttr("gyro_app_install.wp", "credentials.user", "admin"),
				),
			},
			{ // in place: quota + alias target
				Config: cfg(4096, "team@example.net"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_mail_mailbox.info", "quota_mb", "4096"),
					resource.TestCheckResourceAttr("gyro_mail_alias.sales", "goto.0", "team@example.net"),
				),
			},
		},
	})
}

func TestAccMockMailWrongService(t *testing.T) {
	_, prov := mock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: prov + `resource "gyro_mail_mailbox" "x" {
  service_id = 9999
  local_part = "x"
  password   = "S3cure-pass!x"
}`,
			ExpectError: regexp.MustCompile(`service_not_found`),
		}},
	})
}
