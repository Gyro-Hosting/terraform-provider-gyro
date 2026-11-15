package provider

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// #3462 madde 12: gyro_service_label with manage_nickname = false owns only the tags - a nickname set elsewhere
// (gyro_container_environment.name / gyro_storage.name / panel) is never sent, read or cleared.
func TestAccMockServiceLabelTagsOnly(t *testing.T) {
	x := &labelMock{labels: map[int64]map[string]any{12345: {"service_id": 12345, "nickname": "gyro-proje", "tags": []any{}}}, base: mockapi.New()}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	nick := func(want any) resource.TestCheckFunc {
		return func(*terraform.State) error {
			x.mu.Lock()
			defer x.mu.Unlock()
			got := any(nil)
			if l := x.labels[12345]; l != nil {
				got = l["nickname"]
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("remote nickname %v, want %v", got, want)
			}
			return nil
		}
	}
	cfg := func(tags string) string {
		return prov + fmt.Sprintf(`
resource "gyro_service_label" "s" {
  service_id      = 12345
  manage_nickname = false
  tags            = [%s]
}
`, tags)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg(`{ name = "production", color = "green" }`), Check: resource.ComposeAggregateTestCheckFunc(nick("gyro-proje"),
				resource.TestCheckNoResourceAttr("gyro_service_label.s", "nickname"),
				resource.TestCheckResourceAttr("gyro_service_label.s", "tags.0.name", "production"))},
			{Config: cfg(`{ name = "production", color = "green" }`), PlanOnly: true},
			{Config: cfg(`{ name = "api" }`), Check: nick("gyro-proje")},
			{Config: prov + `
resource "gyro_service_label" "s" {
  service_id      = 12345
  manage_nickname = false
  nickname        = "x"
}
`, PlanOnly: true, ExpectError: regexp.MustCompile(`manage_nickname = false`)},
			// destroy: tags cleared, the nickname stays
			{Config: prov, Check: nick("gyro-proje")},
		},
	})
}
