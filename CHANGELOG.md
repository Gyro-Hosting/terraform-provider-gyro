## 0.27.0 (2026-11-14)

FEATURES:
* **New data source `gyro_container_environment`** (Gyro Public API 2.27.0): Kubernetes `namespace`, in-cluster `service_domain`, the current
  `egress_ips` (the IPv4 addresses your containers reach the internet from - for IP allow lists of external services; dedicated = own nodes +
  the shared pool used during updates, serverless = shared pool), `egress_hash`, `build_egress_ips` (Gyro Pipeline) and `ingress_ips`.

ENHANCEMENTS:
* `gyro_container_service`: new read-only `internal_address` (`<name>.<namespace>.svc.cluster.local:80`) and `namespace`.
* `gyro_container_notifications`: new event `egress` (the egress IPs of the environment changed), enabled by default.

## 0.26.0 (2026-11-14)

FEATURES:
* **New data source `gyro_account_limits`** (Gyro Public API 2.26.0): every limit of the account with its usage (new-account limit of the prepaid
  cloud, Gyro Cloud Istanbul servers, templates, additional IPs/disks per server, API order caps, keys, webhooks, firewall rules and groups,
  storage, DNS, container plans, Pipeline, uptime monitors, projects). Use it in a `precondition` to fail at plan time.

ENHANCEMENTS:
* Errors caused by a limit now explain how to raise it: the diagnostic names the limit (`limit_key`, limit, usage) and prints the ready
  support ticket command (`gyro ticket create --department-id 3 ...`); rate limits say "retry later", plan limits say "upgrade the plan".

NOTES:
* Provider documentation: new "Limits" section. Account limits are raised with a support ticket. New accounts: at most 5 Gyro Cloud Istanbul
  servers and 1 container environment during the first 7 days (was 2 servers).

## 0.25.0 (2026-11-14)

FEATURES:
* **New resource `gyro_storage`** (Gyro Public API 2.25.0): order a Gyro Object Storage (S3) or Protected Storage service (`plan` from
  `gyro storage plans`, e.g. `standard-1tb` / `protected-250gb`; `cycle` monthly | quarterly | semiannually | annually; `name`;
  `object_lock_buckets` for protected plans). Paid from the account balance; waits until the first bucket and access key exist (`status = active`).
  `name` changes in place. `plan`, `cycle` and `object_lock_buckets` never trigger a replacement (that would delete the data): a change is refused
  at plan time - change the plan in the panel / `gyro service resize`, then update the value. Destroy cancels at once and deletes every bucket
  and object (`delete_immediately = false` files a cancellation request instead). Import with the service id.

NOTES:
* `gyro_server.plan`: the description now says clearly that changing `plan` in Terraform replaces the server (new server, old data deleted),
  and how to keep the data: resize in place first (`gyro service resize` / POST /cloud/services/{id}/resize keeps the disk and all data on
  tr-ist-1, the disk only grows, a restart of about 12 seconds only when needed), then set the new plan.

## 0.24.4 (2026-11-14)

BUG FIXES:
* `gyro_container_service` (`env`, `secrets`) and `gyro_container_pipeline` (`build_env`, `build_args`, `secrets`): a variable VALUE that starts
  with an invisible character (zero-width space U+200B, BOM U+FEFF, U+200C, U+200D, U+2060 - usually from copy-paste) now fails
  `terraform plan` with a clear message naming the variable (the value is never printed). The platform strips these characters, so the
  value came back different and Terraform reported an inconsistent result after apply and tainted the resource. Inner and trailing spaces
  and non-breaking spaces are kept by the platform and stay valid. Names were already validated in 0.24.3.

NOTES:
* Gyro Public API 2.24.4: a replica count outside the plan limit (`replicas`, `autoscaling.max`) is refused with 422 `invalid_replicas`
  and the error names the limit ("at most N copies per service").

## 0.24.3 (2026-11-14)

