---
page_title: "gyro_server_ip Resource - gyro"
subcategory: ""
description: |-
  One additional IPv4 address of a Gyro Cloud Istanbul (tr-ist-1) server.
---

# gyro_server_ip (Resource)

One additional IPv4 address of a Gyro Cloud Istanbul (tr-ist-1) server. The address is taken from the pool and configured on the server automatically (/32 on the primary interface, kept across reboots and reinstalls). Billed hourly from the prepaid balance (default 2 EUR per address-month) until destroyed, at least `minimum_hours` (default 6) per address; up to 5 per server by default. Destroy releases the address (getting the same address back is not guaranteed). Reverse DNS: `gyro_server_rdns`.

## Example Usage

```terraform
resource "gyro_server_ip" "extra" {
  count     = 2
  server_id = gyro_server.web.id
}

output "extra_ips" {
  value = gyro_server_ip.extra[*].ip
}
```

## Schema

### Required

- `server_id` (Number) tr-ist-1 server id.

### Read-Only

- `added_at` (String)
- `applied_on_server` (Boolean) false while the server's guest agent has not confirmed the address yet.
- `hourly_eur` (Number) Current price per address-hour in EUR (VAT excluded).
- `id` (String) <server_id>/<ip>
- `ip` (String) The assigned IPv4 address.
- `minimum_hours` (Number) Every added address is billed at least this many hours (default 6), even if destroyed earlier. API 2.17.1+.
- `monthly_eur` (Number) Current price per address-month in EUR (VAT excluded).
- `reverse_dns` (String) Current PTR of the address (automatic default `<a-b-c-d>.ip.gyrohosting.com` unless set with `gyro_server_rdns`). API 2.17.2+.

## Import

Import is supported using `<server_id>/<ip>`, e.g. `terraform import gyro_server_ip.extra 123/31.58.248.200`.
