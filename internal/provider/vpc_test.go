package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// In-memory VPC API (Gyro Public API 2.29.0, V1B-SOZLESME.md) wrapped around the mock API.
type vpcMockSubnet struct {
	ID             int64
	VPC            int64
	Name, CIDR, GW string
}

type vpcMockNIC struct {
	ID, Server, Subnet int64
	IP, State, Err     string
	polls              int
}

type vpcMock struct {
	mu      sync.Mutex
	base    http.Handler
	next    int64
	vpcs    map[int64]map[string]any // id -> name,cidr,project,gw fields
	subnets map[int64]*vpcMockSubnet
	pfs     map[int64]map[string]any // id -> vpc, protocol, ...
	nics    map[int64]*vpcMockNIC
	// counters
	patches, gwPuts, deletes, nicDeletes, nicAttachPolls, pfPatches int
	gwOffBeforeDelete                                               bool
	failNIC                                                         bool  // next attach ends in state error
	foreignNIC                                                      int64 // vpc id that gets a server card Terraform does not manage
	hiddenInUse                                                     int64 // vpc id whose DELETE answers 409 although GET shows it empty
	log                                                             []string
}

func newVPCMock(base http.Handler) *vpcMock {
	return &vpcMock{base: base, vpcs: map[int64]map[string]any{}, subnets: map[int64]*vpcMockSubnet{}, pfs: map[int64]map[string]any{}, nics: map[int64]*vpcMockNIC{}}
}

var (
	reVPC  = regexp.MustCompile(`/cloud/vpcs(?:/(\d+)(?:/(subnets|gateway|port-forwards)(?:/(\d+))?)?)?$`)
	reNICs = regexp.MustCompile(`/cloud/servers/(\d+)/nics(?:/(\d+))?$`)
)

func (x *vpcMock) id() int64 { x.next++; return 100 + x.next }