BUG FIXES:
* `gyro_container_service` (`env`, `secrets`) and `gyro_container_pipeline` (`build_env`, `build_args`, `secrets`): variable names are validated
  at plan time. A name with an invisible character (e.g. a zero-width space from copy-paste), whitespace, a leading digit or a reserved
  `GYRO_` / `KUBERNETES_` prefix now fails `terraform plan` with a clear message. Before, the platform cleaned the name, Terraform reported
  an inconsistent result after apply and the resource was tainted.

## 0.24.2 (2026-11-14)

NOTES:
* `gyro_prepaid_estimate` (Gyro Public API 2.24.2): serverless containers are billed for the full month again - no monthly cap; `hours` defaults
  to 730 (a full month) and `monthly` is 730 hours of running time. Replaces the 672-hour note of 0.24.1. Documentation only, no schema change.

## 0.24.1 (2026-11-13)

NOTES:
* `gyro_prepaid_estimate` (Gyro Public API 2.24.1): serverless containers use the same monthly basis as every prepaid product - at most 672
  charged hours a calendar month; `hours` defaults to 672 and `monthly` is 672 hours of running time. Documentation only, no schema change.

## 0.24.0 (2026-11-13)

FEATURES:
* `gyro_container_pipeline` (Gyro Public API 2.24.0): private repositories over SSH - `repo` accepts `git@host:org/repo.git` /
  `ssh://user@host[:port]/path`; `deploy_key = true` makes Gyro create a read-only ed25519 deploy key (exposes `deploy_key_public` and
  `deploy_key_fingerprint`; the private key never leaves Gyro, `false` deletes it); `ssh_host_fingerprint` confirms the host key of servers
  other than GitHub / GitLab / Bitbucket (whose keys are pinned). `git_provider = "archive"` creates a pipeline without a repository that
  builds uploaded .tar.gz / .zip archives (API `POST .../uploads`). `repo` is now optional (empty for archive pipelines).

## 0.23.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_container_pipeline` (Gyro Public API 2.23.0, `/cloud/containers/{id}/pipelines`): Gyro Pipeline for Gyro Containers (ECS):
  connects a GitHub, GitLab, Gitea or https git repository; pushes / tags build the Dockerfile in an isolated build environment and roll the target
  services out with automatic rollback. Exposes `webhook_url` and `webhook_secret`; `token` and build `secrets` are write-only. Import `<service_id>/<pipeline_id>`.

## 0.22.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_container_image` (Gyro Public API 2.22.0, `POST/GET /cloud/containers/{id}/registry/imports`): copies an image from
  Docker Hub, GitHub, Quay or any https registry into a Gyro Containers environment's private registry without `docker push` (linux/amd64),
  waits for the copy and exposes `image` for `gyro_container_service`. Optional `username`/`password` are used only for the copy and never
  stored by Gyro. Destroy deletes the tag (refused while a service uses it).

## 0.21.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_service_label` (Gyro Public API `GET/PUT /cloud/services/{id}/label`): nickname (1-60 characters) and up to 10
  colour tags for any existing service (cloud server, container environment, VDS, storage, DNS, mail, ...). The nickname is the service
  title in the client area; the product name and service number stay below it. Destroy clears the label. Import by service id. Closes #1.
* `gyro_services` data source: new computed `nickname` and `tags` (`name`, `color`) per service.

## 0.20.1 (2026-11-13)

DOCUMENTATION:
* Examples for `gyro_project` and `gyro_service_project` use neutral placeholder names and ids. No functional change.

## 0.20.0 (2026-11-13)

FEATURES:
* `gyro_storage_bucket`: changing `name` now **renames the bucket in place** (Gyro Public API 2.20.0) instead of replacing it. Object Storage: the S3 name
  (`full_name`) changes. Protected Storage: the display name and website address change, `full_name` (the S3 name) stays; versions and locks are never moved.
* `gyro_server_storage_mount`: Protected Storage buckets (API 2.20.0). New `accept_versioning` (required for read/write on Protected Storage) and computed
  `protected`; `read_only` now defaults to the API default (false on Object Storage, true on Protected Storage).

## 0.19.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_server_disk` (Gyro Public API 2.19.0 `/cloud/servers/{id}/disks`): additional disks for Gyro Cloud Istanbul
  (tr-ist-1) servers, class `ssd` (fast) or `hdd` (large storage), billed per GB hourly from the prepaid balance. Raising `size_gb`
  grows the disk in place; shrinking is refused at plan time; changing `class` replaces the disk. Import `<server_id>/<disk_id>`.

