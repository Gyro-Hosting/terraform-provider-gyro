# Terraform Provider for Gyro (`gyro-hosting/gyro`)

*English below · [Türkçe](#türkçe)*

Manage [Gyro](https://www.gyrohosting.com) cloud servers and SSH keys with Terraform, on top of the
[Gyro Public API v1](https://www.gyrohosting.com/developers/).

```hcl
terraform {
  required_providers {
    gyro = { source = "gyro-hosting/gyro", version = "~> 0.1" }
  }
}

provider "gyro" {}   # export GYRO_TOKEN=wck_...

resource "gyro_ssh_key" "laptop" {
  name       = "laptop"
  public_key = file("~/.ssh/id_ed25519.pub")
}

resource "gyro_server" "web" {
  server_type   = "cx23"
  location      = "fsn1"
  image         = "debian-12"
  hostname      = "web-1.example.com"
  ssh_key_ids   = [tonumber(gyro_ssh_key.laptop.id)]
  confirm_order = true   # paid order from your account balance
}
```

| Kind | Name | Notes |
|---|---|---|
| Resource | `gyro_server` | create = paid order from balance (`confirm_order = true`, idempotent), in-place `hostname`, `power_state`, `auto_renew`; `image`, product, `cycle`, `ssh_key_ids`, `user_data` force replacement; destroy = cancellation; import by id |
| Resource | `gyro_ssh_key` | account-level public key; import by id |
| Data source | `gyro_products` | filters `location`, `server_type`, `available_only` |
| Data source | `gyro_locations` | |
| Data source | `gyro_images` | filter `os_flavor` |

## Authentication

Create a client API key in the client area (*Account → API*). Scopes:
`Module:Addons/GyroAPI/cloud_read` (always), `cloud_write` (keys, rename, power), `cloud_order` (create servers),
`cloud_delete` (destroy). Configure with `token` / `GYRO_TOKEN` and optionally `endpoint` / `GYRO_ENDPOINT`.

## Money safety

* `confirm_order` must be `true`; otherwise the plan fails before any call.
* Orders are paid from the account balance only (never a stored card); `402 insufficient_balance` stops the apply.
* Each create sends one `Idempotency-Key`; network retries replay the first answer instead of ordering twice.
* The server id is saved to state immediately after the order, so a timeout never leaves an untracked paid server.

## Development

```sh
go build ./...                    # provider + gyroctl
go test ./...                     # unit tests
TF_ACC=1 go test ./...            # + acceptance tests against the in-process mock API (needs terraform in PATH)
GYRO_ACC_DEV=1 GYRO_ENDPOINT=... GYRO_TOKEN=... TF_ACC=1 go test ./internal/provider -run TestAccDevReadOnly
go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name gyro
```

Local use without the registry: build the binary and add a `dev_overrides { "gyro-hosting/gyro" = "<dir>" }` block to `~/.terraformrc`.

`gyroctl` (in `cmd/gyroctl`) is a small CLI over the same client: `gyroctl servers`, `gyroctl action <id> reboot`,
`gyroctl create --product 723 --image debian-12 --dry-run`.

## License

[MPL-2.0](LICENSE)

---

## Türkçe

Gyro Hosting ([gyrohosting.com](https://www.gyrohosting.com)) bulut sunucularını ve SSH anahtarlarını Terraform ile
yönetmek için resmi sağlayıcı. Gyro Public API v1 üzerinde çalışır.

**Kurulum:** yukarıdaki `required_providers` bloğunu ekleyin, `terraform init` çalıştırın.

**Kimlik doğrulama:** müşteri panelinde *Hesabım → API* bölümünden bir API anahtarı (`wck_...`) oluşturun ve
`GYRO_TOKEN` ortam değişkenine verin (ya da `token` alanı). Yetkiler: `cloud_read` (her zaman), `cloud_write`
(anahtar, ad değiştirme, güç), `cloud_order` (sunucu oluşturma), `cloud_delete` (silme).

**Para güvenliği:** `gyro_server` oluşturmak bakiyeden ücretli sipariş verir; `confirm_order = true` olmadan plan
hata verir. Sipariş yalnız hesap bakiyesinden ödenir (kayıtlı kart kullanılmaz), yetersiz bakiyede (402) işlem durur.
Her oluşturma tek bir `Idempotency-Key` ile gider; ağ hatasında ikinci sipariş oluşmaz.

**Kaynaklar:** `gyro_server`, `gyro_ssh_key` · **Veri kaynakları:** `gyro_products`, `gyro_locations`, `gyro_images`.
Örnekler `examples/`, belgeler `docs/` altında ve Terraform Registry sayfasında.

Lisans: MPL-2.0.
