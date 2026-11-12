---
page_title: "gyro_server_rdns Resource - gyro"
subcategory: ""
description: |-
  Reverse DNS (PTR) record of one address of a server.
---

# gyro_server_rdns (Resource)

Reverse DNS (PTR) record of one address of a server. In region tr-ist-1 (Gyro Bulut İstanbul) every address already has an automatic default PTR `<a-b-c-d>.ip.gyrohosting.com`; this resource replaces it with your own name (live within seconds, status active). Names under Gyro domains and protected brand names are refused. Destroy resets the address to the default.

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
- `default` (Boolean) tr-ist-1: true when the address carries the automatic default name (the resource is then recreated).
- `status` (String) tr-ist-1: none | pending | active | rejected (empty in other regions).

## Import

Import is supported using `<server_id>/<ip>`, e.g. `terraform import gyro_server_rdns.mail 123/45.87.174.10`.
