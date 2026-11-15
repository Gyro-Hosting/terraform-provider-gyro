# <service_id>/<webhook_id>  (the secret cannot be read back; rotate it with: gyro storage events rotate-secret <service_id> <webhook_id>)
terraform import gyro_storage_bucket_notification.thumbnails 456/7
