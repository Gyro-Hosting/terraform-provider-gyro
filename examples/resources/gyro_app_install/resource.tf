# WARNING: installing an app rebuilds the server disk
resource "gyro_app_install" "wp" {
  server_id = tonumber(gyro_server.web.id)
  app       = "wordpress"
  domain    = "blog.example.com"
  email     = "admin@example.com"
}
