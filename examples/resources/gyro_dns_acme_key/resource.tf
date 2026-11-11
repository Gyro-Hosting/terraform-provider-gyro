# ACME key for Let's Encrypt DNS-01 (wildcard certificates). It can only add/remove _acme-challenge TXT records.
resource "gyro_dns_acme_key" "k8s" {
  service_id = 12345 # Gyro DNS service
  name       = "k8s-prod"
  zones      = ["example.com"]
}

# e.g. hand it to cert-manager
resource "kubernetes_secret" "gyro_acme" {
  metadata {
    name      = "gyro-acme"
    namespace = "cert-manager"
  }
  data = { token = gyro_dns_acme_key.k8s.token }
}
