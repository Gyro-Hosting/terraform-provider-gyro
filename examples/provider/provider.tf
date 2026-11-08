terraform {
  required_providers {
    gyro = {
      source  = "gyro-hosting/gyro"
      version = "~> 0.2"
    }
  }
}

# Reads GYRO_TOKEN (client API key, wck_...) from the environment.
provider "gyro" {}
