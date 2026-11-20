# Orderable sizes with EUR prices, and the servers a backup space can be bound to.
data "gyro_backup_space_plans" "all" {}

output "backup_plans" {
  value = [for p in data.gyro_backup_space_plans.all.plans : "${p.plan}: ${p.prices[0].amount} ${p.prices[0].currency}/${p.prices[0].cycle}"]
}
