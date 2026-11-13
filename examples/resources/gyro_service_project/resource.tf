# Put any existing service (DNS, storage, mail, containers, VDS ...) into a project.
resource "gyro_service_project" "dns" {
  service_id = 12345
  project_id = tonumber(gyro_project.gyroproje.id)
}
