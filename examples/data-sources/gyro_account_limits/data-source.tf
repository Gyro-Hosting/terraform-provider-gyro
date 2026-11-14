data "gyro_account_limits" "this" {}

locals {
  web_count = 3
}

# Fail at plan time instead of half-way through an apply when a new account would go over its limit.
# Account limits are raised with a support ticket: gyro ticket create --department-id 3 --subject "Limit increase: new_account.servers" --message "..."
resource "terraform_data" "limit_check" {
  lifecycle {
    precondition {
      condition = (!data.gyro_account_limits.this.new_account_applies ||
      data.gyro_account_limits.this.new_account_servers_used + local.web_count <= data.gyro_account_limits.this.new_account_servers)
      error_message = "Over the new-account server limit; open a support ticket to raise it (gyro ticket create --department-id 3 ...)."
    }
  }
}

output "limits" {
  value = { for l in data.gyro_account_limits.this.limits : l.key => "${coalesce(l.used, 0)} / ${l.limit == null ? "per plan" : l.limit}" }
}
