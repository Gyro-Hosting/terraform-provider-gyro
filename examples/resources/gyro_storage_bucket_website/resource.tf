# Single-page app (React / Vue / Angular) published from a bucket
resource "gyro_storage_bucket_website" "app" {
  service_id     = 456 # object storage service
  bucket         = gyro_storage_bucket.assets.name
  index_document = "index.html" # entry file (default index.html)
  spa            = true         # missing paths (/dashboard/settings, reloads) return index.html with 200
}

# Classic static site with its own 404 page
resource "gyro_storage_bucket_website" "docs" {
  service_id     = 456
  bucket         = "docs"
  error_document = "404.html"
}

output "app_url" { value = gyro_storage_bucket_website.app.url }
