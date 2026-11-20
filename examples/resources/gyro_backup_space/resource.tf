# 100 GB SFTP backup space bound to a server of the account, billed monthly from the balance.
# Only the server's IPv4 addresses can connect (sftp -P 2201 <user>@<host>, write into /veri).
resource "gyro_backup_space" "yedek" {
  plan      = "100gb"
  cycle     = "monthly"
  server_id = 12345
  name      = "gyro-proje-yedek"

  # destroy deletes the space and every file in it (cannot be undone)
  lifecycle {
    prevent_destroy = true
  }
}

output "sftp" {
  value = "sftp -P ${gyro_backup_space.yedek.port} ${gyro_backup_space.yedek.user}@${gyro_backup_space.yedek.host}"
}

# password is sensitive: pass it to a secret store / configuration, never print it
# gyro_backup_space.yedek.password
