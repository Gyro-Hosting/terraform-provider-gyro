# Attach both groups to a server (order = evaluation order, max 5). Without gyro_firewall rules, inbound traffic
# that no group allows is denied.
resource "gyro_server_security_groups" "web1" {
  server_id          = 1234
  security_group_ids = [gyro_security_group.web.id, gyro_security_group.admin.id]
}
