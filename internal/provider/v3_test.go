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

// Static website (entry file / SPA) and CORS rules on a bucket (GyroAPI 2.6.0) against the mock.
func TestAccMockStorageWebsiteCors(t *testing.T) {
	_, prov := mock(t)
	cfg := func(idx string, spa bool, origin string, withHeaders bool) string {
		hdr := ""
		if withHeaders {
			hdr = `allowed_headers = ["*"]
      expose_headers  = ["ETag"]`
		}
		return prov + fmt.Sprintf(`
resource "gyro_storage_bucket" "site" {
  service_id = 3000
  name       = "site"
}
resource "gyro_storage_bucket_website" "site" {
  service_id     = 3000
  bucket         = gyro_storage_bucket.site.name
  index_document = %q
  spa            = %v
}
resource "gyro_storage_bucket_cors" "site" {
  service_id = 3000
  bucket     = gyro_storage_bucket.site.name
  rules = [
    {
      id              = "upload"
      allowed_origins = [%q]
      allowed_methods = ["GET", "PUT"]
      %s
      max_age_seconds = 600
    },
    {
      allowed_origins = ["*"]
      allowed_methods = ["GET"]
    },
  ]
}
`, idx, spa, origin, hdr)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: cfg("app.html", true, "https://app.example.com", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_storage_bucket_website.site", "spa", "true"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_website.site", "index_document", "app.html"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_website.site", "error_document", ""),
					resource.TestCheckResourceAttr("gyro_storage_bucket_website.site", "url", "https://site-abcd.web.example"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_cors.site", "rules.#", "2"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_cors.site", "rules.0.allowed_headers.0", "*"),
					resource.TestCheckNoResourceAttr("gyro_storage_bucket_cors.site", "rules.1.allowed_headers"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_cors.site", "rules.1.max_age_seconds", "0"),
				),
			},
			{ // in place: entry file, SPA off, origin, headers removed
				Config: cfg("index.html", false, "https://www.example.com", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_storage_bucket_website.site", "spa", "false"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_cors.site", "rules.0.allowed_origins.0", "https://www.example.com"),
					resource.TestCheckNoResourceAttr("gyro_storage_bucket_cors.site", "rules.0.allowed_headers"),
				),
			},
			{ResourceName: "gyro_storage_bucket_website.site", ImportState: true, ImportStateId: "3000/site", ImportStateVerify: true},
		},
	})
}

// Protected Storage (API 2.14.0): locked bucket with the service code suffix, default retention, old version expiry.
func TestAccMockProtectedStorage(t *testing.T) {
	_, prov := mock(t)
	cfg := func(mode string, days int, ack bool, nc int) string {
		return prov + fmt.Sprintf(`
resource "gyro_storage_bucket" "arsiv" {
  service_id  = 3001
  name        = "arsiv"
  object_lock = true
}
resource "gyro_storage_bucket_object_lock" "arsiv" {
  service_id             = 3001
  bucket                 = gyro_storage_bucket.arsiv.full_name
  mode                   = %q
  days                   = %d
  acknowledge_compliance = %v
}
resource "gyro_storage_bucket_noncurrent_expiration" "arsiv" {
  service_id = 3001
  bucket     = gyro_storage_bucket.arsiv.full_name
  days       = %d
}
`, mode, days, ack, nc)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: cfg("GOVERNANCE", 30, false, 7),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_storage_bucket.arsiv", "full_name", "arsiv-ab12"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.arsiv", "object_lock", "true"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.arsiv", "versioning", "true"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_object_lock.arsiv", "mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_noncurrent_expiration.arsiv", "days", "7"),
				),
			},
			{Config: cfg("COMPLIANCE", 30, false, 7), ExpectError: regexp.MustCompile(`acknowledge_compliance`)},
			{
				Config: cfg("COMPLIANCE", 60, true, 0),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_storage_bucket_object_lock.arsiv", "mode", "COMPLIANCE"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_object_lock.arsiv", "days", "60"),
					resource.TestCheckResourceAttr("gyro_storage_bucket_noncurrent_expiration.arsiv", "days", "0"),
				),
			},
			{ResourceName: "gyro_storage_bucket_object_lock.arsiv", ImportState: true, ImportStateId: "3001/arsiv-ab12", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"disable_on_destroy"}},
		},
	})
}
