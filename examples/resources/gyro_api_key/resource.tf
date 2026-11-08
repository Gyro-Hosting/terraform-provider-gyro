# A key for the reseller's end-customer: sees only Acme's services, cannot reach billing
resource "gyro_api_key" "acme_ci" {
  name        = "acme-ci"
  customer_id = tonumber(gyro_reseller_customer.acme.id)
  scopes      = ["cloud_read", "cloud_write", "services_read", "services_write"]
  actions     = ["reboot", "poweron", "snapshots:*"]
  rate_limit  = 60
}

output "acme_token" {
  value     = gyro_api_key.acme_ci.token
  sensitive = true
}
