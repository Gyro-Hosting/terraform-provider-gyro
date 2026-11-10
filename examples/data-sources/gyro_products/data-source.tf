data "gyro_products" "frankfurt_area" {
  region         = "eu-de-1"
  available_only = true
}

output "plans" {
  value = [for p in data.gyro_products.frankfurt_area.products : "${p.plan} (${p.name}): ${p.monthly_price} ${p.currency}"]
}
