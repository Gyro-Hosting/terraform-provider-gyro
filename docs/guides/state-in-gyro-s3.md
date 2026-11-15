---
page_title: "Keeping Terraform state in Gyro storage"
subcategory: ""
description: |-
  Use the Terraform s3 backend with Gyro Protected Storage (with state locking) or Gyro Object Storage.
---

# Keeping Terraform state in Gyro storage

The Terraform `s3` backend works with both Gyro storage products. They differ in **state locking**
(`use_lockfile`, Terraform 1.10+), which needs conditional writes (`If-None-Match: *`) on the server:

| | Endpoint | Conditional writes | `use_lockfile` | Old versions of the state |
|---|---|---|---|---|
| **Protected Storage** (recommended for state) | `https://s3-kilit.gyrohosting.com` | yes (412 on an existing lock) | safe | kept (versioning is always on) |
| **Object Storage** | `https://s3.gyrohosting.com` | **no** (the header is ignored) | **unsafe: do not rely on it** | not kept |

Measured on 15 Nov 2026 with two concurrent `terraform apply` runs: on Protected Storage the second run stops with
`Error acquiring the state lock` (HTTP 412); on Object Storage both runs went ahead and overwrote each other's lock.

## Protected Storage (recommended)

```terraform
terraform {
  backend "s3" {
    bucket = "gyro-proje-tfstate"     # a bucket of your Protected Storage service
    key    = "prod/terraform.tfstate"
    region = "tr-ist-1"
    endpoints = {
      s3 = "https://s3-kilit.gyrohosting.com"
    }
    use_path_style              = true
    use_lockfile                = true # Terraform 1.10+: lock file next to the state
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
  }
}
```

Credentials: an access key of the service with read and write permission on the bucket, given as
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (or `access_key` / `secret_key` in a `-backend-config` file kept out of version control).
Every state write keeps the previous version, so a broken state can be restored from the bucket's versions. If you add an
Object Lock retention rule, keep it short (state files are rewritten on every apply; locked versions cannot be deleted until they expire).

## Object Storage

The same block with `endpoints = { s3 = "https://s3.gyrohosting.com" }` stores the state, but **leave `use_lockfile` out**:
the lock is not enforced there. Use it only when a single runner (one CI job at a time) applies the configuration.

## Notes

* Always `use_path_style = true`; the region name is `tr-ist-1`.
* `skip_s3_checksum = true` avoids the AWS-only checksum headers of newer Terraform versions.
* The state contains values of other resources (for example secrets), so give the access key to as few people as possible.
  For container secrets use `secrets_wo` on `gyro_container_service` (write-only, never stored in the state).
