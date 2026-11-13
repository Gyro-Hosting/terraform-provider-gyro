# What would two 0.5 vCPU / 1 GB serverless containers and one Istanbul server cost?
data "gyro_prepaid_estimate" "stack" {
  items = [
    { cpu_m = 500, memory_mi = 1024, count = 2, hours = 672 },
    { plan = "standart-2-4" },
  ]
}

output "monthly" {
  value = "${data.gyro_prepaid_estimate.stack.monthly} ${data.gyro_prepaid_estimate.stack.currency}"
}
