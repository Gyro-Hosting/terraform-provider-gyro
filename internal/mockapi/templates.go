package mockapi

import (
	"fmt"
	"net/http"
	"strings"
)

// Custom templates (API 2.15.0): tr-ist-1 servers only, owner-scoped (cross-tenant 404), created "creating" and ready on the next GET.
type tpl struct {
	ID, Owner, Server int64
	Name, Desc, Mode  string
	Status            string
	Polls             int
}

type tplStore struct {
	m    map[int64]*tpl
	next int64
}

var tplStores = map[*API]*tplStore{} // guarded by the API mutex (ServeHTTP holds it)

func (a *API) tpls() *tplStore {
	if tplStores[a] == nil {
		tplStores[a] = &tplStore{m: map[int64]*tpl{}, next: 1}
	}
	return tplStores[a]
}

// TemplateCount: live templates (tests).
func (a *API) TemplateCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, t := range a.tpls().m {
		if t.Status != "deleted" {
			n++
		}
	}
	return n
}

func tplJSON(t *tpl) map[string]any {
	return map[string]any{"id": t.ID, "image": fmt.Sprintf("custom:%d", t.ID), "name": t.Name, "description": t.Desc, "status": t.Status, "size_gb": 2.44,
		"size_bytes": 2619355210, "min_disk_gb": 35, "base_image": "ubuntu-24.04", "base_image_name": "Ubuntu 24.04 LTS", "source_server_id": t.Server,
		"region": "tr-ist-1", "mode": map[bool]string{true: "snapshot", false: t.Mode}[t.Mode == ""], "warnings": []string{}, "created_at": "2026-11-12T15:15:54+03:00", "ready_at": nil}
}

// templates handles /cloud/templates*, /cloud/servers/{id}/templates. Returns false when the path is not a template route.
func (a *API) templates(w http.ResponseWriter, r *http.Request, m string, seg []string, owner int64, body map[string]any, id func(int) int64) bool {
	st := a.tpls()
	if len(seg) == 4 && seg[1] == "servers" && seg[3] == "templates" && m == "POST" {
		sv := a.servers[id(2)]
		if sv == nil || sv.Owner != owner {
			fail(w, 404, "server_not_found", "Server not found.")
			return true
		}
		if sv.Reg != "tr-ist-1" {
			fail(w, 422, "region_not_supported", "Templates are available for servers in region tr-ist-1 only.")
			return true
		}
		name, _ := body["name"].(string)
		desc, _ := body["description"].(string)
		mode, _ := body["mode"].(string)
		t := &tpl{ID: st.next, Owner: owner, Server: sv.ID, Name: name, Desc: desc, Mode: mode, Status: "creating", Polls: 1}
		st.m[t.ID] = t
		st.next++
		ok(w, 202, map[string]any{"status": "accepted", "server_id": sv.ID, "template": tplJSON(t)}, nil)
		return true
	}
	if len(seg) < 2 || seg[1] != "templates" {
		return false
	}
	if len(seg) == 2 && m == "GET" {
		out := []map[string]any{}
		for i := int64(1); i < st.next; i++ {
			if t := st.m[i]; t != nil && t.Owner == owner && t.Status != "deleted" {
				out = append(out, tplJSON(t))
			}
		}
		ok(w, 200, out, map[string]any{"total": len(out), "page": 1, "limit": 100, "next_page": 0, "count": len(out), "max": 10})
		return true
	}
	t := st.m[id(2)]
	if t == nil || t.Owner != owner || t.Status == "deleted" {
		fail(w, 404, "template_not_found", "Template not found.")
		return true
	}
	switch {
	case len(seg) == 3 && m == "GET":
		if t.Status == "creating" {
			if t.Polls <= 0 {
				t.Status = "ready"
			}
			t.Polls--
		}
		ok(w, 200, tplJSON(t), nil)
	case len(seg) == 3 && m == "PATCH":
		if n, has := body["name"].(string); has {
			t.Name = n
		}
		if d, has := body["description"].(string); has {
			t.Desc = d
		}
		ok(w, 200, tplJSON(t), nil)
	case len(seg) == 3 && m == "DELETE":
		t.Status = "deleted"
		ok(w, 202, map[string]any{"status": "accepted", "template_id": t.ID}, nil)
	case len(seg) == 4 && seg[3] == "servers" && m == "POST":
		if t.Status != "ready" {
			fail(w, 409, "template_not_ready", "The template is not ready.")
			return true
		}
		// same money rules as POST /cloud/servers (confirm, Idempotency-Key, balance); installed from the template
		b2 := map[string]any{"image": "ubuntu-24.04", "region": "tr-ist-1"}
		for k, v := range body {
			b2[k] = v
		}
		before := a.nextSrv
		a.create(w, r, owner, b2)
		if s := a.servers[before]; s != nil && a.nextSrv > before {
			s.Image = fmt.Sprintf("custom:%d", t.ID)
		}
	default:
		fail(w, 404, "endpoint_not_found", "No API endpoint matches "+m+" /"+strings.Join(seg, "/")+".")
	}
	return true
}
