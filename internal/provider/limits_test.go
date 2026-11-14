package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// API 2.26.0: GET /cloud/account/limits + the limit hint in refusals.
type limitsMock struct{ base http.Handler }

func (x *limitsMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/v1/client/cloud/account/limits":
		_, _ = w.Write([]byte(`{"data":{"account_id":12345,"new_account":{"applies":true,"period_days":7,"ends_at":"2026-11-21 10:00:00",
"servers":{"limit":5,"used":2},"container_environments":{"limit":1,"used":0}},
"limits":[{"key":"compute.max_vms","group":"compute","scope":"account","limit":10,"used":2,"unit":"servers","custom":false,"increasable":true,"how_to_increase":"open_support_ticket","description":"x"},
{"key":"s3.max_buckets","group":"s3","scope":"service","limit":null,"used":null,"unit":"buckets","custom":false,"increasable":true,"how_to_increase":"upgrade_plan_or_open_support_ticket","description":"y",
 "services":[{"service_id":12345,"name":"Gyro Proje","limit":5,"used":1}]}]}}`))
	case r.URL.Path == "/api/v1/client/cloud/ssh-keys" && r.Method == http.MethodPost:
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":{"code":"ssh_key_limit","message":"SSH key limit reached (50). This limit can be raised with a support ticket.",
"details":{"max":50,"limit_exceeded":true,"limit_key":"api.ssh_keys","limit":50,"current":null,"how_to_increase":"open_support_ticket",
"support_ticket":{"cli":"gyro ticket create --department-id 3 --subject \"Limit increase: api.ssh_keys\" --message \"...\""}}}}`))
	default:
		x.base.ServeHTTP(w, r)
	}
}

func TestAccMockAccountLimits(t *testing.T) {
	srv := httptest.NewServer(&limitsMock{base: mockapi.New()})
	t.Cleanup(srv.Close)
	prov := fmt.Sprintf("provider \"gyro\" {\n  endpoint = %q\n  token    = \"wck_testA\"\n}\n", srv.URL+"/api/v1/client")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: prov + `data "gyro_account_limits" "l" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gyro_account_limits.l", "new_account_servers", "5"),
					resource.TestCheckResourceAttr("data.gyro_account_limits.l", "new_account_servers_used", "2"),
					resource.TestCheckResourceAttr("data.gyro_account_limits.l", "limits.0.key", "compute.max_vms"),
					resource.TestCheckResourceAttr("data.gyro_account_limits.l", "limits.0.limit", "10"),
					resource.TestCheckResourceAttr("data.gyro_account_limits.l", "limits.1.services.0.limit", "5"),
				)},
			{Config: prov + `resource "gyro_ssh_key" "k" {
  name       = "gyro-proje"
  public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGyroProjeTestKeyAAAAAAAAAAAAAAAAAAAAAAAAAAAA gyro"
}`, ExpectError: regexp.MustCompile(`(?s)raised with a support ticket.*gyro ticket create`)},
		},
	})
}
