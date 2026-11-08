resource "gyro_mail_alias" "sales" {
  service_id = 123
  local_part = "sales"
  goto       = ["alice@example.com", "bob@example.com"]
}
