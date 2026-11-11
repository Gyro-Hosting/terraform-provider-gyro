// Package mockapi is an in-memory stand-in for the Gyro Public API v1 used by unit and acceptance tests.
// It mirrors the real server's contract: bearer auth per owner (cross-tenant = 404), confirm + Idempotency-Key
// on paid orders, dry-run quotes, balance checks, async provisioning and 409 while a server is locked.
package mockapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type server struct {
	ID        int64
	Owner     int64
	ProductID int64
	Type, Loc string
	Plan, Reg string
	Image     string
	Hostname  string
	Status    string // inprocess -> active, cancelled
	Power     string
	Polls     int // GETs left until provisioning completes
	Cancel    string
	AutoRenew bool
	Cycle     string
	Snapshots []map[string]any
}

type key struct {
	ID          int64
	Owner       int64
	Name, Pub   string
	Fingerprint string
}

type idem struct {
	hash   string
	status int
	body   []byte
}

type webhook struct {
	ID, Owner, Customer int64
	URL, Desc, Status   string
	Events              []string
}

type customer struct {
	ID, Reseller      int64
	Name, Ref, Status string
	AllowOrder        bool
}

type subkey struct {
	ID, Owner, Customer int64
	Name                string
	Scopes              []string
	RateLimit           int64
}

// typed product-line state (mail service 2000, storage service 3000, domain example.com - all owner 1)
type typedState struct {
	mailboxes map[string]map[string]any
	aliases   map[int64]map[string]any
	buckets   map[string]bool
	web       map[string]map[string]any // bucket -> website settings (enabled, index_document, error_document, spa, url)
	cors      map[string][]any          // bucket -> rules (API shape)
	skeys     map[string]string
	dns       []map[string]any
	app       map[int64]map[string]any
	nextAlias int64
}

// API is safe for concurrent use.
type API struct {
	T         typedState
	hooks     map[int64]*webhook
	customers map[int64]*customer
	subkeys   map[int64]*subkey
	nextObj   int64
	Resellers map[int64]bool
	mu        sync.Mutex
	Tokens    map[string]int64  // token -> owner
	Balance   map[int64]float64 // owner -> TRY
	servers   map[int64]*server
	fw        map[int64]map[string]any // server -> network firewall policy (GyroAPI 2.8.0)
	sgs       map[string]map[string]any // security groups (GyroAPI 2.9.0)
	sgOwner   map[string]int64
	sgBind    map[int64][]string // server -> attached group ids
	nextSg    int64
	keys      map[int64]*key
	idem      map[string]idem
	nextSrv   int64
	nextKey   int64
	Calls     []string
	// ProvisionPolls is how many GETs a new server stays "inprocess" (default 1).
	ProvisionPolls int
	// Ist enables the tr-ist-1 (Istanbul) catalogue: one product, its own image list, up to 10 SSH keys at creation (API 2.10.0).
	Ist bool
}

var istProducts = []map[string]any{
	{"id": 1107, "name": "Gyro Bulut Standart 2-4", "plan": "standart-2-4", "region": "tr-ist-1", "product_line": "cloud_server", "server_type": "ist-1107", "location": "ist1", "cores": 2, "memory_gb": 4, "disk_gb": 50, "cpu_type": "dedicated", "architecture": "x86", "traffic_tb": nil, "ipv4": true, "ipv6": true, "available": true,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 239.49, "setup": 0, "currency": "TRY"}}},
}

var istImages = []map[string]any{{"slug": "alma-8", "name": "AlmaLinux 8", "os_flavor": "alma"}, {"slug": "debian-12", "name": "Debian 12", "os_flavor": "debian"},
	{"slug": "ubuntu-22.04", "name": "Ubuntu 22.04", "os_flavor": "ubuntu"}, {"slug": "ubuntu-24.04", "name": "Ubuntu 24.04", "os_flavor": "ubuntu"}}

func (a *API) catalog() []map[string]any {
	if !a.Ist {
		return products
	}
	return append(append([]map[string]any{}, products...), istProducts...)
}

func New() *API {
	return &API{
		Tokens:         map[string]int64{"wck_testA": 1, "wck_testB": 2},
		Balance:        map[int64]float64{1: 5000, 2: 0},
		servers:        map[int64]*server{},
		keys:           map[int64]*key{},
		idem:           map[string]idem{},
		nextSrv:        1000,
		nextKey:        1,
		ProvisionPolls: 1,
		hooks:          map[int64]*webhook{},
		customers:      map[int64]*customer{},
		subkeys:        map[int64]*subkey{},
		nextObj:        100,
		Resellers:      map[int64]bool{1: true},
		T: typedState{mailboxes: map[string]map[string]any{}, aliases: map[int64]map[string]any{}, buckets: map[string]bool{}, web: map[string]map[string]any{}, cors: map[string][]any{}, skeys: map[string]string{},
			app: map[int64]map[string]any{}, nextAlias: 1},
	}
}

