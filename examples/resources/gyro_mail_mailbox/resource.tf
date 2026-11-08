data "gyro_services" "mail" { product_line = "mail" }

resource "gyro_mail_mailbox" "info" {
  service_id = data.gyro_services.mail.services[0].id
  local_part = "info"
  name       = "Info"
  quota_mb   = 2048
  password   = var.info_password
}
