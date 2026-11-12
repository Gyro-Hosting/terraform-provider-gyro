---
page_title: "gyro_server_template Resource - gyro"
subcategory: ""
description: |-
  A custom template captured from one of your tr-ist-1 (Istanbul) servers.
---

# gyro_server_template (Resource)

A custom template captured from one of your tr-ist-1 (Istanbul) servers. Use its `image` (`custom:<id>`) on `gyro_server` to create servers from it. Only the root disk is copied; new servers get a new host name, IP, root password, SSH host keys and machine id (cloud-init), your users, files and settings stay. Billed 0.05 EUR per GB-month of the space the template really uses (prepaid, hourly from the account balance); destroying the template stops the charge. At most 10 per account, visible only in your account. Images cannot be uploaded from outside. Gyro Public API 2.15.0.

## Example Usage

```terraform
resource "gyro_server_template" "golden" {
  server_id   = gyro_server.web.id
  name        = "web-golden"
  description = "nginx + app, 2026-11"
}

resource "gyro_server" "web2" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = gyro_server_template.golden.image
  hostname      = "web2.example.com"
  ssh_key_ids   = [gyro_ssh_key.laptop.id]
  confirm_order = true
}
```

## Schema

### Required

- `name` (String) 1-60 characters (letters, digits, space . _ -). Changed in place.
- `server_id` (Number) tr-ist-1 server to capture. Changing it captures a new template.

### Optional

- `description` (String) Up to 200 characters. Changed in place.
- `mode` (String) `snapshot` (default: the server keeps running, the file system is frozen briefly) or `shutdown` (the server is stopped while it is copied, then started again). Changing it captures a new template.

### Read-Only

- `base_image` (String) Catalogue image the source server was installed from.
- `created_at` (String)
- `id` (String) The ID of this resource.
- `image` (String) Image slug for gyro_server: `custom:<id>`.
- `min_disk_gb` (Number) Smallest disk a server from this template can have (the plan disk must be at least this).
- `size_gb` (Number) Space the template really uses (billed).
- `status` (String) creating, ready, error, deleting or suspended (template storage suspended because the balance ran out).

## Import

Import is supported using the template id, e.g. `terraform import gyro_server_template.golden 12`.
