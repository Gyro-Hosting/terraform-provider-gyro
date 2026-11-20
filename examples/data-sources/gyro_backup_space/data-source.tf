# Connection details and usage of an existing backup space (no password).
data "gyro_backup_space" "yedek" {
  id = 12345
}

output "backup_used_percent" {
  value = data.gyro_backup_space.yedek.used_percent
}
