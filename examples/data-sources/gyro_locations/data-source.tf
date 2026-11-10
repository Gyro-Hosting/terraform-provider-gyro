data "gyro_locations" "all" {}

output "regions" {
  value = { for l in data.gyro_locations.all.locations : l.region => "${l.city}, ${l.country}" }
}
