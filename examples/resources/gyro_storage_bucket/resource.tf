resource "gyro_storage_bucket" "assets" {
  service_id = 456
  name       = "assets"
}

# Protected Storage: versioned bucket with object lock (WORM).
# The real bucket name is "archive-<service code>": use full_name in S3 clients and other resources.
resource "gyro_storage_bucket" "archive" {
  service_id  = 789
  name        = "archive"
  object_lock = true
}
