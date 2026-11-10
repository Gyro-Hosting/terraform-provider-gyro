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
