terraform {
  required_providers {
    gyro = {
      source  = "gyro-hosting/gyro"
      version = "~> 0.3"
    }
  }
}

# Reads GYRO_TOKEN (client API key, wck_...) from the environment.
provider "gyro" {}

# Another account you are a panel user of (Sub-Accounts), with your own key (Gyro Public API 2.46.0).
# Permissions are the panel user permissions; list the ids with `gyro account list`.
provider "gyro" {
  alias      = "client"
  account_id = 12345
}
