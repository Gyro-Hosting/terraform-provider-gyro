# Give any service a nickname and colour tags. The nickname becomes the service title in the
# client area; the product name and service number stay below it.
resource "gyro_server" "api" {
  plan          = "standart-2-4"
  region        = "tr-ist-1"
  image         = "ubuntu-24.04"
  confirm_order = true
}

resource "gyro_service_label" "api" {
  service_id = tonumber(gyro_server.api.id)
  nickname   = "api-gateway"
  tags = [
    { name = "production", color = "green" },
    { name = "api" }, # colour omitted: a stable default is chosen by the API
  ]
}

# An existing service (e.g. a container environment) by its number.
resource "gyro_service_label" "ecs" {
  service_id = 12345
  nickname   = "gyro-ecs-1"
  tags       = [{ name = "staging", color = "orange" }]
}

# Only the tags of a container environment whose nickname comes from gyro_container_environment.name:
# manage_nickname = false never sends, reads or clears the nickname, so the two resources do not overwrite each other.
resource "gyro_service_label" "env_tags" {
  service_id      = gyro_container_environment.app.id
  manage_nickname = false
  tags            = [{ name = "production", color = "green" }]
}
