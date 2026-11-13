package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// API 2.20.0: gyro_storage_bucket name change = in-place rename. Object Storage: the S3 name changes;
// Protected Storage: display name changes, full_name (S3 name) stays.
func TestAccMockBucketRename(t *testing.T) {
	_, prov := mock(t)
	cfg := func(std, kl string) string {
		return prov + fmt.Sprintf(`
resource "gyro_storage_bucket" "std" {
  service_id = 3000
  name       = %q
}
resource "gyro_storage_bucket" "kl" {
  service_id = 3001
  name       = %q
}
`, std, kl)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg("assets", "arsiv"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_storage_bucket.std", "full_name", "assets"),
				resource.TestCheckResourceAttr("gyro_storage_bucket.kl", "full_name", "arsiv-ab12"),
			)},
			{Config: cfg("assets-v2", "yedek"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("gyro_storage_bucket.std", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("gyro_storage_bucket.kl", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("gyro_storage_bucket.std", "full_name", "assets-v2"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.std", "path_url", "https://s3.example/assets-v2"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.kl", "name", "yedek"),
					resource.TestCheckResourceAttr("gyro_storage_bucket.kl", "full_name", "arsiv-ab12"),
				)},
			{Config: cfg("assets-v2", "yedek"), PlanOnly: true},
		},
	})
}
