---
page_title: "gyro_server_rdns Resource - gyro"
subcategory: ""
description: |-
  Reverse DNS (PTR) record of one address of a server.
---

# gyro_server_rdns (Resource)

Reverse DNS (PTR) record of one address of a server. In region tr-ist-1 a PTR is a request that goes live after approval (status pending -> active); a new address without a record gets one created. Destroy resets the address to the default.

## Example Usage

```terraform
resource "gyro_server_rdns" "mail" {
  server_id = gyro_server.web.id
  ip        = gyro_server.web.ipv4
  hostname  = "mail.example.com"
}
```

## Schema

### Required

- `hostname` (String) Fully qualified host name for the PTR record, e.g. mail.example.com.
- `ip` (String) An address of this server.
- `server_id` (Number) Server id.

### Read-Only

- `id` (String) The ID of this resource.
- `status` (String) tr-ist-1: none | pending | active | rejected (empty in other regions).

## Import

Import is supported using `<server_id>/<ip>`, e.g. `terraform import gyro_server_rdns.mail 123/45.87.174.10`.