var products = []map[string]any{
	{"id": 723, "name": "Gyro Bulut Eko 2-4", "plan": "eko-2-4", "region": "eu-de-1", "product_line": "cloud_server", "server_type": "lg-e24", "location": "dc-a", "cores": 2, "memory_gb": 4, "disk_gb": 40, "cpu_type": "shared", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": true,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 553.49, "setup": 0, "currency": "TRY"}}},
	{"id": 656, "name": "Gyro Bulut Pro 2-8", "plan": "pro-2-8", "region": "eu-de-1", "product_line": "cloud_server", "server_type": "lg-p28", "location": "dc-a", "cores": 2, "memory_gb": 8, "disk_gb": 80, "cpu_type": "dedicated", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": true,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 4015.99, "setup": 0, "currency": "TRY"}}},
	{"id": 687, "name": "Gyro Bulut Standart 1-1", "plan": "standart-1-1", "region": "eu-fi-1", "product_line": "cloud_server", "server_type": "lg-s11", "location": "dc-c", "cores": 1, "memory_gb": 1, "disk_gb": 20, "cpu_type": "shared", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": false,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 599.49, "setup": 0, "currency": "TRY"}}},
}

// regionCity mirrors the API: a region may also be given by its city name.
var regionCity = map[string]string{"eu-de-1": "falkenstein", "eu-fi-1": "helsinki", "tr-ist-1": "istanbul"}

// matchesPlan: the API accepts plan or the legacy server_type, and either value in either field.
func matchesPlan(p, body map[string]any) bool {
	for _, k := range []string{"plan", "server_type"} {
		if v, _ := body[k].(string); v != "" {
			return v == p["plan"] || v == p["server_type"]
		}
	}
	return false
}

// matchesRegion: region, legacy location or city name, in either field.
func matchesRegion(p, body map[string]any) bool {
	for _, k := range []string{"region", "location"} {
		if v, _ := body[k].(string); v != "" {
			return v == p["region"] || v == p["location"] || strings.EqualFold(regionCity[p["region"].(string)], v)
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Limit", "60")
	w.Header().Set("X-RateLimit-Remaining", "59")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func ok(w http.ResponseWriter, status int, data any, meta map[string]any) {
	body := map[string]any{"data": data}
	if meta != nil {
		body["meta"] = meta
	}
	writeJSON(w, status, body)
}

func (a *API) srvJSON(s *server, live bool) map[string]any {
	ip := fmt.Sprintf("192.0.2.%d", s.ID%250)
	out := map[string]any{"id": s.ID, "name": "Gyro Bulut", "hostname": s.Hostname, "status": s.Status, "product_id": s.ProductID,
		"plan": s.Plan, "region": s.Reg, "server_type": s.Type, "location": s.Loc, "ipv4": nil, "ipv6": nil, "provisioned": s.Status == "active" || s.Status == "cancelled",
		"cycle": s.Cycle, "price": map[string]any{"amount": 553.49, "currency": "TRY"}, "auto_renew": s.AutoRenew,
		"created_at": "2026-11-09T01:10:44+03:00", "due_date": "2026-12-09", "cancellation": nil}
	if s.Cancel != "" {
		out["cancellation"] = s.Cancel
	}
	if s.Status == "active" {
		out["ipv4"] = ip
		out["ipv6"] = "2001:db8::/64"
	}
	if live {
		if s.Status == "active" {
			out["live"] = map[string]any{"power_state": s.Power, "locked": false, "rescue_enabled": false, "backups_enabled": false,
				"ipv4": ip, "ipv6_network": "2001:db8::/64", "cores": 2, "memory_gb": 4, "disk_gb": 40, "image": s.Image, "image_name": s.Image,
				"region": s.Reg, "location": s.Loc, "traffic": map[string]any{"outgoing_bytes": 0, "included_bytes": 21990232555520}}
		} else {
			out["live"] = nil
		}
	}
	return out
}

func fingerprint(pub string) (string, bool) {
	f := strings.Fields(pub)
	if len(f) < 2 || !strings.HasPrefix(f[0], "ssh-") && !strings.HasPrefix(f[0], "ecdsa-") {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(b) < 20 {
		return "", false
	}
	h := sha256.Sum256(b)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(h[:]), "="), true
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Calls = append(a.Calls, r.Method+" "+r.URL.Path)
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/client"), "/")
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		fail(w, 401, "missing_token", "Missing API key.")
		return
	}
	owner, found := a.Tokens[strings.TrimPrefix(auth, "Bearer ")]
	if !found {
		fail(w, 401, "invalid_token", "Invalid API key.")
		return
	}
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if body == nil {
		body = map[string]any{}
	}
	seg := strings.Split(path, "/")
	id := func(i int) int64 {
		if i >= len(seg) {
			return 0
		}
		n, _ := strconv.ParseInt(seg[i], 10, 64)
		return n
	}
	m := r.Method
	switch {
	case seg[0] == "cloud" && (len(seg) >= 2 && seg[1] == "security-groups" || len(seg) >= 4 && seg[1] == "servers" && seg[3] == "security-groups"): // GyroAPI 2.9.0
		a.securityGroups(w, m, seg, owner, body, id)
		return
	case len(seg) >= 5 && seg[0] == "cloud" && seg[1] == "servers" && seg[3] == "firewall" && seg[4] == "policy": // GyroAPI 2.8.0
		if a.fw == nil {
			a.fw = map[int64]map[string]any{}
		}
		sv := a.servers[id(2)]
		if sv == nil || sv.Owner != owner {
			fail(w, 404, "server_not_found", "Server not found.")
			return
		}
		cur := a.fw[sv.ID]
		if cur == nil {
			cur = map[string]any{"server_id": sv.ID, "engine": "network", "default_action": "allow", "ping": true, "status": "none", "permanent": false, "rules": []any{}, "operator_rules": []any{}}
			a.fw[sv.ID] = cur
		}
		if len(seg) == 6 && seg[5] == "confirm" && m == "POST" {
			if cur["status"] != "trial" {
				fail(w, 422, "err_no_trial", "No trial to confirm.")
				return
			}
			cur["status"], cur["permanent"] = "active", true
		} else if m == "DELETE" {
			cur["rules"], cur["default_action"], cur["status"], cur["permanent"] = []any{}, "allow", "none", false
		} else if m == "PUT" {
			rl, _ := body["rules"].([]any)
			out := []any{}
			for i, x := range rl {
				r, _ := x.(map[string]any)
				src, _ := r["source"].(string)
				if src == "" || src == "any" {
					src = "any"
				} else if !strings.Contains(src, "/") && !strings.Contains(src, ":") {
					src += "/32" // the API normalises single addresses
				}
				if src == "0.0.0.0/32" {
					fail(w, 422, "err_source_zero", "0.0.0.0/32 matches no address.")
					return
				}
				get := func(k, d string) string {
					if v, ok := r[k].(string); ok && v != "" {
						return v
					}
					return d
				}
				out = append(out, map[string]any{"priority": i + 1, "type": get("type", "rule"), "direction": get("direction", "in"), "action": get("action", "allow"),
					"protocol": get("protocol", "tcp"), "source": src, "destination": get("destination", "any"), "port": get("port", "any"), "description": get("description", "")})
			}
			if body["default_action"] == "deny" && len(out) == 0 && body["confirm_warnings"] != true {
				fail(w, 422, "confirm_warnings_required", "The change has warnings.")
				return
			}
			da, _ := body["default_action"].(string)
			if da == "" {
				da = "allow"
			}
			cur["rules"], cur["default_action"] = out, da
			if p, ok := body["ping"].(bool); ok {
				cur["ping"] = p
			}
			if body["permanent"] == true {
				cur["status"], cur["permanent"] = "active", true
			} else {
				cur["status"], cur["permanent"] = "trial", false
			}
		}
		ok(w, 200, cur, nil)
	case m == "GET" && path == "whoami":
		ok(w, 200, map[string]any{"id": 1, "type": "client", "owner_id": owner, "permissions": []string{"Module:Addons/GyroAPI/*"}}, nil)
	case m == "GET" && path == "cloud/products":
		var out []map[string]any
		for _, p := range a.catalog() {
			q := r.URL.Query()
			if l := q.Get("location"); l != "" && p["location"] != l && p["region"] != l {
				continue
			}
			if t := q.Get("server_type"); t != "" && p["server_type"] != t && p["plan"] != t {
				continue
			}
			if l := q.Get("region"); l != "" && p["region"] != l && p["location"] != l && !strings.EqualFold(regionCity[p["region"].(string)], l) {
				continue
			}
			if t := q.Get("plan"); t != "" && p["plan"] != t && p["server_type"] != t {
				continue
			}
			out = append(out, p)
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0})
	case m == "GET" && path == "cloud/locations" && a.Ist:
		ok(w, 200, []map[string]any{{"region": "eu-de-1", "code": "dc-a", "city": "Falkenstein", "country": "DE", "zone": "eu-central", "products": 2}, {"region": "eu-fi-1", "code": "dc-c", "city": "Helsinki", "country": "FI", "zone": "eu-central", "products": 1}, {"region": "tr-ist-1", "code": "ist1", "city": "Istanbul", "country": "TR", "zone": "tr-ist", "products": 1}}, nil)
	case m == "GET" && path == "cloud/images" && a.Ist && r.URL.Query().Get("region") == "tr-ist-1":
		ok(w, 200, istImages, nil)
	case m == "GET" && path == "cloud/locations":
		ok(w, 200, []map[string]any{{"region": "eu-de-1", "code": "dc-a", "city": "Falkenstein", "country": "DE", "zone": "eu-central", "products": 2}, {"region": "eu-fi-1", "code": "dc-c", "city": "Helsinki", "country": "FI", "zone": "eu-central", "products": 1}}, nil)
	case m == "GET" && path == "cloud/images":
		ok(w, 200, []map[string]any{{"slug": "debian-12", "name": "Debian 12", "os_flavor": "debian"}, {"slug": "ubuntu-24.04", "name": "Ubuntu 24.04", "os_flavor": "ubuntu"}}, nil)
	case m == "GET" && path == "balance":
		ok(w, 200, map[string]any{"balance": map[string]any{"amount": a.Balance[owner], "currency": "TRY"}, "low_balance": false}, nil)
	case m == "GET" && path == "cloud/ssh-keys":
		out := []map[string]any{}
		for i := int64(1); i < a.nextKey; i++ {
			if k := a.keys[i]; k != nil && k.Owner == owner {
				out = append(out, map[string]any{"id": k.ID, "name": k.Name, "public_key": k.Pub, "fingerprint": k.Fingerprint, "created_at": "2026-11-09T01:00:00+03:00"})
			}
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0})
	case m == "POST" && path == "cloud/ssh-keys":
		name, _ := body["name"].(string)
		pub, _ := body["public_key"].(string)
		fp, valid := fingerprint(pub)
		if name == "" || !valid {
			fail(w, 422, "invalid_public_key", "public_key must be an OpenSSH public key.")
			return
		}
		for _, k := range a.keys {
			if k.Owner == owner && k.Fingerprint == fp {
				fail(w, 409, "ssh_key_exists", "An SSH key with this fingerprint already exists.")
				return
			}
		}
		k := &key{ID: a.nextKey, Owner: owner, Name: name, Pub: strings.TrimSpace(pub), Fingerprint: fp}
		a.keys[k.ID] = k
		a.nextKey++
		ok(w, 201, map[string]any{"id": k.ID, "name": k.Name, "public_key": k.Pub, "fingerprint": k.Fingerprint, "created_at": "2026-11-09T01:00:00+03:00"}, nil)
	case len(seg) == 3 && seg[0] == "cloud" && seg[1] == "ssh-keys":
		k := a.keys[id(2)]
		if k == nil || k.Owner != owner {
			fail(w, 404, "ssh_key_not_found", "SSH key not found.")
			return
		}
		if m == "DELETE" {
			delete(a.keys, k.ID)
			ok(w, 200, map[string]any{"deleted": true, "id": k.ID}, nil)
			return
		}
		ok(w, 200, map[string]any{"id": k.ID, "name": k.Name, "public_key": k.Pub, "fingerprint": k.Fingerprint, "created_at": "2026-11-09T01:00:00+03:00"}, nil)
	case m == "GET" && path == "cloud/servers":
		out := []map[string]any{}
		for i := int64(1000); i < a.nextSrv; i++ {
			if s := a.servers[i]; s != nil && s.Owner == owner {
				out = append(out, a.srvJSON(s, false))
			}
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0})
	case m == "POST" && path == "cloud/servers":
		a.create(w, r, owner, body)
	case path == "cloud/services" && m == "GET":
		out := []map[string]any{}
		for i := int64(1000); i < a.nextSrv; i++ {
			if s := a.servers[i]; s != nil && s.Owner == owner {
				out = append(out, map[string]any{"id": s.ID, "type": "server", "product_line": "cloud_server", "name": "Gyro Bulut", "status": s.Status,
					"product_id": s.ProductID, "cycle": s.Cycle, "price": map[string]any{"amount": 553.49, "currency": "TRY"}, "auto_renew": false})
			}
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0})
	case strings.HasPrefix(path, "cloud/mail/") || strings.HasPrefix(path, "cloud/storage/") || strings.HasPrefix(path, "domains/"):
		a.typed(w, m, owner, seg, body)
	case len(seg) == 4 && seg[0] == "cloud" && seg[1] == "servers" && seg[3] == "app":
		a.appRoute(w, m, owner, id(2), body)
	case strings.HasPrefix(path, "cloud/webhooks"):
		a.webhooks(w, m, owner, seg, body)
	case strings.HasPrefix(path, "cloud/reseller/customers"):
		a.reseller(w, m, owner, seg, body, r)
	case path == "cloud/keys" || strings.HasPrefix(path, "cloud/keys/"):
		a.keysRoute(w, m, owner, 0, seg, 2, body)
	case len(seg) >= 3 && seg[0] == "cloud" && seg[1] == "servers":
		s := a.servers[id(2)]
		if s == nil || s.Owner != owner {
			fail(w, 404, "server_not_found", "Server not found.")
			return
		}
		a.serverRoute(w, r, s, seg, body)
	default:
		fail(w, 404, "endpoint_not_found", "No API endpoint matches "+m+" /"+path+".")
	}
}

func (a *API) create(w http.ResponseWriter, r *http.Request, owner int64, body map[string]any) {
	raw, _ := json.Marshal(body)
	ik := r.Header.Get("Idempotency-Key")
	if ik != "" {
		if prev, seen := a.idem[fmt.Sprint(owner)+"|"+ik]; seen {
			if prev.hash != string(raw) {
				fail(w, 422, "idempotency_key_reuse", "This Idempotency-Key was already used with a different request.")
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(prev.status)
			_, _ = w.Write(prev.body)
			return
		}
	}
	pid := int64(0)
	if f, isNum := body["product_id"].(float64); isNum {
		pid = int64(f)
	}
	var prod map[string]any
	for _, p := range a.catalog() {
		if (pid > 0 && int64(p["id"].(int)) == pid) || (pid == 0 && matchesPlan(p, body) && matchesRegion(p, body)) {
			prod = p
		}
	}
	if prod == nil {
		fail(w, 422, "product_not_orderable", "product_id (or plan + region) must name an orderable product.")
		return
	}
	if !prod["available"].(bool) {
		fail(w, 422, "out_of_stock", "This product is out of stock.")
		return
	}
	img, _ := body["image"].(string)
	ist := prod["region"] == "tr-ist-1"
	if img != "debian-12" && img != "ubuntu-24.04" && !(ist && (img == "ubuntu-22.04" || img == "alma-8")) {
		fail(w, 422, "invalid_image", "image is not offered on this product.")
		return
	}
	if ud, _ := body["user_data"].(string); ist && ud != "" {
		fail(w, 422, "invalid_user_data", "This product does not accept user_data.")
		return
	}
	if ks, has := body["ssh_keys"].([]any); has && ((!ist && len(ks) > 1) || len(ks) > 10) {
		fail(w, 422, "too_many_ssh_keys", "too many SSH keys at creation.")
		return
	}
	if ks, has := body["ssh_keys"].([]any); has {
		for _, x := range ks {
			k := a.keys[int64(x.(float64))]
			if k == nil || k.Owner != owner {
				fail(w, 422, "ssh_key_not_found", "ssh_keys: key not found.")
				return
			}
		}
	}
	price := prod["prices"].([]map[string]any)[0]["amount"].(float64) * 1.2
	quote := map[string]any{"product_id": prod["id"], "cycle": "monthly", "total": map[string]any{"amount": price, "currency": "TRY"}}
	if body["dry_run"] == true {
		ok(w, 200, map[string]any{"dry_run": true, "quote": quote}, nil)
		return
	}
	if body["confirm"] != true {
		fail(w, 422, "confirm_required", "Creating a server charges your account balance. Repeat with \"confirm\": true.")
		return
	}
	if ik == "" {
		fail(w, 400, "idempotency_key_required", "An Idempotency-Key header is required when creating a server.")
		return
	}
	if a.Balance[owner] < price {
		fail(w, 402, "insufficient_balance", "Your account balance does not cover this order.")
		return
	}
	a.Balance[owner] -= price
	host, _ := body["hostname"].(string)
	cycle, _ := body["cycle"].(string)
	if cycle == "" {
		cycle = "monthly"
	}
	s := &server{ID: a.nextSrv, Owner: owner, ProductID: int64(prod["id"].(int)), Type: prod["server_type"].(string), Loc: prod["location"].(string),
		Plan: prod["plan"].(string), Reg: prod["region"].(string),
		Image: img, Hostname: host, Status: "inprocess", Power: "running", Polls: a.ProvisionPolls, Cycle: cycle}
	a.servers[s.ID] = s
	a.nextSrv++
	resp := map[string]any{"data": map[string]any{"server": a.srvJSON(s, false), "order": map[string]any{"id": s.ID + 60000, "number": fmt.Sprint(s.ID + 60000), "status": "inprocess"},
		"payment": map[string]any{"method": "balance", "status": "paid"}}}
	out, _ := json.Marshal(resp)
	a.idem[fmt.Sprint(owner)+"|"+ik] = idem{hash: string(raw), status: 202, body: out}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	_, _ = w.Write(out)
}

func (a *API) serverRoute(w http.ResponseWriter, r *http.Request, s *server, seg []string, body map[string]any) {
	m := r.Method
	switch {
	case len(seg) == 3 && m == "GET":
		if s.Status == "inprocess" {
			if s.Polls <= 0 {
				s.Status = "active"
			}
			s.Polls--
		}
		ok(w, 200, a.srvJSON(s, true), nil)
	case len(seg) == 3 && m == "PATCH":
		if h, has := body["hostname"].(string); has {
			s.Hostname = h
		}
		if ar, has := body["auto_renew"].(bool); has {
			s.AutoRenew = ar
		}
		ok(w, 200, a.srvJSON(s, true), nil)
	case len(seg) == 3 && m == "DELETE":
		if r.URL.Query().Get("confirm") != "true" && body["confirm"] != true {
			fail(w, 422, "confirm_required", "Repeat with confirm=true.")
			return
		}
		if r.URL.Query().Get("immediate") == "true" {
			s.Status = "cancelled"
			ok(w, 202, map[string]any{"status": "cancelled", "server_id": s.ID}, nil)
			return
		}
		s.Cancel = "pending"
		ok(w, 202, map[string]any{"status": "cancellation_requested", "server_id": s.ID}, nil)
	case len(seg) == 5 && seg[3] == "actions" && m == "POST":
		if s.Status != "active" {
			fail(w, 409, "not_actionable", "The server is not in an actionable state.")
			return
		}
		switch seg[4] {
		case "poweron":
			if s.Power == "running" {
				fail(w, 422, "tool_action_failed", "Server is already running.")
				return
			}
			s.Power = "running"
		case "shutdown", "poweroff":
			if s.Power == "stopped" {
				fail(w, 422, "tool_action_failed", "Server is already off.")
				return
			}
			s.Power = "stopped"
		case "reboot":
		case "rebuild", "reset":
			if body["confirm"] != true {
				fail(w, 422, "confirm_required", "This action is destructive.")
				return
			}
			if img, has := body["image"].(string); has {
				s.Image = img
			}
		default:
			fail(w, 404, "unknown_action", "Unknown action.")
			return
		}
		ok(w, 202, map[string]any{"action": seg[4], "status": "accepted", "server_id": s.ID, "message": "ok"}, nil)
	case len(seg) == 4 && seg[3] == "snapshots" && m == "GET":
		ok(w, 200, map[string]any{"items": s.Snapshots, "quota_gb": 20}, nil)
	case len(seg) == 4 && seg[3] == "snapshots" && m == "POST":
		s.Snapshots = append(s.Snapshots, map[string]any{"id": len(s.Snapshots) + 1, "description": "snap", "status": "available", "size_bytes": 1})
		ok(w, 202, map[string]any{"status": "accepted", "server_id": s.ID}, nil)
	default:
		fail(w, 404, "endpoint_not_found", "No API endpoint matches.")
	}
}

// ServerStatus is a test helper (owner-agnostic).
func (a *API) ServerStatus(id int64) (status, cancel, hostname, power string, exists bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.servers[id]
	if s == nil {
		return "", "", "", "", false
	}
	return s.Status, s.Cancel, s.Hostname, s.Power, true
}

// KeyExists is a test helper.
func (a *API) KeyExists(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.keys[id] != nil
}

func strs(v any) []string {
	out := []string{}
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (a *API) hookJSON(h *webhook) map[string]any {
	var cust any
	if h.Customer > 0 {
		cust = h.Customer
	}
	return map[string]any{"id": h.ID, "url": h.URL, "events": h.Events, "description": h.Desc, "customer_id": cust, "status": h.Status}
}

func (a *API) webhooks(w http.ResponseWriter, m string, owner int64, seg []string, body map[string]any) {
	if len(seg) == 2 && m == "POST" {
		u, _ := body["url"].(string)
		if !strings.HasPrefix(u, "https://") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") {
			fail(w, 422, "invalid_url", "url must be an https:// address on a public host.")
			return
		}
		h := &webhook{ID: a.nextObj, Owner: owner, URL: u, Events: strs(body["events"]), Status: "active"}
		h.Desc, _ = body["description"].(string)
		if c, isNum := body["customer_id"].(float64); isNum {
			h.Customer = int64(c)
		}
		a.nextObj++
		a.hooks[h.ID] = h
		out := a.hookJSON(h)
		out["secret"] = strings.Repeat("ab", 32)
		ok(w, 201, out, nil)
		return
	}
	if len(seg) == 2 && m == "GET" {
		out := []map[string]any{}
		for _, h := range a.hooks {
			if h.Owner == owner {
				out = append(out, a.hookJSON(h))
			}
		}
		ok(w, 200, out, nil)
		return
	}
	id, _ := strconv.ParseInt(seg[2], 10, 64)
	h := a.hooks[id]
	if h == nil || h.Owner != owner {
		fail(w, 404, "webhook_not_found", "Webhook not found.")
		return
	}
	switch m {
	case "GET":
		ok(w, 200, a.hookJSON(h), nil)
	case "PATCH":
		if u, has := body["url"].(string); has {
			h.URL = u
		}
		if _, has := body["events"]; has {
			h.Events = strs(body["events"])
		}
		if d, has := body["description"].(string); has {
			h.Desc = d
		}
		if st, has := body["status"].(string); has {
			h.Status = st
		}
		ok(w, 200, a.hookJSON(h), nil)
	case "DELETE":
		delete(a.hooks, id)
		ok(w, 200, map[string]any{"deleted": true}, nil)
	}
}

func (a *API) custJSON(c *customer) map[string]any {
	var ref any
	if c.Ref != "" {
		ref = c.Ref
	}
	return map[string]any{"id": c.ID, "name": c.Name, "external_ref": ref, "status": c.Status, "allow_order": c.AllowOrder, "services": 0, "email": nil, "note": nil}
}

func (a *API) reseller(w http.ResponseWriter, m string, owner int64, seg []string, body map[string]any, r *http.Request) {
	if !a.Resellers[owner] {
		fail(w, 403, "not_reseller", "Reseller endpoints need an active reseller account.")
		return
	}
	if len(seg) == 3 && m == "POST" {
		n, _ := body["name"].(string)
		if n == "" {
			fail(w, 422, "invalid_name", "name is required.")
			return
		}
		c := &customer{ID: a.nextObj, Reseller: owner, Name: n, Status: "active"}
		c.Ref, _ = body["external_ref"].(string)
		c.AllowOrder, _ = body["allow_order"].(bool)
		a.nextObj++
		a.customers[c.ID] = c
		ok(w, 201, a.custJSON(c), nil)
		return
	}
	if len(seg) < 4 {
		fail(w, 404, "endpoint_not_found", "")
		return
	}
	id, _ := strconv.ParseInt(seg[3], 10, 64)
	c := a.customers[id]
	if c == nil || c.Reseller != owner {
		fail(w, 404, "customer_not_found", "Customer not found.")
		return
	}
	if len(seg) >= 5 && seg[4] == "keys" {
		a.keysRoute(w, m, owner, id, seg, 5, body)
		return
	}
	switch m {
	case "GET":
		ok(w, 200, a.custJSON(c), nil)
	case "PATCH":
		if n, has := body["name"].(string); has {
			c.Name = n
		}
		if n, has := body["external_ref"].(string); has {
			c.Ref = n
		}
		if n, has := body["status"].(string); has {
			c.Status = n
		}
		if n, has := body["allow_order"].(bool); has {
			c.AllowOrder = n
		}
		ok(w, 200, a.custJSON(c), nil)
	case "DELETE":
		if r.URL.Query().Get("confirm") != "true" {
			fail(w, 422, "confirm_required", "confirm=true")
			return
		}
		for k, sk := range a.subkeys {
			if sk.Customer == id {
				delete(a.subkeys, k)
			}
		}
		delete(a.customers, id)
		ok(w, 200, map[string]any{"deleted": true}, nil)
	}
}

func (a *API) keyJSON(k *subkey) map[string]any {
	var cust, rl any
	if k.Customer > 0 {
		cust = k.Customer
	}
	if k.RateLimit > 0 {
		rl = k.RateLimit
	}
	return map[string]any{"id": k.ID, "name": k.Name, "token_prefix": "wck_mock", "customer_id": cust, "scopes": k.Scopes, "ips": []string{}, "rate_limit": rl, "status": "active"}
}

func (a *API) keysRoute(w http.ResponseWriter, m string, owner, customer int64, seg []string, idx int, body map[string]any) {
	if len(seg) == idx && m == "GET" {
		out := []map[string]any{}
		for _, k := range a.subkeys {
			if k.Owner == owner && k.Customer == customer {
				out = append(out, a.keyJSON(k))
			}
		}
		ok(w, 200, out, nil)
		return
	}
	if len(seg) == idx && m == "POST" {
		k := &subkey{ID: a.nextObj, Owner: owner, Customer: customer, Scopes: strs(body["scopes"])}
		k.Name, _ = body["name"].(string)
		if rl, isNum := body["rate_limit"].(float64); isNum {
			k.RateLimit = int64(rl)
		}
		if len(k.Scopes) == 0 {
			k.Scopes = []string{"cloud_read"}
		}
		a.nextObj++
		a.subkeys[k.ID] = k
		tok := fmt.Sprintf("wck_mocktoken%020d", k.ID)
		a.Tokens[tok] = owner
		out := a.keyJSON(k)
		out["token"] = tok
		ok(w, 201, out, nil)
		return
	}
	if len(seg) == idx+1 && m == "DELETE" {
		id, _ := strconv.ParseInt(seg[idx], 10, 64)
		k := a.subkeys[id]
		if k == nil || k.Owner != owner || k.Customer != customer {
			fail(w, 404, "key_not_found", "Key not found.")
			return
		}
		delete(a.subkeys, id)
		ok(w, 200, map[string]any{"deleted": true}, nil)
		return
	}
	fail(w, 404, "endpoint_not_found", "")
}

// Counts is a test helper: live webhooks, customers and sub keys.
func (a *API) Counts() (hooks, customers, keys int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.hooks), len(a.customers), len(a.subkeys)
}

func (a *API) typed(w http.ResponseWriter, m string, owner int64, seg []string, body map[string]any) {
	if owner != 1 {
		fail(w, 404, "service_not_found", "Service not found.")
		return
	}
	t := &a.T
	key := strings.Join(seg, "/")
	switch {
	case seg[0] == "cloud" && seg[1] == "mail" && seg[2] != "2000", seg[0] == "cloud" && seg[1] == "storage" && seg[2] != "3000":
		fail(w, 404, "service_not_found", "Service not found.")
	case key == "cloud/mail/2000/mailboxes" && m == "GET":
		items := []map[string]any{}
		for _, b := range t.mailboxes {
			items = append(items, b)
		}
		ok(w, 200, map[string]any{"items": items}, nil)
	case key == "cloud/mail/2000/mailboxes" && m == "POST":
		lp, _ := body["local_part"].(string)
		if lp == "" || body["password"] == nil {
			fail(w, 422, "tool_action_failed", "local_part and password required")
			return
		}
		q := int64(1024)
		if f, isNum := body["quota"].(float64); isNum {
			q = int64(f)
		}
		name, _ := body["name"].(string)
		t.mailboxes[lp] = map[string]any{"email": lp + "@example.com", "name": name, "quota_mb": q, "used_mb": 0, "active": 1}
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case len(seg) >= 5 && seg[3] == "mailboxes":
		mb := t.mailboxes[seg[4]]
		if mb == nil {
			fail(w, 422, "tool_action_failed", "not yours")
			return
		}
		switch {
		case m == "DELETE":
			if body["confirm"] != true {
				fail(w, 422, "confirm_required", "confirm")
				return
			}
			delete(t.mailboxes, seg[4])
		case m == "PATCH":
			if v, has := body["name"].(string); has {
				mb["name"] = v
			}
			if v, has := body["quota"].(float64); has {
				mb["quota_mb"] = int64(v)
			}
			if v, has := body["active"].(float64); has {
				mb["active"] = int64(v)
			}
		}
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case key == "cloud/mail/2000/aliases" && m == "GET":
		items := []map[string]any{}
		for _, x := range t.aliases {
			items = append(items, x)
		}
		ok(w, 200, map[string]any{"items": items}, nil)
	case key == "cloud/mail/2000/aliases" && m == "POST":
		lp, _ := body["local_part"].(string)
		g, _ := body["goto"].(string)
		t.aliases[t.nextAlias] = map[string]any{"id": t.nextAlias, "address": lp + "@example.com", "goto": strings.Split(g, ","), "active": 1, "kind": "forward"}
		t.nextAlias++
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case len(seg) == 5 && seg[3] == "aliases":
		aid, _ := strconv.ParseInt(seg[4], 10, 64)
		x := t.aliases[aid]
		if x == nil {
			fail(w, 422, "tool_action_failed", "not yours")
			return
		}
		if m == "DELETE" {
			delete(t.aliases, aid)
		} else if g, has := body["goto"].(string); has {
			x["goto"] = strings.Split(g, ",")
		}
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case key == "cloud/storage/3000/buckets" && m == "GET":
		items := []map[string]any{}
		for b := range t.buckets {
			items = append(items, map[string]any{"name": b, "bytes": 0, "objects": 0, "state": "ready", "path_url": "https://s3.example/" + b, "vhost_url": "https://" + b + ".s3.example"})
		}
		ok(w, 200, map[string]any{"items": items}, nil)
	case key == "cloud/storage/3000/buckets" && m == "POST":
		b, _ := body["bucket"].(string)
		t.buckets[b] = true
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case len(seg) == 6 && seg[3] == "buckets" && seg[5] == "website": // GyroAPI 2.6.0
		bk := seg[4]
		if !t.buckets[bk] {
			fail(w, 422, "tool_action_failed", "This bucket does not belong to your service.")
			return
		}
		cur := t.web[bk]
		if cur == nil {
			cur = map[string]any{"enabled": false, "index_document": "index.html", "error_document": "", "spa": false, "url": "https://" + bk + "-abcd.web.example"}
			t.web[bk] = cur
		}
		switch m {
		case "PUT":
			if cur["enabled"] != true && body["accept"] != true {
				fail(w, 422, "tool_action_failed", "accept the rules")
				return
			}
			idx, _ := body["index_document"].(string)
			if idx == "" {
				idx = "index.html"
			}
			if strings.Contains(idx, "/") {
				fail(w, 422, "tool_action_failed", "invalid index document")
				return
			}
			ed, _ := body["error_document"].(string)
			spa, _ := body["spa"].(bool)
			cur["enabled"], cur["index_document"], cur["error_document"], cur["spa"] = true, idx, ed, spa
		case "DELETE":
			if body["confirm"] != true {
				fail(w, 422, "confirm_required", "confirm")
				return
			}
			cur["enabled"] = false
		}
		ok(w, map[bool]int{true: 200, false: 202}[m == "GET"], map[string]any{"status": "accepted", "website": cur}, nil)
	case len(seg) == 6 && seg[3] == "buckets" && seg[5] == "cors":
		bk := seg[4]
		if !t.buckets[bk] {
			fail(w, 422, "tool_action_failed", "This bucket does not belong to your service.")
			return
		}
		switch m {
		case "PUT":
			rl, _ := body["rules"].([]any)
			for _, x := range rl {
				r, _ := x.(map[string]any)
				if o, _ := r["allowed_origins"].([]any); len(o) == 0 {
					fail(w, 422, "tool_action_failed", "Rule: AllowedOrigins is required")
					return
				}
			}
			t.cors[bk] = rl
		case "DELETE":
			if body["confirm"] != true {
				fail(w, 422, "confirm_required", "confirm")
				return
			}
			delete(t.cors, bk)
		}
		rules := t.cors[bk]
		if rules == nil {
			rules = []any{}
		}
		ok(w, map[bool]int{true: 200, false: 202}[m == "GET"], map[string]any{"status": "accepted", "rules": rules}, nil)
	case len(seg) == 5 && seg[3] == "buckets" && m == "DELETE":
		delete(t.buckets, seg[4])
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case key == "cloud/storage/3000/keys" && m == "GET":
		items := []map[string]any{}
		for k, l := range t.skeys {
			items = append(items, map[string]any{"id": k, "label": l})
		}
		ok(w, 200, map[string]any{"items": items}, nil)
	case key == "cloud/storage/3000/keys" && m == "POST":
		l, _ := body["label"].(string)
		k := fmt.Sprintf("GKMOCK%04d", len(t.skeys)+1)
		t.skeys[k] = l
		ok(w, 202, map[string]any{"status": "accepted", "key": map[string]any{"id": k, "secret": strings.Repeat("s", 40), "label": l}}, nil)
	case len(seg) == 5 && seg[3] == "keys" && m == "DELETE":
		delete(t.skeys, seg[4])
		ok(w, 202, map[string]any{"status": "accepted"}, nil)
	case len(seg) == 3 && seg[0] == "domains" && seg[2] == "dns-records":
		switch m {
		case "GET":
			ok(w, 200, t.dns, nil)
		case "POST":
			t.dns = append(t.dns, map[string]any{"identity": fmt.Sprint(len(t.dns) + 1), "type": body["type"], "name": body["name"], "value": body["value"], "ttl": 3600})
			ok(w, 201, map[string]any{"created": true}, nil)
		case "DELETE":
			out := t.dns[:0]
			for _, r := range t.dns {
				if !(r["type"] == body["type"] && r["name"] == body["name"] && r["value"] == body["value"]) {
					out = append(out, r)
				}
			}
			t.dns = out
			ok(w, 200, map[string]any{"deleted": true}, nil)
		}
	default:
		fail(w, 404, "endpoint_not_found", "")
	}
}

func (a *API) appRoute(w http.ResponseWriter, m string, owner, sid int64, body map[string]any) {
	s := a.servers[sid]
	if s == nil || s.Owner != owner {
		fail(w, 404, "server_not_found", "Server not found.")
		return
	}
	if m == "GET" {
		ok(w, 200, map[string]any{"server_id": sid, "install": a.T.app[sid]}, nil)
		return
	}
	if body["confirm"] != true {
		fail(w, 422, "confirm_required", "rebuilds the disk")
		return
	}
	app, _ := body["app"].(string)
	a.T.app[sid] = map[string]any{"id": 1, "app": app, "status": "hazir", "progress": 100, "url": "https://" + app + ".example", "credentials": map[string]any{"user": "admin", "password": "mockpass"}}
	ok(w, 202, map[string]any{"server_id": sid, "status": "accepted", "install_id": 1}, nil)
}

// OrderCount is the number of servers ordered so far (every successful paid order).
func (a *API) OrderCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return int(a.nextSrv - 1000)
}

