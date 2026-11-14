# Object Storage (S3) 1 TB, billed monthly from the account balance
resource "gyro_storage" "yedek" {
  plan  = "standard-1tb"
  cycle = "monthly"
  name  = "gyro-proje-yedek"

  # destroy deletes every bucket and object of the service
  lifecycle {
    prevent_destroy = true
  }
}

# Protected Storage with 2 object lock (WORM) buckets
resource "gyro_storage" "worm" {
  plan                = "protected-250gb"
  cycle               = "annually"
  object_lock_buckets = 2
}

resource "gyro_storage_bucket" "dosyalar" {
  service_id = gyro_storage.yedek.service_id
  name       = "gyro-proje-dosyalar"
}
