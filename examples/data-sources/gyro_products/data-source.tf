data "gyro_products" "dedicated_fsn" {
  location = "fsn1"
}

output "cheapest" {
  value = [for p in data.gyro_products.dedicated_fsn.products : "${p.name}: ${p.monthly_price} ${p.currency}" if p.available]
}