## 0.18.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_project` (Gyro Public API 2.18.0 `/cloud/projects`): customer projects to group services (name, description,
  color, icon). Deleting a project moves its services back to the default project (id 0). Import `<project_id>`.
* **New resource** `gyro_service_project`: put any existing service into a project; destroy moves it back to the default project.

ENHANCEMENTS:
* `gyro_server` and `gyro_load_balancer`: new optional `project_id`. Sent with the order (validated before anything is created); changing it
  later moves the service in place (no replacement). Ignored by older APIs without projects.

## 0.17.0 (2026-11-13)

FEATURES:
* **New resource** `gyro_server_storage_mount` (Gyro Public API 2.17.5 `/cloud/servers/{id}/storage-mounts`): mount a Gyro Object Storage
  bucket on a server as a folder (read-only or read/write, one narrow access key per mount). Applied automatically on Gyro Cloud Istanbul
  (tr-ist-1) servers; elsewhere `install_command` (sensitive, single use, 60 minutes). Import `<server_id>/<mount_id>`.

## 0.16.2 (2026-11-13)

FEATURES:
* **New data source** `gyro_container_sizes` (Gyro Public API 2.17.4 `GET /cloud/billing/container-sizes`): Gyro Containers Serverless
  vCPU / memory pairs accepted by `gyro_container_service` (`cpu_m` / `memory_mi`) and the per-hour prices.
* **New data source** `gyro_prepaid_estimate` (`POST /cloud/billing/estimate`): hourly / daily / monthly cost of prepaid plans and serverless
  containers (`cpu_m`, `memory_mi`, `count`, `hours`; no monthly cap, `capped = false`) and how long the balance lasts. Nothing is ordered.

ENHANCEMENTS:
* `gyro_server_ip`: new computed `reverse_dns` (automatic default PTR, API 2.17.2), `minimum_hours` (each address is billed at least this many
  hours, API 2.17.1), `hourly_eur` and `monthly_eur`.

## 0.16.1 (2026-11-13)

ENHANCEMENTS:
* `gyro_server_rdns` (Gyro Public API 2.17.2): tr-ist-1 reverse DNS is published automatically. Every address has a default PTR
  `<a-b-c-d>.ip.gyrohosting.com`; a custom `hostname` is live within seconds (`status` active). New computed `default` attribute;
  when an address is back on the default name the resource is removed from state (recreated on the next apply).

## 0.16.0 (2026-11-12)

FEATURES:
* `gyro_container_service`: new `autoscaling` attribute (`min`, `max`, `cpu_percent` default 70, `memory_percent` 0 = off; Gyro Public API 2.17.0
  `PUT /cloud/containers/{id}/services/{name}/autoscaling`). While set, the replica count follows the load and `replicas` is ignored (no drift).
  New `notification` attribute (`min_ready`): alert when fewer replicas are ready. Both are settings, not revisions.
* **New resource** `gyro_container_notifications`: alert channels of a Gyro Containers environment (e-mail, SMS, Telegram chat id, signed webhook,
  events, cooldown, resolved messages; `GET/PUT /cloud/containers/{id}/notifications`). `webhook_secret` (sensitive) verifies
  `X-Gyro-Signature = sha256=HMAC(secret, X-Gyro-Timestamp + "." + body)`. Import with the environment id. Acceptance tests (mock) included.

## 0.15.0 (2026-11-12)

FEATURES:
* **New data source** `gyro_apps`: one-click apps from `GET /cloud/apps` (Gyro Public API 2.16.1) with `min_ram_gb`, `min_disk_gb`, `min_cpu`,
  `needs_domain`, `popular_tr` ("Popular in Türkiye"), `version`, `license` and supported `os` images. Optional filters `category`,
  `popular_tr` and `os`. Use an `id` from it in `gyro_app_install.app`. The catalogue now has 74 apps (aaPanel, CyberPanel, Easypanel,
  Mailcow, GitLab CE, Zabbix, Joomla, Drupal, PrestaShop, Moodle, Invoice Ninja, Akaunting, ONLYOFFICE Docs, Immich, RustDesk, Appwrite,
  NocoDB and Plane were added).

