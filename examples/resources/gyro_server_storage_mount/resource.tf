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