func jsonOut(w http.ResponseWriter, st int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(st)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func jsonErr(w http.ResponseWriter, st int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(st)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (x *vpcMock) subnetView(s *vpcMockSubnet) map[string]any {
	used := 0
	for _, n := range x.nics {
		if n.Subnet == s.ID {
			used++
		}
	}
	return map[string]any{"id": s.ID, "vpc_id": s.VPC, "name": s.Name, "cidr": s.CIDR, "gateway_ip": s.GW, "used": used, "capacity": 249, "state": "ready", "created_at": "2026-11-15T00:00:00Z"}
}

func (x *vpcMock) pfList(vpc int64) []map[string]any {
	out := []map[string]any{}
	for i := int64(0); i <= x.next+100; i++ {
		if p, ok := x.pfs[i]; ok && p["vpc"] == vpc {
			src, _ := p["source_cidrs"].([]any)
			if src == nil {
				src = []any{}
			}
			out = append(out, map[string]any{"id": i, "protocol": p["protocol"], "public_port": p["public_port"], "target_ip": p["target_ip"], "target_port": p["target_port"], "source_cidrs": src})
		}
	}
	return out
}

func (x *vpcMock) gateway(id int64) map[string]any {
	v := x.vpcs[id]
	ip := ""
	st := "off"
	if v["enabled"].(bool) {
		ip, st = "31.58.248.230", "active"
	}
	return map[string]any{"enabled": v["enabled"], "nat": v["nat"], "inter_subnet": v["inter"], "public_ip": ip, "status": st,
		"port_forwards": x.pfList(id), "monthly_eur": 4.99, "hourly_eur": 0.00743}
}

func (x *vpcMock) view(id int64) map[string]any {
	v := x.vpcs[id]
	subs := []map[string]any{}
	servers := []map[string]any{}
	for i := int64(0); i <= x.next+100; i++ {
		if s, ok := x.subnets[i]; ok && s.VPC == id {
			subs = append(subs, x.subnetView(s))
		}
	}
	for i := int64(0); i <= x.next+100; i++ {
		if n, ok := x.nics[i]; ok && x.subnets[n.Subnet] != nil && x.subnets[n.Subnet].VPC == id {
			servers = append(servers, map[string]any{"server_id": n.Server, "name": "srv", "nic_id": n.ID, "subnet_id": n.Subnet, "ip": n.IP, "state": n.State})
		}
	}
	if x.foreignNIC == id {
		servers = append(servers, map[string]any{"server_id": 999, "name": "other", "nic_id": 9999})
	}
	return map[string]any{"id": id, "name": v["name"], "cidr": v["cidr"], "region": "tr-ist-1", "project_id": v["project"], "state": "ready",
		"subnets": subs, "servers": servers, "gateway": x.gateway(id), "created_at": "2026-11-15T00:00:00Z"}
}

func (x *vpcMock) nicView(n *vpcMockNIC) map[string]any {
	s := x.subnets[n.Subnet]
	v := map[string]any{"id": n.ID, "vpc_id": s.VPC, "subnet_id": s.ID, "subnet_name": s.Name, "subnet_cidr": s.CIDR, "ip": n.IP, "gateway_ip": s.GW,
		"mac": fmt.Sprintf("00:50:56:aa:00:%02x", n.ID%256), "state": n.State}
	if n.Err != "" {
		v["error"] = n.Err
	}
	return v
}

func (x *vpcMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if mt := reVPC.FindStringSubmatch(r.URL.Path); mt != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.vpcHandler(w, r, mt)
		return
	}
	if mt := reNICs.FindStringSubmatch(r.URL.Path); mt != nil {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.nicHandler(w, r, mt)
		return
	}
	x.base.ServeHTTP(w, r)
}

func (x *vpcMock) vpcHandler(w http.ResponseWriter, r *http.Request, mt []string) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	x.log = append(x.log, r.Method+" "+strings.TrimPrefix(mt[0], "/"))
	id, _ := strconv.ParseInt(mt[1], 10, 64)
	sub, oid := mt[2], mt[3]
	if id > 0 && x.vpcs[id] == nil {
		jsonErr(w, 404, "not_found", "VPC not found")
		return
	}
	switch {
	case id == 0 && r.Method == http.MethodGet:
		list := []map[string]any{}
		for i := int64(0); i <= x.next+100; i++ {
			if _, ok := x.vpcs[i]; ok {
				list = append(list, x.view(i))
			}
		}
		jsonOut(w, 200, map[string]any{"vpcs": list, "count": len(list)})
	case id == 0 && r.Method == http.MethodPost:
		cidr, _ := body["cidr"].(string)
		if strings.HasPrefix(cidr, "10.200.") {
			jsonErr(w, 422, "reserved_range", "reserved")
			return
		}
		nid := x.id()
		pr := int64(0)
		if p, ok := body["project_id"].(float64); ok {
			pr = int64(p)
		}
		x.vpcs[nid] = map[string]any{"name": body["name"], "cidr": cidr, "project": pr, "enabled": false, "nat": true, "inter": "allow"}
		base := strings.Split(cidr, "/")[0]
		oct := strings.Split(base, ".")
		sid := x.id()
		x.subnets[sid] = &vpcMockSubnet{ID: sid, VPC: nid, Name: "default", CIDR: oct[0] + "." + oct[1] + ".0.0/24", GW: oct[0] + "." + oct[1] + ".0.1"}
		jsonOut(w, 201, x.view(nid))
	case sub == "" && r.Method == http.MethodGet:
		jsonOut(w, 200, x.view(id))
	case sub == "" && r.Method == http.MethodPatch:
		x.patches++
		x.vpcs[id]["name"] = body["name"]
		jsonOut(w, 200, x.view(id))
	case sub == "" && r.Method == http.MethodDelete:
		if r.URL.Query().Get("confirm") != "true" && body["confirm"] != true {
			jsonErr(w, 422, "confirm_required", "confirm")
			return
		}
		if len(x.view(id)["servers"].([]map[string]any)) > 0 || len(x.pfList(id)) > 0 || x.hiddenInUse == id {
			jsonErr(w, 409, "vpc_in_use", "VPC has attached network cards or port forwards")
			return
		}
		x.gwOffBeforeDelete = x.vpcs[id]["enabled"] == false
		for k, s := range x.subnets {
			if s.VPC == id {
				delete(x.subnets, k)
			}
		}
		delete(x.vpcs, id)
		x.deletes++
		jsonOut(w, 200, map[string]any{"id": id, "deleted": true})
	case sub == "gateway" && r.Method == http.MethodGet:
		jsonOut(w, 200, x.gateway(id))
	case sub == "gateway" && r.Method == http.MethodPut:
		x.gwPuts++
		if _, has := body["confirm"]; has {
			jsonErr(w, 422, "unexpected_field", "confirm is not accepted here")
			return
		}
		if e, ok := body["enabled"].(bool); ok {
			if !e && len(x.pfList(id)) > 0 {
				jsonErr(w, 409, "gateway_in_use", "port forwards")
				return
			}
			x.vpcs[id]["enabled"] = e
		}
		if n, ok := body["nat"].(bool); ok {
			x.vpcs[id]["nat"] = n
		}
		if s, ok := body["inter_subnet"].(string); ok {
			x.vpcs[id]["inter"] = s
		}
		jsonOut(w, 200, x.gateway(id))
	case sub == "subnets" && oid == "" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for i := int64(0); i <= x.next+100; i++ {
			if s, ok := x.subnets[i]; ok && s.VPC == id {
				out = append(out, x.subnetView(s))
			}
		}
		jsonOut(w, 200, map[string]any{"subnets": out, "count": len(out)})
	case sub == "subnets" && oid == "" && r.Method == http.MethodPost:
		cidr, _ := body["cidr"].(string)
		if cidr == "" {
			p := 24.0
			if v, ok := body["prefix"].(float64); ok {
				p = v
			}
			oct := strings.Split(strings.Split(x.vpcs[id]["cidr"].(string), "/")[0], ".")
			cidr = fmt.Sprintf("%s.%s.%d.0/%d", oct[0], oct[1], 50+x.next, int(p))
		}
		for _, s := range x.subnets {
			if s.VPC == id && s.CIDR == cidr {
				jsonErr(w, 422, "cidr_overlap", "overlap")
				return
			}
		}
		sid := x.id()
		gw := strings.TrimSuffix(strings.Split(cidr, "/")[0], ".0") + ".1"
		x.subnets[sid] = &vpcMockSubnet{ID: sid, VPC: id, Name: body["name"].(string), CIDR: cidr, GW: gw}
		jsonOut(w, 201, x.subnetView(x.subnets[sid]))
	case sub == "subnets" && oid != "" && r.Method == http.MethodDelete:
		sid, _ := strconv.ParseInt(oid, 10, 64)
		s := x.subnets[sid]
		if s == nil || s.VPC != id {
			jsonErr(w, 404, "not_found", "subnet")
			return
		}
		for _, n := range x.nics {
			if n.Subnet == sid {
				jsonErr(w, 409, "subnet_in_use", "in use")
				return
			}
		}
		delete(x.subnets, sid)
		jsonOut(w, 200, map[string]any{"id": sid, "deleted": true})
	case sub == "port-forwards" && oid == "" && r.Method == http.MethodGet:
		jsonOut(w, 200, map[string]any{"port_forwards": x.pfList(id), "count": len(x.pfList(id))})
	case sub == "port-forwards" && oid == "" && r.Method == http.MethodPost:
		if x.vpcs[id]["enabled"] != true {
			jsonErr(w, 409, "gateway_disabled", "gateway is off")
			return
		}
		pid := x.id()
		src, _ := body["source_cidrs"].([]any)
		if src == nil {
			src = []any{}
		}
		x.pfs[pid] = map[string]any{"vpc": id, "protocol": body["protocol"], "public_port": body["public_port"], "target_ip": body["target_ip"], "target_port": body["target_port"], "source_cidrs": src}
		jsonOut(w, 201, map[string]any{"id": pid, "protocol": body["protocol"], "public_port": body["public_port"], "target_ip": body["target_ip"], "target_port": body["target_port"], "source_cidrs": src})
	case sub == "port-forwards" && oid != "" && r.Method == http.MethodPatch:
		pid, _ := strconv.ParseInt(oid, 10, 64)
		p := x.pfs[pid]
		if p == nil || p["vpc"] != id {
			jsonErr(w, 404, "not_found", "pf")
			return
		}
		src, ok := body["source_cidrs"].([]any)
		if !ok {
			jsonErr(w, 422, "invalid_source", "source_cidrs missing")
			return
		}
		p["source_cidrs"] = src
		x.pfPatches++
		for _, v := range x.pfList(id) {
			if v["id"] == pid {
				jsonOut(w, 200, v)
			}
		}
	case sub == "port-forwards" && oid != "" && r.Method == http.MethodDelete:
		pid, _ := strconv.ParseInt(oid, 10, 64)
		if p := x.pfs[pid]; p == nil || p["vpc"] != id {
			jsonErr(w, 404, "not_found", "pf")
			return
		}
		delete(x.pfs, pid)
		jsonOut(w, 200, map[string]any{"id": pid, "deleted": true})
	default:
		jsonErr(w, 405, "method_not_allowed", r.Method)
	}
}