## 0.14.0 (2026-11-12)

FEATURES:
* **New resource** `gyro_server_ip` (Gyro Public API 2.16.0): one additional IPv4 address of a tr-ist-1 server (`POST/GET /cloud/servers/{id}/ips`,
  `DELETE /cloud/servers/{id}/ips/{ip}`). The address is configured on the server automatically; billed hourly from the prepaid balance
  (default 2 EUR per address-month) until destroyed, up to 5 per server by default. Import with `<server_id>/<ip>`. Acceptance test (mock) included.
## 0.13.0 (2026-11-12)

FEATURES:
* **New resource** `gyro_server_template` (Gyro Public API 2.15.0): capture one of your tr-ist-1 servers as a custom template (`mode` snapshot or
  shutdown), rename it in place, import by id. Billed 0.05 EUR per GB-month of the space it really uses (prepaid, hourly); destroy stops the charge.
* `gyro_server`: `image = "custom:<id>"` (e.g. `gyro_server_template.golden.image`) orders a tr-ist-1 server installed from your template
  (`POST /cloud/templates/{id}/servers`, same confirm / Idempotency-Key / balance rules). Mock API and acceptance test included.
## 0.12.2 (2026-11-12)

ENHANCEMENTS:
* `gyro_server` in region `tr-ist-1` (Gyro Cloud Istanbul, Gyro Public API 2.14.0): `user_data` is accepted (cloud-init: `#cloud-config` or a `#!` script)
  and all `ssh_key_ids` (up to 10) are installed for root at creation through cloud-init (about 25 s from order to SSH). Description and docs updated,
  mock API follows the 2.14.0 behaviour. No schema change.
## 0.12.1 (2026-11-12)

BUG FIXES:
* `gyro_storage_bucket`: a bucket is found in any live state (only `deleting` / `purged` buckets are ignored); 0.12.0 required the state `active`.
* Acceptance tests for Protected Storage (mock API service 3001).

## 0.12.0 (2026-11-12)

FEATURES:
* Gyro Protected Storage (versioning + object lock / WORM), Gyro Public API 2.14.0:
  * `gyro_storage_bucket`: new `object_lock` (Protected Storage only, forces a new bucket when set; cannot be turned off), computed `full_name`
    (real bucket name: `<name>-<service code>` on Protected Storage) and `versioning`. Buckets on Protected Storage are now found after create.
  * **New resource** `gyro_storage_bucket_object_lock`: default retention of a locked bucket (`mode` GOVERNANCE or COMPLIANCE, `days` 1-3650).
    COMPLIANCE needs `acknowledge_compliance = true`. Destroy leaves the rule in place and only forgets the resource (warning) unless
    `disable_on_destroy = true`; object locks already applied can never be removed. Import `<service_id>/<bucket>`.
  * **New resource** `gyro_storage_bucket_noncurrent_expiration`: delete old versions after `days` (0 = keep). Destroy restores the default (30 days).
    Import `<service_id>/<bucket>`.
* Existing `gyro_storage_bucket` states keep working without changes (no replacement planned).

## 0.11.1 (2026-11-12)

DOCUMENTATION:
* `gyro_firewall`: country sources (`country:XX` / `not-country:XX`) are no longer offered on the Istanbul network firewall; the `source`
  description now says so. Check `capabilities` of the firewall policy (Gyro Public API) for the features of the platform behind a server.
  No schema or behaviour change.

## 0.11.0 (2026-11-12)

FEATURES:
* **New resource** `gyro_server_rdns`: PTR of one server address (`server_id`, `ip`, `hostname`; computed `status`). In tr-ist-1 the record is a request
  (`pending` until approved) and a new address without a record gets one created. Destroy resets the address. Import `<server_id>/<ip>`.
* Requires Gyro Public API 2.13.0 for tr-ist-1 create/`status` (other regions work with older APIs).

## 0.10.0 (2026-11-11)

