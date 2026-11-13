---
page_title: "gyro_server_storage_mount Resource - gyro"
subcategory: ""
description: |-
  Mounts a bucket of your Gyro Object Storage service on a Linux server as a folder.
---

# gyro_server_storage_mount (Resource)

Mounts a bucket of your Gyro Object Storage service on a Linux server (systemd) as a folder (rclone FUSE mount with a local write cache, starts on boot). Each mount has its own access key limited to that one bucket (read-only or read/write), removed on destroy. Gyro Cloud Istanbul (tr-ist-1) servers apply it automatically (`automatic = true`); on other servers run `install_command` once (valid 60 minutes, single use). Free; S3 traffic counts towards the storage service. Not available for Protected Storage buckets. Requires Gyro Public API 2.17.5.

## Example Usage

```terraform
resource "gyro_server_storage_mount" "backups" {
  server_id          = gyro_server.web.id
  storage_service_id = 17546
  bucket             = "backups"
  mount_point        = "/mnt/backups"
}

# On servers outside tr-ist-1 run the one-time command on the server (sensitive, valid 60 minutes):
output "mount_command" {
  value     = gyro_server_storage_mount.backups.automatic ? "" : gyro_server_storage_mount.backups.install_command
  sensitive = true
}
```

## Schema

### Required

- `bucket` (String) Bucket name in that service (renaming the bucket later does not break the mount).
- `server_id` (Number) Server service id.
- `storage_service_id` (Number) Your active Object Storage service id.

### Optional

- `mount_point` (String) Absolute path under /mnt, /srv, /data, /media, /opt, /home or /var/www; default /mnt/<bucket>. Must be empty or missing on the server.
- `read_only` (Boolean)

### Read-Only

- `automatic` (Boolean) true when applied by the Gyro Compute guest agent (tr-ist-1).
- `id` (String) <server_id>/<mount_id>
- `install_command` (String, Sensitive) One-line installer (only known right after create; single use, 60 minutes). Run it on the server as root unless automatic is true.
- `mount_id` (Number)
- `name` (String) systemd unit instance gyro-s3mount@<name>.
- `remove_command` (String) Cleans up the unit on a non tr-ist-1 server after destroy.
- `state` (String) pending | installing | active | error | expired (command not used within 60 minutes: taint to get a new one).

## Import

```shell
terraform import gyro_server_storage_mount.backups 17547/12
```