func (x *vpcMock) nicHandler(w http.ResponseWriter, r *http.Request, mt []string) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	srv, _ := strconv.ParseInt(mt[1], 10, 64)
	nid, _ := strconv.ParseInt(mt[2], 10, 64)
	if srv == 404 {
		jsonErr(w, 404, "not_found", "server")
		return
	}
	switch {
	case nid == 0 && r.Method == http.MethodGet:
		out := []map[string]any{}
		for i := int64(0); i <= x.next+100; i++ {
			n, ok := x.nics[i]
			if !ok || n.Server != srv {
				continue
			}
			// state machine: attaching -> (1 poll) -> ready|error ; detaching -> (1 poll) -> gone
			switch n.State {
			case "attaching":
				x.nicAttachPolls++
				if n.polls++; n.polls >= 1 {
					if x.failNIC {
						n.State, n.Err = "error", "guest agent did not answer"
					} else {
						n.State = "ready"
					}
				}
				out = append(out, x.nicView(n))
			case "detaching":
				if n.polls++; n.polls >= 2 {
					delete(x.nics, i)
					continue
				}
				out = append(out, x.nicView(n))
			default:
				out = append(out, x.nicView(n))
			}
		}
		jsonOut(w, 200, map[string]any{"nics": out, "count": len(out), "max_nics": 3, "public_ip": "31.58.248.200", "public_enabled": true, "applied": true})
	case nid == 0 && r.Method == http.MethodPost:
		var sid int64
		switch v := body["subnet_id"].(type) {
		case float64:
			sid = int64(v)
		case string:
			sid, _ = strconv.ParseInt(v, 10, 64)
		}
		s := x.subnets[sid]
		if s == nil {
			jsonErr(w, 404, "not_found", "subnet")
			return
		}
		ip, _ := body["ip"].(string)
		if ip == "" {
			used := 0
			for _, n := range x.nics {
				if n.Subnet == sid {
					used++
				}
			}
			ip = strings.TrimSuffix(s.GW, ".1") + "." + strconv.Itoa(5+used)
		}
		id := x.id()
		x.nics[id] = &vpcMockNIC{ID: id, Server: srv, Subnet: sid, IP: ip, State: "attaching"}
		jsonOut(w, 202, map[string]any{"nic": x.nicView(x.nics[id]), "task_id": 77})
	case nid > 0 && r.Method == http.MethodDelete:
		n := x.nics[nid]
		if n == nil || n.Server != srv {
			jsonErr(w, 404, "not_found", "nic")
			return
		}
		x.nicDeletes++
		if n.State == "error" {
			delete(x.nics, nid)
		} else {
			n.State, n.polls = "detaching", 0
		}
		jsonOut(w, 202, map[string]any{"id": nid, "state": "detaching", "task_id": 78})
	default:
		jsonErr(w, 405, "method_not_allowed", r.Method)
	}
}

