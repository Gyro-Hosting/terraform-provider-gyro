data "gyro_container_environment" "app" {
  service_id = 12345
}

# Egress IPs: put them on the IP allow list of an external service (database, payment provider, partner API).
# They change when the environment's nodes change; the owner is notified and this list is always current.
output "egress_ips" {
  value = data.gyro_container_environment.app.egress_ips
}

# Other services of the same environment reach a service at <service>.<namespace>.svc.cluster.local:80 (or <service>:80).
output "api_internal_url" {
  value = "http://api.${data.gyro_container_environment.app.service_domain}"
}
