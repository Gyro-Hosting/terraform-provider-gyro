package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// API 2.17.4: GET /cloud/billing/container-sizes + POST /cloud/billing/estimate (serverless items).
type billingMock struct {
	base http.Handler
	body map[string]any
}

func (x *billingMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/client/cloud/billing/container-sizes":
		_, _ = w.Write([]byte(`{"data":{"product_id":1215,"plan":"ecs-sunucusuz","currency":"EUR","vcpu_hour_eur":0.0298,"gb_hour_eur":0.0024,
"environment_limit":{"vcpu":16,"memory_gb":32},"eur_rate":56.07,"local_currency":"TRY",
"sizes":[{"cpu_m":250,"vcpu":0.25,"memory_mi":[512,1024,2048],"hourly_eur_min":0.00865,"hourly_eur_max":0.01225},
{"cpu_m":500,"vcpu":0.5,"memory_mi":[1024,2048,3072,4096],"hourly_eur_min":0.0173,"hourly_eur_max":0.0245}]}}`))
	case "/api/v1/client/cloud/billing/estimate":
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &x.body)
		_, _ = w.Write([]byte(`{"data":{"items":[
{"product_id":1215,"plan":"ecs-sunucusuz","type":"serverless","count":2,"cpu_m":250,"memory_mi":512,"hourly":{"amount":0.97,"currency":"TRY"},"hourly_eur":0.0173,
 "monthly_cap":{"amount":708.13,"currency":"TRY"},"monthly_cap_eur":12.629,"capped":false,"hours":100,"total":{"amount":97,"currency":"TRY"},"total_eur":1.73},
{"product_id":1084,"plan":"standart-1-2","type":"server","count":1,"hourly":{"amount":0.29,"currency":"TRY"},"hourly_eur":0.0052,
 "monthly_cap":{"amount":195.69,"currency":"TRY"},"monthly_cap_eur":3.49,"capped":true}],
"hourly":{"amount":1.26,"currency":"TRY"},"daily":{"amount":30.27,"currency":"TRY"},"monthly_cap":{"amount":903.82,"currency":"TRY"},
"balance":{"amount":1084.25,"currency":"TRY"},"hours_covered":null,"order_requires":{"amount":30.27,"currency":"TRY"},"can_order":true}}`))
	default:
		x.base.ServeHTTP(w, r)
	}
}

func TestAccMockBillingDataSources(t *testing.T) {
	x := &billingMock{base: mockapi.New()}
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	cfg := fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
}
data "gyro_container_sizes" "s" {}
data "gyro_prepaid_estimate" "e" {
  items = [
    { cpu_m = data.gyro_container_sizes.s.sizes[0].cpu_m, memory_mi = data.gyro_container_sizes.s.sizes[0].memory_mi[0], count = 2, hours = 100 },
    { plan = "standart-1-2" },
  ]
}
`, srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.gyro_container_sizes.s", "sizes.#", "2"),
			resource.TestCheckResourceAttr("data.gyro_container_sizes.s", "sizes.0.memory_mi.#", "3"),
			resource.TestCheckResourceAttr("data.gyro_container_sizes.s", "vcpu_hour_eur", "0.0298"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "items.0.type", "serverless"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "items.0.capped", "false"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "items.0.total", "97"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "items.1.capped", "true"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "currency", "TRY"),
			resource.TestCheckResourceAttr("data.gyro_prepaid_estimate.e", "can_order", "true"),
			resource.TestCheckNoResourceAttr("data.gyro_prepaid_estimate.e", "hours_covered"),
			func(_ *terraform.State) error {
				it := x.body["items"].([]any)[0].(map[string]any)
				if it["type"] != "serverless" || it["cpu_m"].(float64) != 250 || it["memory_mi"].(float64) != 512 {
					return fmt.Errorf("unexpected request %v", x.body)
				}
				return nil
			},
		)}},
	})
}
