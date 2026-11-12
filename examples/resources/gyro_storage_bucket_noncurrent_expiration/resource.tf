# Delete old (non-current) versions 7 days after they are replaced or deleted.
resource "gyro_storage_bucket_noncurrent_expiration" "archive" {
  service_id = gyro_storage_bucket.archive.service_id
  bucket     = gyro_storage_bucket.archive.full_name
  days       = 7
}
