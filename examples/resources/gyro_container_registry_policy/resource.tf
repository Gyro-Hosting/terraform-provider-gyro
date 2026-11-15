resource "gyro_container_registry_policy" "app" {
  service_id    = gyro_container_environment.app.id
  keep          = 5 # images of the last 5 revisions of every service stay
  untagged_days = 3 # other tags older than 3 days are deleted
}
