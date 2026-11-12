# All one-click apps
data "gyro_apps" "all" {}

# Apps listed under "Popular in Türkiye" that run on Ubuntu 24.04
data "gyro_apps" "popular" {
  popular_tr = true
  os         = "ubuntu-24.04"
}

output "popular_app_ids" {
  value = [for a in data.gyro_apps.popular.apps : a.id]
}