FEATURES:
* **New resource** `gyro_dns_acme_key`: ACME key of a Gyro DNS service for Let's Encrypt DNS-01 (wildcard certificates). The key can only add and
  remove `_acme-challenge` TXT records, optionally only in `zones` and only from `ips`; `token` (sensitive) is known right after creation. Every
  argument forces a new key. Import `<service_id>/<key_id>` (no token). Use it with acme.sh, Certbot, Caddy, Traefik or cert-manager
  (https://github.com/Gyro-Hosting/gyro-acme).
* Requires Gyro Public API 2.13.0.

## 0.9.0 (2026-11-11)

FEATURES:
* Region `tr-ist-1` (Gyro Cloud Istanbul) with the existing `gyro_server` resource: `plan` + `region = "tr-ist-1"` (or `istanbul`), images from
  `gyro_images` with `region = "tr-ist-1"`. Up to 10 `ssh_key_ids` at creation in tr-ist-1 (installed for root shortly after the first boot); `user_data` is not
  accepted there.
* Data source `gyro_images`: new optional `region` filter (API 2.10.0+; older APIs return the global list).
* Requires Gyro Public API 2.10.0 for the region filter; tr-ist-1 servers themselves work with the same API calls.
* **New resource** `gyro_container_service` (Gyro Containers / ECS, Istanbul): image, port, replicas, cpu_m, memory_mi, public, health_path, command, args,
  env, secrets (write only), domains, run_as_user; import `<service_id>/<name>`.
* Prepaid servers (tr-ist-1 is billed hourly from the account balance): the API reports `cycle = "hourly"`; the resource keeps the configured `cycle`
  (no replacement) and `price` is the hourly EUR price. Destroying always deletes at once.
* Requires Gyro Public API 2.11.0.

## 0.8.0 (2026-11-11)

FEATURES:
* **New resource** `gyro_security_group`: reusable, stateful allow lists for servers with the Network Firewall add-on (AWS security group model).
  `rule` blocks with `direction` (in/out), `type` (ssh, http, https, rdp, mysql, postgresql, mssql, redis, mongodb, smtp, smtps, imaps, dns_udp, dns_tcp,
  all_tcp, all_udp, all_icmp, all, custom_tcp, custom_udp), `port` (custom types), `source` (CIDR list, default 0.0.0.0/0) and `description`; computed
  `rule_id`, `status` and `servers`. Renaming keeps the group. `confirm_trial` (default true) confirms a change that the API runs as a 5 minute trial
  (could cut SSH/RDP). Import by group id (sg-xxxxxxxx).
* **New resource** `gyro_server_security_groups`: the exact, ordered list of groups attached to a server (max 5); waits while the server applies the
  previous change. Destroy detaches all groups. Import by server id.
* Requires Gyro Public API 2.9.0 (`/cloud/security-groups`, `/cloud/servers/{id}/security-groups`).

## 0.7.0 (2026-11-11)

FEATURES:
* **New resource** `gyro_firewall`: the network firewall of a server (Network Firewall add-on), modelled on AWS network ACLs / ufw.
  Ordered `rule` blocks (first match wins) with `action` (allow/deny), `protocol` (tcp, udp, tcp_udp, icmp, all), `source`
  (any, IPv4/CIDR list, `country:XX` / `not-country:XX`), `destination` (any or one IP of the server), `port` (any, 22, 80,443, 1000-2000),
  `direction` (in/out), `description`, plus `default_action` (allow/deny) for unmatched inbound traffic and `ping`.
  `permanent` (default true) skips the 5 minute trial; `accept_warnings` applies changes the API flags (lock-out risk, shadowed rules, private sources).
  The resource owns all customer rules of the server (rules applied by Gyro support are untouched); destroy removes them. Import by server id.
* Requires Gyro Public API 2.8.0 (`/cloud/servers/{id}/firewall/policy`).

## 0.6.0 (2026-11-10)

FEATURES:
* **New resource** `gyro_storage_bucket_website`: publish a bucket as a static website. `index_document` (entry file, default `index.html`), `error_document` (404 page),
  `spa` (single-page app mode: every missing path returns the entry file with status 200; real files are served as they are). Computed `url`.
  Creating it accepts the abuse rules (bucket becomes public); destroy disables the website and keeps the bucket. Import `<service_id>/<bucket>`.
* **New resource** `gyro_storage_bucket_cors`: authoritative CORS rules of a bucket (AWS S3 semantics: `allowed_origins`, `allowed_methods`, `allowed_headers`,
  `expose_headers`, `max_age_seconds`, optional `id`). Applies to the S3 endpoint, the static website and custom domains. Import `<service_id>/<bucket>`.
* Requires Gyro Public API 2.6.0 (`/cloud/storage/{id}/buckets/{bucket}/website|cors`).

## 0.5.0 (2026-11-10)

FEATURES:
* **New resource** `gyro_dns_zone`: a zone on Gyro DNS (ns1.gyrodns.net Istanbul + ns2.gyrodns.net Germany). Optional one-time BIND `zone_file` import,
  `dnssec` toggle with `ds_records`, ownership verification details (`status`, `verification_txt_name`, `verification_txt_value`). Import `<service_id>/<zone>`.
* **New resource** `gyro_dns_record`: one record set (name + type: A, AAAA, CNAME, MX, TXT, SRV, CAA, ALIAS, NS, PTR) with `ttl` and a set of `records`.
  Values written in short form (relative names, unquoted TXT) do not cause a diff after the API normalises them. Import `<service_id>/<zone>/<name>/<type>`.
* Requires Gyro Public API 2.5.0 (`/cloud/dns/{id}/...`).

## 0.4.0 (2026-11-10)

FEATURES:
* **New resource** `gyro_load_balancer`: managed L4/L7 load balancer (small/medium/large, EU regions eu-de-1, eu-de-2, eu-fi-1). Paid order from the account balance (`confirm_order`), services (tcp/http/https, health checks, sticky sessions, HTTPS certificates, HTTP→HTTPS redirect, PROXY protocol), targets (your cloud servers, all your cloud servers, dedicated server IPs, private IPs), algorithm. `services` and `targets` are authoritative when set; drift made in the panel or CLI is shown and corrected. Import by id.
* Requires Gyro Public API 2.4.0 (`/cloud/load-balancers`).

## 0.3.0 (2026-11-10)

FEATURES:
* `gyro_server`: new `plan` (e.g. `standart-2-4`) and `region` (e.g. `eu-de-1`, `tr-ist-1`) arguments, matching Gyro Public API 2.3.
  Both are filled from the API when not set (create with `product_id`, import, state from older versions).
* `gyro_products`: new `region` and `plan` filters; each product exposes `plan`, `region` and `product_line`.
* `gyro_locations`: each entry exposes `region`.
* New guide: *Upgrading to 0.3*.

DEPRECATIONS:
* `gyro_server.server_type` and `gyro_server.location` (use `plan` / `region`), the `gyro_products` filters `server_type` / `location`,
  and `gyro_locations.locations[*].code`. They keep working and print a warning; removal is planned for a future major version.

UPGRADE NOTES:
* No server is replaced when upgrading from 0.2.x: `plan`/`region` are written to state on the next refresh and `terraform plan` stays empty.
* Moving a configuration from `server_type`/`location` (or `product_id`) to `plan`/`region` that names the same product does not replace the server.
* Aliases accepted by the API (a city name as region, a region in `location`, ...) no longer cause a diff after apply.

## 0.2.0 (2026-11-09)

FEATURES:
* **New resources:** `gyro_webhook`, `gyro_reseller_customer`, `gyro_api_key`, `gyro_mail_mailbox`, `gyro_mail_alias`,
  `gyro_storage_bucket`, `gyro_storage_key`, `gyro_domain_record`, `gyro_app_install`
* **New data source:** `gyro_services` (every product line)
* `gyro_server`: new `delete_immediately` (default `true`): `terraform destroy` cancels at once instead of filing a cancellation request.
* Requires Gyro Public API 2.x (`/cloud/services`, typed `/cloud/mail|storage/...`, reseller endpoints).

## 0.1.0

* Initial release: `gyro_server`, `gyro_ssh_key`, data sources `gyro_products`, `gyro_locations`, `gyro_images`.
