# Image uploads to "resimler/" trigger your thumbnail service.
resource "gyro_storage_bucket_notification" "thumbnails" {
  service_id  = 456
  bucket      = gyro_storage_bucket.assets.full_name
  url         = "https://ornek.com/s3-olay"
  events      = ["storage.object.created"]
  prefix      = "resimler/"
  suffix      = [".jpg", ".jpeg", ".png", ".webp"]
  description = "Gyro Proje küçük resim"
}

# Verify X-Gyro-Signature in your application with this value (sha256=HMAC-SHA256(secret, timestamp + "." + raw body)).
output "event_secret" {
  value     = gyro_storage_bucket_notification.thumbnails.secret
  sensitive = true
}
