# E-mail once when the bucket reaches 80 % of the package (again after it drops below 95 % of that and fills up again).
resource "gyro_storage_bucket_alert" "assets_full" {
  service_id = 456
  bucket     = gyro_storage_bucket.assets.full_name
  type       = "percent"
  value      = 80
}

# Or an absolute size: e-mail when the "gyro-proje-yedek" bucket reaches 200 GB.
resource "gyro_storage_bucket_alert" "backup_200gb" {
  service_id = 456
  bucket     = "gyro-proje-yedek"
  type       = "gb"
  value      = 200
}
