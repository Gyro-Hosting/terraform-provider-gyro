# A project per business / client / environment. Destroying it only deletes the project:
# its services go back to the default project (id 0).
resource "gyro_project" "gyroproje" {
  name        = "Gyro Proje"
  description = "Web + API + CDN"
  color       = "blue"
  icon        = "rocket-takeoff"
}

# New servers and load balancers can be ordered straight into a project.
resource "gyro_server" "web" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-24.04"
  confirm_order = true
  project_id    = tonumber(gyro_project.gyroproje.id)
}
