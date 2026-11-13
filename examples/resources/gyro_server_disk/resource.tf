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
