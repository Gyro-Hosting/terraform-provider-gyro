# A subnet with a fixed range
resource "gyro_subnet" "app" {
  vpc_id = gyro_vpc.uretim.id
  name   = "app"
  cidr   = "10.10.1.0/24"
}

# Let the API pick a free /26 inside the VPC
resource "gyro_subnet" "db" {
  vpc_id = gyro_vpc.uretim.id
  name   = "db"
  prefix = 26
}
