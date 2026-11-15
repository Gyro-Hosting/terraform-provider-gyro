---
page_title: "gyro_container_environment Resource - gyro"
subcategory: ""
description: |-
  A Gyro Containers (ECS) environment in Istanbul (tr-ist-1), prepaid hourly.
---

# gyro_container_environment (Resource)

A Gyro Containers (ECS) environment in Istanbul (tr-ist-1), prepaid: charged hourly from the account balance. List the plans with
`gyro container plans` (GET /cloud/containers/plans).

* `extra_nodes` adds nodes of the plan's size (same price as a plan node, at most 20 nodes in total). Lowering it drains the removed
  nodes first and is refused when the remaining nodes cannot hold the running services.
* `egress_approval_hours` (1-72) makes node changes wait for your approval before new egress IPs carry traffic (0 = off). Approve with
  `gyro container egress-change approve <id> --yes` or `POST /cloud/containers/{id}/egress-change/approve`.
* **destroy deletes the environment at once** (same as Spending > Delete in the panel): hourly charges stop; every service, node,
  registry image and pipeline is deleted. Use `lifecycle { prevent_destroy = true }` for environments you must keep.
* `plan` never triggers a replacement: a plan change is refused (do it in the panel, then set the new value).

The data source of the same name reads an existing environment.

## Example Usage

```terraform
resource "gyro_container_environment" "app" {
  plan                  = "pro" # GET /cloud/containers/plans
  name                  = "Gyro Project"
  extra_nodes           = 1  # same price as a plan node, billed hourly
  egress_approval_hours = 24 # new egress IPs wait for your approval (0 = off)

  lifecycle { prevent_destroy = true }
}
```

## Schema

### Required
- `plan` (String) mini-1-1 | small-1-2 | starter | standart | pro | serverless.

### Optional
- `name` (String) Environment name (nickname, max 60). Changes in place; when not set, the name on the platform (panel) is kept.
- `extra_nodes` (Number) Extra nodes on top of the plan, 0-19 (default 0). Not for serverless.
- `egress_approval_hours` (Number) Approval window for egress IP changes, 0-72 hours (default 0 = off, 24 recommended).

### Read-Only
- `id` (String), `service_id` (Number), `code` (String), `namespace` (String), `mode` (String: dedicated | serverless)
- `node_count` (Number) Plan nodes + extra nodes.
- `egress_ips` (List of String) Current egress IPv4 addresses.
- `pending_egress_ips` (List of String) New egress IPs announced by a node change and not in use for sure yet.

## Import

```shell
terraform import gyro_container_environment.app 12345
```
