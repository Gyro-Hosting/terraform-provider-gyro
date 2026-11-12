# Serverless container sizes (vCPU / memory pairs) and prices
data "gyro_container_sizes" "all" {}

output "smallest" {
  value = {
    cpu_m     = data.gyro_container_sizes.all.sizes[0].cpu_m
    memory_mi = data.gyro_container_sizes.all.sizes[0].memory_mi[0]
    eur_hour  = data.gyro_container_sizes.all.sizes[0].hourly_eur_min
  }
}
