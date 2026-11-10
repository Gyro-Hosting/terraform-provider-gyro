---
page_title: "Upgrading to 0.3"
subcategory: ""
description: |-
  plan and region replace the deprecated server_type and location arguments.
---

# Upgrading to 0.3

Version 0.3 follows Gyro Public API 2.3: servers and products are described by a **plan**
(for example `standart-2-4`, `pro-2-8`) and a **region** (for example `eu-de-1`, `tr-ist-1`).

| Before (deprecated) | Now |
|---|---|
| `gyro_server.server_type` | `gyro_server.plan` |
| `gyro_server.location` | `gyro_server.region` |
| `gyro_products` filter `server_type` / `location` | filter `plan` / `region` |
| `gyro_locations.locations[*].code` | `gyro_locations.locations[*].region` |

## What happens to existing servers

* Nothing is replaced. Upgrading the provider fills `plan` and `region` into state on the next refresh;
  `terraform plan` shows no changes.
* The deprecated arguments keep working and only print a deprecation warning. They will be removed in a
  future major version.
* You can switch a server's configuration from `server_type`/`location` (or from `product_id`) to
  `plan`/`region` at any time. As long as the values name the same product, the plan stays empty.

```terraform
resource "gyro_server" "web" {
  plan          = "standart-2-4" # was: server_type = "..."
  region        = "eu-de-1"      # was: location    = "..."
  image         = "debian-12"
  confirm_order = true
}
```

Find the names with the data sources:

```terraform
data "gyro_locations" "all" {}

data "gyro_products" "de" {
  region         = "eu-de-1"
  available_only = true
}

output "plans" {
  value = [for p in data.gyro_products.de.products : p.plan]
}
```

Changing `plan` or `region` to a different product still replaces the server (destroy, then create),
exactly like changing `product_id`.
