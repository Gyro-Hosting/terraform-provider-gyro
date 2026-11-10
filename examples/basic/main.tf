resource "gyro_ssh_key" "laptop" {
  name       = "laptop"
  public_key = file("~/.ssh/id_ed25519.pub")
}

resource "gyro_server" "web" {
  plan          = "standart-2-4"
  region        = "eu-de-1"
  image         = "debian-12"
  hostname      = "web-1.example.com"
  ssh_key_ids   = [tonumber(gyro_ssh_key.laptop.id)]
  power_state   = "running"
  confirm_order = true # creating a server is a paid order from your account balance
}

output "web_ipv4" {
  value = gyro_server.web.ipv4
}
