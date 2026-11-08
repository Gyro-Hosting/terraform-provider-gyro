resource "gyro_storage_key" "ci" {
  service_id = 456
  label      = "ci-uploader"
}

output "s3_secret" {
  value     = gyro_storage_key.ci.secret_key
  sensitive = true
}
