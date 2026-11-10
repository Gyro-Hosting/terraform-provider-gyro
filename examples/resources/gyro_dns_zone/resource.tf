resource "gyro_dns_zone" "example" {
  service_id = 789 # Gyro DNS service
  name       = "example.com"
  dnssec     = true
}

# Point the domain to the name servers (or create the TXT record) to activate the zone
output "nameservers" { value = gyro_dns_zone.example.nameservers }
output "verify_txt" { value = "${gyro_dns_zone.example.verification_txt_name} = ${gyro_dns_zone.example.verification_txt_value}" }
output "ds" { value = gyro_dns_zone.example.ds_records }
