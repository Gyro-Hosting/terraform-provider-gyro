# Lock every new object version for 30 days (GOVERNANCE: Gyro staff can bypass it on request).
resource "gyro_storage_bucket_object_lock" "archive" {
  service_id = gyro_storage_bucket.archive.service_id
  bucket     = gyro_storage_bucket.archive.full_name
  mode       = "GOVERNANCE"
  days       = 30
}

# COMPLIANCE: nobody (Gyro included) can delete or shorten the lock until it expires.
# resource "gyro_storage_bucket_object_lock" "audit" {
#   service_id             = 789
#   bucket                 = gyro_storage_bucket.audit.full_name
#   mode                   = "COMPLIANCE"
#   days                   = 365
#   acknowledge_compliance = true
# }