func vpcProv(t *testing.T) (*vpcMock, string) {
	m := mockapi.New()
	x := newVPCMock(m)
	srv := httptest.NewServer(x)
	t.Cleanup(srv.Close)
	return x, fmt.Sprintf(`provider "gyro" {
  endpoint = %q
  token    = "wck_testA"
  poll_interval_seconds = 1
  poll_timeout_minutes  = 1
}
`, srv.URL+"/api/v1/client")
}

func vpcCfg(prov, name, inter string, gw, full bool) string {
	s := prov + fmt.Sprintf(`
resource "gyro_vpc" "main" {
  name            = %q
  cidr            = "10.10.0.0/16"
  gateway_enabled = %t
  inter_subnet    = %q
}
`, name, gw, inter)
	if full {
		s += `
resource "gyro_subnet" "app" {
  vpc_id = gyro_vpc.main.id
  name   = "app"
  cidr   = "10.10.1.0/24"
}

resource "gyro_subnet" "auto" {
  vpc_id = gyro_vpc.main.id
  name   = "auto"
  prefix = 26
}

resource "gyro_server_nic" "web" {
  server_id = 123
  subnet_id = gyro_subnet.app.subnet_id
}

resource "gyro_server_nic" "db" {
  server_id = 124
  subnet_id = gyro_vpc.main.default_subnet_id
  ip        = "10.10.0.20"
}

resource "gyro_port_forward" "ssh" {
  vpc_id      = gyro_vpc.main.id
  protocol    = "tcp"
  public_port = 2222
  target_ip   = gyro_server_nic.web.ip
  target_port = 22
}

data "gyro_vpc" "by_name" {
  name       = gyro_vpc.main.name
  depends_on = [gyro_subnet.app, gyro_subnet.auto]
}

data "gyro_vpc" "by_id" {
  id         = gyro_vpc.main.id
  depends_on = [gyro_port_forward.ssh]
}
`
	}
	return s
}

