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
