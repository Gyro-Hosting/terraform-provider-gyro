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
	ID         int64
	Owner      int64
	ProductID  int64
	Type, Loc  string
	Image      string
	Hostname   string
	Status     string // inprocess -> active, cancelled
	Power      string
	Polls      int // GETs left until provisioning completes
	Cancel     string
	AutoRenew  bool
	Cycle      string
	Snapshots  []map[string]any
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

// API is safe for concurrent use.
type API struct {
	mu       sync.Mutex
	Tokens   map[string]int64   // token -> owner
	Balance  map[int64]float64  // owner -> TRY
	servers  map[int64]*server
	keys     map[int64]*key
	idem     map[string]idem
	nextSrv  int64
	nextKey  int64
	Calls    []string
	// ProvisionPolls is how many GETs a new server stays "inprocess" (default 1).
	ProvisionPolls int
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
	}
}

var products = []map[string]any{
	{"id": 723, "name": "Gyro Bulut Eko 2-4", "server_type": "cx23", "location": "fsn1", "cores": 2, "memory_gb": 4, "disk_gb": 40, "cpu_type": "shared", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": true,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 553.49, "setup": 0, "currency": "TRY"}}},
	{"id": 656, "name": "Gyro Bulut Pro 2-8", "server_type": "ccx13", "location": "fsn1", "cores": 2, "memory_gb": 8, "disk_gb": 80, "cpu_type": "dedicated", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": true,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 4015.99, "setup": 0, "currency": "TRY"}}},
	{"id": 687, "name": "Gyro Bulut Standart 1-1", "server_type": "cpx02", "location": "hel1", "cores": 1, "memory_gb": 1, "disk_gb": 20, "cpu_type": "shared", "architecture": "x86", "traffic_tb": 20, "ipv4": true, "ipv6": true, "available": false,
		"prices": []map[string]any{{"cycle": "monthly", "amount": 599.49, "setup": 0, "currency": "TRY"}}},
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
		"server_type": s.Type, "location": s.Loc, "ipv4": nil, "ipv6": nil, "provisioned": s.Status == "active" || s.Status == "cancelled",
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
				"location": s.Loc, "traffic": map[string]any{"outgoing_bytes": 0, "included_bytes": 21990232555520}}
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
	case m == "GET" && path == "whoami":
		ok(w, 200, map[string]any{"id": 1, "type": "client", "owner_id": owner, "permissions": []string{"Module:Addons/GyroAPI/*"}}, nil)
	case m == "GET" && path == "cloud/products":
		var out []map[string]any
		for _, p := range products {
			if l := r.URL.Query().Get("location"); l != "" && p["location"] != l {
				continue
			}
			if t := r.URL.Query().Get("server_type"); t != "" && p["server_type"] != t {
				continue
			}
			out = append(out, p)
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0})
	case m == "GET" && path == "cloud/locations":
		ok(w, 200, []map[string]any{{"code": "fsn1", "city": "Falkenstein", "country": "DE", "zone": "eu-central", "products": 2}, {"code": "hel1", "city": "Helsinki", "country": "FI", "zone": "eu-central", "products": 1}}, nil)
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
	for _, p := range products {
		if (pid > 0 && int64(p["id"].(int)) == pid) || (pid == 0 && p["server_type"] == body["server_type"] && p["location"] == body["location"]) {
			prod = p
		}
	}
	if prod == nil {
		fail(w, 422, "product_not_orderable", "product_id (or server_type + location) must name an orderable product.")
		return
	}
	if !prod["available"].(bool) {
		fail(w, 422, "out_of_stock", "This product is out of stock.")
		return
	}
	img, _ := body["image"].(string)
	if img != "debian-12" && img != "ubuntu-24.04" {
		fail(w, 422, "invalid_image", "image is not offered on this product.")
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
