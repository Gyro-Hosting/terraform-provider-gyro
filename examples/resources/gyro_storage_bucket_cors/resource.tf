resource "gyro_storage_bucket_cors" "assets" {
  service_id = 456
  bucket     = gyro_storage_bucket.assets.name
  rules = [
    {
      id              = "browser-upload" # presigned PUT from your web app
      allowed_origins = ["https://www.example.com", "https://example.com"]
      allowed_methods = ["GET", "HEAD", "PUT", "POST"]
      allowed_headers = ["*"]
      expose_headers  = ["ETag"]
      max_age_seconds = 3600
    },
    {
      allowed_origins = ["*"] # public read for fonts / images
      allowed_methods = ["GET", "HEAD"]
    },
  ]
}