func TestAccMockVPCLifecycle(t *testing.T) {
	x, prov := vpcProv(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		CheckDestroy: func(*terraform.State) error {
			if len(x.vpcs) != 0 || len(x.nics) != 0 || len(x.pfs) != 0 {
				return fmt.Errorf("left over: vpcs=%d nics=%d pfs=%d", len(x.vpcs), len(x.nics), len(x.pfs))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: vpcCfg(prov, "uretim", "allow", true, true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_vpc.main", "region", "tr-ist-1"),
				resource.TestCheckResourceAttr("gyro_vpc.main", "gateway_enabled", "true"),
				resource.TestCheckResourceAttr("gyro_vpc.main", "gateway_public_ip", "31.58.248.230"),
				resource.TestCheckResourceAttr("gyro_vpc.main", "project_id", "0"),
				resource.TestCheckResourceAttrSet("gyro_vpc.main", "default_subnet_id"),
				resource.TestCheckResourceAttr("gyro_subnet.app", "gateway_ip", "10.10.1.1"),
				resource.TestCheckResourceAttr("gyro_subnet.auto", "prefix", "26"),
				resource.TestMatchResourceAttr("gyro_subnet.auto", "cidr", regexp.MustCompile(`^10\.10\.\d+\.0/26$`)),
				resource.TestCheckResourceAttr("gyro_server_nic.web", "state", "ready"),
				resource.TestCheckResourceAttr("gyro_server_nic.web", "ip", "10.10.1.5"),
				resource.TestCheckResourceAttr("gyro_server_nic.db", "ip", "10.10.0.20"),
				resource.TestCheckResourceAttrPair("gyro_server_nic.web", "vpc_id", "gyro_vpc.main", "id"),
				resource.TestCheckResourceAttrSet("gyro_server_nic.web", "mac"),
				resource.TestCheckResourceAttr("gyro_port_forward.ssh", "target_ip", "10.10.1.5"),
				resource.TestCheckResourceAttrPair("data.gyro_vpc.by_name", "id", "gyro_vpc.main", "id"),
				resource.TestCheckResourceAttr("data.gyro_vpc.by_name", "subnets.#", "3"),
				resource.TestCheckResourceAttr("data.gyro_vpc.by_id", "name", "uretim"),
				resource.TestCheckResourceAttr("data.gyro_vpc.by_id", "gateway_status", "active"),
				func(*terraform.State) error {
					if x.nicAttachPolls < 2 {
						return fmt.Errorf("attach was not awaited (polls %d)", x.nicAttachPolls)
					}
					return nil
				},
			)},
			{ResourceName: "gyro_vpc.main", ImportState: true, ImportStateVerify: true},
			{ResourceName: "gyro_subnet.app", ImportState: true, ImportStateVerify: true},
			{ResourceName: "gyro_subnet.auto", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"prefix"}},
			{ResourceName: "gyro_server_nic.web", ImportState: true, ImportStateVerify: true},
			{ResourceName: "gyro_port_forward.ssh", ImportState: true, ImportStateVerify: true},
			{Config: vpcCfg(prov, "gyro-proje", "deny", true, true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_vpc.main", "name", "gyro-proje"),
				resource.TestCheckResourceAttr("gyro_vpc.main", "inter_subnet", "deny"),
				func(*terraform.State) error {
					if x.patches != 1 || x.gwPuts != 2 { // 1 at create (gateway on) + 1 inter_subnet
						return fmt.Errorf("patches=%d gwPuts=%d", x.patches, x.gwPuts)
					}
					return nil
				},
			)},
			// remove cards, forwards and subnets, turn the gateway off
			{Config: vpcCfg(prov, "gyro-proje", "deny", false, false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_vpc.main", "gateway_enabled", "false"),
				resource.TestCheckResourceAttr("gyro_vpc.main", "gateway_public_ip", ""),
				func(*terraform.State) error {
					if x.nicDeletes != 2 || len(x.nics) != 0 {
						return fmt.Errorf("nic deletes=%d left=%d (detach must be awaited)", x.nicDeletes, len(x.nics))
					}
					return nil
				},
			)},
			// gateway on again: destroy must turn it off before DELETE
			{Config: vpcCfg(prov, "gyro-proje", "deny", true, false)},
			{Config: prov, Check: func(*terraform.State) error {
				if x.deletes != 1 || !x.gwOffBeforeDelete {
					return fmt.Errorf("deletes=%d gateway off before delete=%t", x.deletes, x.gwOffBeforeDelete)
				}
				return nil
			}},
		},
	})
}

