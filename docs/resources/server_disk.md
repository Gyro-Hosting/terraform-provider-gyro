---
page_title: "gyro_server_disk Resource - gyro"
subcategory: ""
description: |-
  One additional disk (class ssd or hdd) of a Gyro Cloud Istanbul (tr-ist-1) server.
---

# gyro_server_disk (Resource)

One additional disk (class `ssd` or `hdd`) of a Gyro Cloud Istanbul (tr-ist-1) server. Billed per GB hourly from the prepaid balance until destroyed (`per_gb_monthly_eur`; SSD 0.09 EUR, HDD 0.035 EUR per GB-month at release time), at least `minimum_hours` per disk. Up to 4 disks per server and a per-class allowance (SSD 500 GB by default); sizes are multiples of 10 GB. Changing `size_gb` grows the disk in place (never shrinks; not while the server has snapshots) - extend the partition / file system inside the server afterwards. The new disk is empty: partition, format and mount it yourself (e.g. with cloud-init). `hdd` is large storage for backups, archives and media - do not run databases on it. Destroy deletes the disk WITH its data.

## Example Usage

```terraform
# Fast SSD disk for data (grow in place by raising size_gb; never shrinks)
resource "gyro_server_disk" "data" {
  server_id = gyro_server.web.id
  size_gb   = 100
}

# Large storage (HDD) for backups / archives / media - when the hdd class is on sale
resource "gyro_server_disk" "archive" {
  server_id = gyro_server.web.id
  size_gb   = 500
  class     = "hdd"
}
```

## Schema

### Required

- `server_id` (Number) tr-ist-1 server id.
- `size_gb` (Number) Disk size in GB (multiple of 10). Can only grow.

### Optional

- `class` (String) `ssd` (fast, default) or `hdd` (large storage for backups, archives and media; not for databases). Changing it replaces the disk.

### Read-Only

- `disk_id` (Number) Disk id on the server.
- `id` (String) <server_id>/<disk_id>
- `minimum_hours` (Number) Every disk is billed at least this many hours.
- `monthly_eur` (Number) Current monthly price of this disk in EUR (VAT excluded).
- `name` (String) Display name.
- `per_gb_monthly_eur` (Number) Current price per GB-month of the disk's class in EUR (VAT excluded).

## Import

Import is supported using `<server_id>/<disk_id>`, e.g. `terraform import gyro_server_disk.data 123/2001`.
