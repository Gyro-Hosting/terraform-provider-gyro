# New server + Network Firewall add-on + firewall rules in one apply.
resource "gyro_server" "db" {
  plan   = "standart-2-4"
  region = "tr-ist-1"
  image  = "ubuntu-24.04"
  name   = "gyro-proje-db"

  confirm_order = true
}

# Prepaid server: the add-on is charged hourly from the balance and removed at once on destroy.
resource "gyro_server_addon" "firewall" {
  service_id = tonumber(gyro_server.db.id)
  addon      = "firewall" # or addon_id / option_id from `gyro service addons available <id>`
}

resource "gyro_firewall" "db" {
  server_id      = tonumber(gyro_server.db.id)
  default_action = "deny"

  rule {
    action      = "allow"
    protocol    = "tcp"
    port        = "22"
    source      = "203.0.113.10/32"
    description = "ssh from the office"
  }

  depends_on = [gyro_server_addon.firewall] # waits until the add-on is active
}