// Objects deleted outside Terraform drop out of state and are created again.
func TestAccMockVPCGone(t *testing.T) {
	x, prov := vpcProv(t)
	cfg := prov + `
resource "gyro_vpc" "main" {
  name = "uretim"
  cidr = "10.10.0.0/16"
}
resource "gyro_server_nic" "web" {
  server_id = 123
  subnet_id = gyro_vpc.main.default_subnet_id
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: func() {
				x.mu.Lock()
				for k := range x.nics {
					delete(x.nics, k)
				}
				x.mu.Unlock()
			}, Config: cfg, Check: func(*terraform.State) error {
				if len(x.nics) != 1 {
					return fmt.Errorf("card not re-created: %d", len(x.nics))
				}
				return nil
			}},
			{PreConfig: func() {
				x.mu.Lock()
				x.nics, x.vpcs, x.subnets = map[int64]*vpcMockNIC{}, map[int64]map[string]any{}, map[int64]*vpcMockSubnet{}
				x.mu.Unlock()
			}, Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("gyro_server_nic.web", "state", "ready"),
				func(*terraform.State) error {
					if len(x.vpcs) != 1 || len(x.nics) != 1 {
						return fmt.Errorf("not re-created: vpcs=%d nics=%d", len(x.vpcs), len(x.nics))
					}
					return nil
				},
			)},
		},
	})
}

// A VPC still in use is not deleted (and its gateway is not touched); a card that ends in state error fails and is cleaned up.
func TestAccMockVPCErrors(t *testing.T) {
	x, prov := vpcProv(t)
	vpc := prov + `
resource "gyro_vpc" "main" {
  name            = "uretim"
  cidr            = "10.10.0.0/16"
  gateway_enabled = true
}
`
	nic := vpc + `
resource "gyro_server_nic" "web" {
  server_id = 123
  subnet_id = gyro_vpc.main.default_subnet_id
}
`
	var vid int64
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: prov + `
resource "gyro_vpc" "bad" {
  name = "x"
  cidr = "10.10.0.1/16"
}
`, ExpectError: regexp.MustCompile(`host\s+bits\s+set`)},
			{Config: prov + `
resource "gyro_vpc" "bad" {
  name = "x"
  cidr = "10.200.0.0/16"
}
`, ExpectError: regexp.MustCompile(`reserved_range`)},
			{Config: vpc, Check: func(s *terraform.State) error {
				vid, _ = strconv.ParseInt(s.RootModule().Resources["gyro_vpc.main"].Primary.ID, 10, 64)
				return nil
			}},
			{PreConfig: func() { x.mu.Lock(); x.failNIC = true; x.mu.Unlock() }, Config: nic,
				ExpectError: regexp.MustCompile(`(?s)ended\s+in\s+state\s+error:\s+guest\s+agent\s+did\s+not\s+answer`)},
			{PreConfig: func() { x.mu.Lock(); x.failNIC = false; x.foreignNIC = vid; x.mu.Unlock() }, Config: prov,
				ExpectError: regexp.MustCompile(`(?s)Remove\s+the\s+server\s+network\s+cards\s+\(gyro_server_nic\).*port\s+forwards`)},
			{PreConfig: func() {
				x.mu.Lock()
				if x.vpcs[vid]["enabled"] != true {
					t.Errorf("the in-use check must run before the gateway is turned off")
				}
				x.foreignNIC = 0
				x.hiddenInUse = vid
				x.mu.Unlock()
			}, Config: prov,
				ExpectError: regexp.MustCompile(`(?s)vpc_in_use.*remove\s+them\s+first`)},
			{PreConfig: func() { x.mu.Lock(); x.hiddenInUse = 0; x.mu.Unlock() }, Config: prov, Check: func(*terraform.State) error {
				if len(x.vpcs) != 0 || len(x.nics) != 0 {
					return fmt.Errorf("left over: vpcs=%d nics=%d", len(x.vpcs), len(x.nics))
				}
				if x.gwPuts < 1 {
					return fmt.Errorf("gateway was never set")
				}
				return nil
			}},
		},
	})
	// both the refused (409) and the final DELETE reached the API
	x.mu.Lock()
	defer x.mu.Unlock()
	off := 0
	for _, l := range x.log {
		if strings.HasPrefix(l, "DELETE cloud/vpcs/") {
			off++
		}
	}
	if off < 2 {
		t.Fatalf("expected the refused and the final DELETE, log: %v", x.log)
	}
}
