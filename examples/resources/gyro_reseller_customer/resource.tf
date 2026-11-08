# Reseller (bayi) accounts only
resource "gyro_reseller_customer" "acme" {
  name         = "Acme Ltd"
  external_ref = "acme-001"
  allow_order  = true # Acme's keys may order servers, paid from YOUR balance at your discount
}
