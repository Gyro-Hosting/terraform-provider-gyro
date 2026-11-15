---
page_title: "gyro_container_logs Resource - gyro"
subcategory: ""
description: |-
  Log package and log alert rules of a Gyro Containers (ECS) environment.
---

# gyro_container_logs (Resource)

Log package and log alert rules of a Gyro Containers (ECS) environment. Free package: 7 days, 5 GB per month. Paid packages: `retention_days` 30 | 90 | 365 with `gb_month` 10 | 50 | 100 | 300 (365 days: up to 100), billed hourly from the prepaid balance. There is no overage bill: when the month's size is used up the oldest logs are deleted earlier and the `log_quota` alert is sent at 80 % and 100 %. Rules (at most 10) notify the environment's alert channels (`gyro_container_notifications`) when a text or level shows up at least `threshold` times within `window_minutes`. Destroy = free package and no rules.

## Example Usage

```terraform
resource "gyro_container_logs" "app" {
  service_id     = gyro_container_environment.app.id
  retention_days = 90 # 7 (free), 30, 90, 365
  gb_month       = 10 # free: 5; paid: 10, 50, 100, 300

  rules = [
    { name = "payment errors", service = "api", pattern = "/api/payment", threshold = 20, window_minutes = 5 },
    { name = "any error", level = "error", threshold = 200, window_minutes = 10 },
  ]
}
```

## Schema

### Required

- `service_id` (Number) Container environment (service) id.

### Optional

- `retention_days` (Number) 7 (free), 30, 90 or 365. Default 7.
- `gb_month` (Number) Monthly log volume of the package (5 with 7 days; 10, 50, 100, 300 for paid packages). Default 5.
- `rules` (Attributes List) Log alert rules (at most 10). See below.

### Read-Only

- `id` (String) The environment id.
- `monthly_eur` (Number) Monthly price of the package (EUR, VAT excluded; 0 = free).

### Nested Schema for `rules`

Required: `name` (String, max 60), `threshold` (Number, 1-100000), `window_minutes` (Number, 1-60).
Optional: `service` (String, empty = all services), `pattern` (String, line contains, case-insensitive), `level` (String: error, warn, info, debug), `enabled` (Bool, default true). `pattern` or `level` is required.
Read-Only: `id` (String).

## Import

```shell
terraform import gyro_container_logs.app 12345
```