// ---- security groups (GyroAPI 2.9.0)
func (a *API) securityGroups(w http.ResponseWriter, m string, seg []string, owner int64, body map[string]any, id func(int) int64) {
	if a.sgs == nil {
		a.sgs, a.sgOwner, a.sgBind = map[string]map[string]any{}, map[string]int64{}, map[int64][]string{}
	}
	rules := func(in any) ([]any, bool) {
		rl, _ := in.([]any)
		out := []any{}
		for i, x := range rl {
			r, _ := x.(map[string]any)
			t, _ := r["type"].(string)
			ports := map[string]string{"ssh": "22", "http": "80", "https": "443", "rdp": "3389", "mysql": "3306", "all": "all", "all_tcp": "all", "all_udp": "all", "all_icmp": "all"}
			port, known := ports[t]
			if t == "custom_tcp" || t == "custom_udp" {
				port, _ = r["port"].(string)
				known = port != ""
			}
			if !known {
				return nil, false
			}
			src := []any{}
			sl, _ := r["source"].([]any)
			for _, s := range sl {
				v, _ := s.(string)
				if !strings.Contains(v, "/") {
					v += "/32"
				}
				src = append(src, v)
			}
			if len(src) == 0 {
				src = []any{"0.0.0.0/0"}
			}
			dir, _ := r["direction"].(string)
			if dir == "" {
				dir = "in"
			}
			d, _ := r["description"].(string)
			out = append(out, map[string]any{"id": fmt.Sprintf("gfr-%08x", i+1), "direction": dir, "type": t, "port": port, "source": src, "description": d})
		}
		return out, true
	}
	servers := func(g string) []any {
		o := []any{}
		for sid, l := range a.sgBind {
			for _, x := range l {
				if x == g {
					o = append(o, sid)
				}
			}
		}
		return o
	}
	view := func(g string) map[string]any { v := a.sgs[g]; v["servers"] = servers(g); return v }
	if seg[1] == "servers" {
		sv := a.servers[id(2)]
		if sv == nil || sv.Owner != owner {
			fail(w, 404, "server_not_found", "Server not found.")
			return
		}
		cur := a.sgBind[sv.ID]
		switch {
		case len(seg) == 4 && m == "PUT":
			l, _ := body["security_groups"].([]any)
			nw := []string{}
			for _, x := range l {
				g, _ := x.(string)
				if a.sgOwner[g] != owner {
					fail(w, 404, "security_group_not_found", "Security group not found.")
					return
				}
				nw = append(nw, g)
			}
			if len(nw) > 5 {
				fail(w, 422, "invalid_security_groups", "max 5")
				return
			}
			cur = nw
		case len(seg) == 5 && m == "POST":
			if a.sgOwner[seg[4]] != owner {
				fail(w, 404, "security_group_not_found", "Security group not found.")
				return
			}
			cur = append(cur, seg[4])
		case len(seg) == 5 && m == "DELETE":
			n := []string{}
			for _, x := range cur {
				if x != seg[4] {
					n = append(n, x)
				}
			}
			cur = n
		}
		a.sgBind[sv.ID] = cur
		l := []any{}
		for _, g := range cur {
			l = append(l, map[string]any{"id": g, "name": a.sgs[g]["name"]})
		}
		ok(w, 200, map[string]any{"server_id": sv.ID, "max": 5, "rule_allowance": 10, "security_groups": l, "trial": false}, nil)
		return
	}
	if len(seg) == 2 {
		if m == "POST" {
			name, _ := body["name"].(string)
			for g, o := range a.sgOwner {
				if o == owner && strings.EqualFold(a.sgs[g]["name"].(string), name) {
					fail(w, 422, "err_sg_dup", "duplicate name")
					return
				}
			}
			rl := []any{map[string]any{"id": "gfr-00000000", "direction": "out", "type": "all", "port": "all", "source": []any{"0.0.0.0/0"}, "description": ""}}
			if body["rules"] != nil {
				var good bool
				if rl, good = rules(body["rules"]); !good {
					fail(w, 422, "err_sg_type", "bad rule")
					return
				}
			}
			a.nextSg++
			g := fmt.Sprintf("sg-%08x", a.nextSg)
			d, _ := body["description"].(string)
			a.sgs[g], a.sgOwner[g] = map[string]any{"id": g, "name": name, "description": d, "status": "unused", "trial_seconds_left": 0, "rules": rl}, owner
			ok(w, 201, view(g), nil)
			return
		}
		l := []any{}
		for g, o := range a.sgOwner {
			if o == owner {
				l = append(l, view(g))
			}
		}
		ok(w, 200, l, nil)
		return
	}
	g := seg[2]
	if a.sgOwner[g] != owner {
		fail(w, 404, "security_group_not_found", "Security group not found.")
		return
	}
	switch {
	case len(seg) == 4 && seg[3] == "rules" && m == "PUT":
		rl, good := rules(body["rules"])
		if !good {
			fail(w, 422, "err_sg_type", "bad rule")
			return
		}
		a.sgs[g]["rules"], a.sgs[g]["status"] = rl, "active"
		v := view(g)
		v["trial"] = false
		ok(w, 200, v, nil)
		return
	case len(seg) == 3 && m == "PATCH":
		if v, has := body["name"].(string); has {
			a.sgs[g]["name"] = v
		}
		if v, has := body["description"].(string); has {
			a.sgs[g]["description"] = v
		}
	case len(seg) == 3 && m == "DELETE":
		if len(servers(g)) > 0 {
			fail(w, 422, "err_sg_attached", "attached")
			return
		}
		delete(a.sgs, g)
		delete(a.sgOwner, g)
		ok(w, 200, map[string]any{"deleted": true, "id": g}, nil)
		return
	}
	ok(w, 200, view(g), nil)
}
