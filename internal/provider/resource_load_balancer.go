package provider

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*lbResource)(nil)
	_ resource.ResourceWithImportState = (*lbResource)(nil)
)

type lbResource struct{ pd *providerData }

func newLoadBalancerResource() resource.Resource { return &lbResource{} }

type lbHealthModel struct {
	Protocol    types.String `tfsdk:"protocol"`
	Port        types.Int64  `tfsdk:"port"`
	Interval    types.Int64  `tfsdk:"interval"`
	Timeout     types.Int64  `tfsdk:"timeout"`
	Retries     types.Int64  `tfsdk:"retries"`
	Path        types.String `tfsdk:"path"`
	Domain      types.String `tfsdk:"domain"`
	StatusCodes types.List   `tfsdk:"status_codes"`
	TLS         types.Bool   `tfsdk:"tls"`
}

type lbServiceModel struct {
	Protocol        types.String   `tfsdk:"protocol"`
	ListenPort      types.Int64    `tfsdk:"listen_port"`
	DestinationPort types.Int64    `tfsdk:"destination_port"`
	ProxyProtocol   types.Bool     `tfsdk:"proxyprotocol"`
	HealthCheck     *lbHealthModel `tfsdk:"health_check"`
	StickySessions  types.Bool     `tfsdk:"sticky_sessions"`
	CookieName      types.String   `tfsdk:"cookie_name"`
	CookieLifetime  types.Int64    `tfsdk:"cookie_lifetime"`
	IdleTimeout     types.Int64    `tfsdk:"idle_timeout"`
	CertificateIDs  types.List     `tfsdk:"certificate_ids"`
	RedirectHTTP    types.Bool     `tfsdk:"redirect_http"`
}

type lbTargetModel struct {
	Type         types.String `tfsdk:"type"`
	ServerID     types.Int64  `tfsdk:"server_id"`
	IP           types.String `tfsdk:"ip"`
	UsePrivateIP types.Bool   `tfsdk:"use_private_ip"`
}

type lbModel struct {
	ID           types.String     `tfsdk:"id"`
	Size         types.String     `tfsdk:"size"`
	Region       types.String     `tfsdk:"region"`
	Cycle        types.String     `tfsdk:"cycle"`
	ConfirmOrder types.Bool       `tfsdk:"confirm_order"`
	Algorithm    types.String     `tfsdk:"algorithm"`
	Services     []lbServiceModel `tfsdk:"services"`
	Targets      []lbTargetModel  `tfsdk:"targets"`
	Name         types.String     `tfsdk:"name"`
	Status       types.String     `tfsdk:"status"`
	IPv4         types.String     `tfsdk:"ipv4"`
	IPv6         types.String     `tfsdk:"ipv6"`
	Price        types.Float64    `tfsdk:"price"`
	Currency     types.String     `tfsdk:"currency"`
	DueDate      types.String     `tfsdk:"due_date"`
	Immediate    types.Bool       `tfsdk:"delete_immediately"`
}

func (r *lbResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_load_balancer"
}

func (r *lbResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	}
	optInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Optional: true, Description: desc}
	}
	optBool := func(desc string) schema.BoolAttribute { return schema.BoolAttribute{Optional: true, Description: desc} }
	optStr := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "A Gyro Cloud load balancer (managed L4/L7, EU regions). Creating one places an order paid from the account balance " +
			"(confirm_order must be true). services and targets are authoritative when set: anything else on the load balancer is removed; leave them unset to manage them elsewhere (panel, CLI). " +
			"Destroying cancels the service; traffic stops at once and the load balancer is destroyed after the grace period.",
		Attributes: map[string]schema.Attribute{
			"id": str("Load balancer id (service id in the client area)."),
			"size": schema.StringAttribute{Required: true, Description: "small, medium or large (see GET /cloud/load-balancer-types). Changing it replaces the load balancer.",
				Validators:    []validator.String{stringvalidator.OneOf("small", "medium", "large")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"region": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("eu-de-1"),
				Description:   "eu-de-1 (default), eu-de-2 or eu-fi-1. Targets must be cloud servers in an EU region. Changing it replaces the load balancer.",
				Validators:    []validator.String{stringvalidator.OneOf("eu-de-1", "eu-de-2", "eu-fi-1")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"cycle": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("monthly"),
				Description:   "Billing cycle: monthly (default), quarterly, semiannually, annually.",
				Validators:    []validator.String{stringvalidator.OneOf("monthly", "quarterly", "semiannually", "annually")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"confirm_order": schema.BoolAttribute{Required: true, Description: "Must be true: creating a load balancer spends account balance."},
			"algorithm": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("round_robin"),
				Description: "round_robin (default) or least_connections.", Validators: []validator.String{stringvalidator.OneOf("round_robin", "least_connections")}},
			"services": schema.ListNestedAttribute{Optional: true, Description: "Listeners, keyed by listen_port. HTTPS needs certificate_ids of certificates on this load balancer (upload or Let's Encrypt from the panel/API).",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"protocol":         schema.StringAttribute{Required: true, Description: "tcp, http or https.", Validators: []validator.String{stringvalidator.OneOf("tcp", "http", "https")}},
					"listen_port":      schema.Int64Attribute{Required: true, Description: "Port the load balancer listens on."},
					"destination_port": schema.Int64Attribute{Required: true, Description: "Port on the targets."},
					"proxyprotocol":    optBool("Send the PROXY protocol header to targets."),
					"health_check": schema.SingleNestedAttribute{Optional: true, Description: "Health check; defaults: TCP for tcp, HTTP GET / for http/https, 15 s interval, 10 s timeout, 3 retries.",
						Attributes: map[string]schema.Attribute{
							"protocol":     schema.StringAttribute{Optional: true, Description: "tcp or http.", Validators: []validator.String{stringvalidator.OneOf("tcp", "http")}},
							"port":         optInt("Port to check (default destination_port)."),
							"interval":     optInt("Seconds between checks (3-60)."),
							"timeout":      optInt("Seconds before a check fails (1-30, below interval)."),
							"retries":      optInt("Failed checks before a target is unhealthy (0-5)."),
							"path":         optStr("HTTP path (starts with /)."),
							"domain":       optStr("HTTP Host header."),
							"status_codes": schema.ListAttribute{ElementType: types.StringType, Optional: true, Description: "Accepted HTTP codes, e.g. [\"2??\", \"3??\"]."},
							"tls":          optBool("HTTP check over TLS."),
						}},
					"sticky_sessions": optBool("Sticky sessions (http/https)."),
					"cookie_name":     optStr("Sticky session cookie name."),
					"cookie_lifetime": optInt("Sticky session cookie lifetime (s)."),
					"idle_timeout":    optInt("Idle timeout (s, http/https)."),
					"certificate_ids": schema.ListAttribute{ElementType: types.Int64Type, Optional: true, Description: "HTTPS: certificate ids of this load balancer."},
					"redirect_http":   optBool("HTTPS: redirect port 80 to HTTPS (port 80 must not have its own service)."),
				}}},
			"targets": schema.ListNestedAttribute{Optional: true, Description: "Targets: your cloud servers (server_id = gyro_server id, same EU region), all your cloud servers (type all), or your dedicated server IPs.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"type":           schema.StringAttribute{Required: true, Description: "server, all or ip.", Validators: []validator.String{stringvalidator.OneOf("server", "all", "ip")}},
					"server_id":      optInt("gyro_server id (type server)."),
					"ip":             optStr("Dedicated server IP (type ip)."),
					"use_private_ip": optBool("Reach the target over the private network (attach one in the panel/API first)."),
				}}},
			"name":   str("Load balancer name."),
			"status": str("Service status (active, suspended, ...)."),
			"ipv4":   str("Public IPv4 address (point your A record here)."),
			"ipv6":   str("Public IPv6 address."),
			"price": schema.Float64Attribute{Computed: true, Description: "Recurring price per cycle (VAT excluded).",
				PlanModifiers: []planmodifier.Float64{float64planmodifier.UseStateForUnknown()}},
			"currency": str("Price currency."),
			"due_date": str("Next due date."),
			"delete_immediately": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "destroy cancels at once (default). false = file a cancellation request instead."},
		},
	}
}

func (r *lbResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
		return
	}
	r.pd = pd
}

/* ---------------------------------------------------------------- mapping */

func setIfKnown[T any](dst *T, keep bool, v T) {
	if keep {
		*dst = v
	}
}

// fromAPI keeps the user's shape: optional fields that are null in the prior model stay null (no diff against
// configs that omit them); fields that are set are refreshed from the API (drift is shown).
func lbServiceFromAPI(prior *lbServiceModel, s client.LBService) lbServiceModel {
	full := prior == nil
	p := lbServiceModel{}
	if prior != nil {
		p = *prior
	}
	out := lbServiceModel{Protocol: types.StringValue(s.Protocol), ListenPort: types.Int64Value(s.ListenPort), DestinationPort: types.Int64Value(s.DestinationPort),
		ProxyProtocol: p.ProxyProtocol, StickySessions: p.StickySessions, CookieName: p.CookieName, CookieLifetime: p.CookieLifetime, IdleTimeout: p.IdleTimeout,
		CertificateIDs: p.CertificateIDs, RedirectHTTP: p.RedirectHTTP}
	if full {
		out.ProxyProtocol, out.StickySessions, out.RedirectHTTP = types.BoolNull(), types.BoolNull(), types.BoolNull()
		out.CookieName, out.CookieLifetime, out.IdleTimeout = types.StringNull(), types.Int64Null(), types.Int64Null()
		out.CertificateIDs = types.ListNull(types.Int64Type)
	}
	setIfKnown(&out.ProxyProtocol, full || !p.ProxyProtocol.IsNull(), types.BoolValue(s.ProxyProtocol))
	if s.Protocol != "tcp" {
		setIfKnown(&out.StickySessions, full || !p.StickySessions.IsNull(), types.BoolValue(s.Sticky))
		setIfKnown(&out.CookieName, full || !p.CookieName.IsNull(), types.StringValue(s.CookieName))
		setIfKnown(&out.CookieLifetime, full || !p.CookieLifetime.IsNull(), types.Int64Value(s.CookieLifetime))
		setIfKnown(&out.IdleTimeout, full || !p.IdleTimeout.IsNull(), types.Int64Value(s.IdleTimeout))
	}
	if s.Protocol == "https" {
		ids := s.Certificates
		if ids == nil {
			ids = []int64{}
		}
		lv, _ := types.ListValueFrom(context.Background(), types.Int64Type, ids)
		setIfKnown(&out.CertificateIDs, full || !p.CertificateIDs.IsNull(), lv)
		setIfKnown(&out.RedirectHTTP, full || !p.RedirectHTTP.IsNull(), types.BoolValue(s.RedirectHTTP))
	}
	if p.HealthCheck != nil || full {
		h := &lbHealthModel{Protocol: types.StringNull(), Port: types.Int64Null(), Interval: types.Int64Null(), Timeout: types.Int64Null(), Retries: types.Int64Null(),
			Path: types.StringNull(), Domain: types.StringNull(), StatusCodes: types.ListNull(types.StringType), TLS: types.BoolNull()}
		ph := p.HealthCheck
		if ph == nil {
			ph = &lbHealthModel{Protocol: types.StringValue("x"), Port: types.Int64Value(0), Interval: types.Int64Value(0), Timeout: types.Int64Value(0), Retries: types.Int64Value(0)}
			if s.HealthCheck.HTTP != nil {
				ph.Path, ph.Domain, ph.TLS = types.StringValue("x"), types.StringValue("x"), types.BoolValue(false)
				ph.StatusCodes = types.ListValueMust(types.StringType, nil)
			}
		}
		hc := s.HealthCheck
		setIfKnown(&h.Protocol, !ph.Protocol.IsNull(), types.StringValue(hc.Protocol))
		setIfKnown(&h.Port, !ph.Port.IsNull(), types.Int64Value(hc.Port))
		setIfKnown(&h.Interval, !ph.Interval.IsNull(), types.Int64Value(hc.Interval))
		setIfKnown(&h.Timeout, !ph.Timeout.IsNull(), types.Int64Value(hc.Timeout))
		setIfKnown(&h.Retries, !ph.Retries.IsNull(), types.Int64Value(hc.Retries))
		if hc.HTTP != nil {
			setIfKnown(&h.Path, !ph.Path.IsNull(), types.StringValue(hc.HTTP.Path))
			setIfKnown(&h.Domain, !ph.Domain.IsNull(), types.StringValue(hc.HTTP.Domain))
			setIfKnown(&h.TLS, !ph.TLS.IsNull(), types.BoolValue(hc.HTTP.TLS))
			if !ph.StatusCodes.IsNull() {
				h.StatusCodes, _ = types.ListValueFrom(context.Background(), types.StringType, hc.HTTP.StatusCodes)
			}
		}
		out.HealthCheck = h
	}
	return out
}

func targetKey(t lbTargetModel) string {
	switch t.Type.ValueString() {
	case "server":
		return "server:" + strconv.FormatInt(t.ServerID.ValueInt64(), 10)
	case "ip":
		return "ip:" + t.IP.ValueString()
	}
	return "all"
}

func lbTargetFromAPI(prior *lbTargetModel, t client.LBTarget) lbTargetModel {
	out := lbTargetModel{Type: types.StringValue(t.Type), ServerID: types.Int64Null(), IP: types.StringNull(), UsePrivateIP: types.BoolNull()}
	if t.Type == "server" {
		out.ServerID = types.Int64Value(t.ServiceID)
	}
	if t.Type == "ip" {
		out.IP = types.StringValue(t.IP)
	}
	if t.Type != "ip" && (prior == nil || !prior.UsePrivateIP.IsNull()) {
		out.UsePrivateIP = types.BoolValue(t.UsePrivateIP)
	}
	return out
}

func applyLB(m *lbModel, lb *client.LoadBalancer) {
	m.ID = types.StringValue(strconv.FormatInt(lb.ID, 10))
	m.Status = types.StringValue(lb.Status)
	if lb.Cycle != "" {
		m.Cycle = types.StringValue(lb.Cycle)
	}
	m.Price = types.Float64Value(lb.Price.Amount)
	m.Currency = sp(lb.Price.Currency)
	m.DueDate = sp(lb.DueDate)
	l := lb.Live
	if l == nil {
		if m.Name.IsUnknown() {
			m.Name = types.StringNull()
		}
		if m.IPv4.IsUnknown() {
			m.IPv4 = types.StringNull()
		}
		if m.IPv6.IsUnknown() {
			m.IPv6 = types.StringNull()
		}
		return
	}
	m.Name = types.StringValue(l.Name)
	m.IPv4 = sp(l.IPv4)
	m.IPv6 = sp(l.IPv6)
	if l.Size != nil && (m.Size.IsNull() || m.Size.IsUnknown() || m.Size.ValueString() == "") {
		m.Size = types.StringValue(*l.Size)
	}
	if m.Region.IsNull() || m.Region.IsUnknown() || m.Region.ValueString() == "" {
		m.Region = types.StringValue(l.Region)
	}
	if l.Algorithm != "" {
		m.Algorithm = types.StringValue(l.Algorithm)
	}
	if m.Services != nil {
		prior := map[int64]*lbServiceModel{}
		for i := range m.Services {
			prior[m.Services[i].ListenPort.ValueInt64()] = &m.Services[i]
		}
		out := make([]lbServiceModel, 0, len(l.Services))
		for _, s := range l.Services {
			out = append(out, lbServiceFromAPI(prior[s.ListenPort], s))
		}
		order := map[int64]int{}
		for i, s := range m.Services {
			order[s.ListenPort.ValueInt64()] = i
		}
		sort.SliceStable(out, func(a, b int) bool {
			return rank(order, out[a].ListenPort.ValueInt64()) < rank(order, out[b].ListenPort.ValueInt64())
		})
		m.Services = out
	}
	if m.Targets != nil {
		prior := map[string]*lbTargetModel{}
		order := map[string]int{}
		for i := range m.Targets {
			prior[targetKey(m.Targets[i])] = &m.Targets[i]
			order[targetKey(m.Targets[i])] = i
		}
		out := make([]lbTargetModel, 0, len(l.Targets))
		for _, t := range l.Targets {
			x := lbTargetFromAPI(nil, t)
			out = append(out, lbTargetFromAPI(prior[targetKey(x)], t))
		}
		sort.SliceStable(out, func(a, b int) bool { return rankS(order, targetKey(out[a])) < rankS(order, targetKey(out[b])) })
		m.Targets = out
	}
}

func rank(o map[int64]int, k int64) int {
	if v, ok := o[k]; ok {
		return v
	}
	return 1 << 20
}

func rankS(o map[string]int, k string) int {
	if v, ok := o[k]; ok {
		return v
	}
	return 1 << 20
}

/* ---------------------------------------------------------------- request bodies */

func serviceBody(ctx context.Context, s lbServiceModel, withKey bool) map[string]any {
	b := map[string]any{"destination_port": s.DestinationPort.ValueInt64()}
	if withKey {
		b["protocol"] = s.Protocol.ValueString()
		b["listen_port"] = s.ListenPort.ValueInt64()
	}
	if !s.ProxyProtocol.IsNull() {
		b["proxyprotocol"] = s.ProxyProtocol.ValueBool()
	}
	if s.Protocol.ValueString() != "tcp" {
		if !s.StickySessions.IsNull() {
			b["sticky"] = s.StickySessions.ValueBool()
		}
		if !s.CookieName.IsNull() {
			b["cookie_name"] = s.CookieName.ValueString()
		}
		if !s.CookieLifetime.IsNull() {
			b["cookie_lifetime"] = s.CookieLifetime.ValueInt64()
		}
		if !s.IdleTimeout.IsNull() {
			b["idle_timeout"] = s.IdleTimeout.ValueInt64()
		}
	}
	if s.Protocol.ValueString() == "https" {
		var ids []int64
		if !s.CertificateIDs.IsNull() {
			_ = s.CertificateIDs.ElementsAs(ctx, &ids, false)
		}
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		b["certificates"] = strings.Join(parts, ",")
		if !s.RedirectHTTP.IsNull() {
			b["redirect_http"] = s.RedirectHTTP.ValueBool()
		}
	}
	if h := s.HealthCheck; h != nil {
		if !h.Protocol.IsNull() {
			b["hc_protocol"] = h.Protocol.ValueString()
		}
		if !h.Port.IsNull() {
			b["hc_port"] = h.Port.ValueInt64()
		}
		if !h.Interval.IsNull() {
			b["hc_interval"] = h.Interval.ValueInt64()
		}
		if !h.Timeout.IsNull() {
			b["hc_timeout"] = h.Timeout.ValueInt64()
		}
		if !h.Retries.IsNull() {
			b["hc_retries"] = h.Retries.ValueInt64()
		}
		if !h.Path.IsNull() {
			b["hc_path"] = h.Path.ValueString()
		}
		if !h.Domain.IsNull() {
			b["hc_domain"] = h.Domain.ValueString()
		}
		if !h.TLS.IsNull() {
			b["hc_tls"] = h.TLS.ValueBool()
		}
		if !h.StatusCodes.IsNull() {
			var codes []string
			_ = h.StatusCodes.ElementsAs(ctx, &codes, false)
			b["hc_status_codes"] = strings.Join(codes, ",")
		}
	}
	return b
}

func targetBody(t lbTargetModel, add bool) map[string]any {
	b := map[string]any{"type": t.Type.ValueString()}
	if t.Type.ValueString() == "server" {
		b["server"] = t.ServerID.ValueInt64()
	}
	if t.Type.ValueString() == "ip" {
		b["ip"] = t.IP.ValueString()
	}
	if add && !t.UsePrivateIP.IsNull() {
		b["use_private_ip"] = t.UsePrivateIP.ValueBool()
	}
	if !add {
		b["confirm"] = true
	}
	return b
}

// reconcile makes the load balancer match the plan (services and targets are authoritative when set).
func (r *lbResource) reconcile(ctx context.Context, id int64, plan lbModel, cur *client.LoadBalancer) error {
	c := r.pd.c
	if cur.Live == nil {
		return fmt.Errorf("load balancer %d is not provisioned yet", id)
	}
	if a := plan.Algorithm.ValueString(); a != "" && a != cur.Live.Algorithm {
		if err := c.LBWrite(ctx, http.MethodPut, id, "algorithm", map[string]any{"algorithm": a}); err != nil {
			return fmt.Errorf("algorithm: %w", err)
		}
	}
	if plan.Targets != nil {
		want := map[string]lbTargetModel{}
		for _, t := range plan.Targets {
			want[targetKey(t)] = t
		}
		have := map[string]client.LBTarget{}
		for _, t := range cur.Live.Targets {
			have[targetKey(lbTargetFromAPI(nil, t))] = t
		}
		for k, t := range have {
			w, keep := want[k]
			if keep && (w.UsePrivateIP.IsNull() || w.UsePrivateIP.ValueBool() == t.UsePrivateIP || t.Type == "ip") {
				continue
			}
			if err := c.LBWrite(ctx, http.MethodDelete, id, "targets", targetBody(lbTargetFromAPI(nil, t), false)); err != nil {
				return fmt.Errorf("remove target %s: %w", k, err)
			}
			delete(have, k)
		}
		for k, t := range want {
			if _, ok := have[k]; ok {
				continue
			}
			if err := c.LBWrite(ctx, http.MethodPost, id, "targets", targetBody(t, true)); err != nil {
				return fmt.Errorf("add target %s: %w", k, err)
			}
		}
	}
	if plan.Services != nil {
		want := map[int64]lbServiceModel{}
		for _, s := range plan.Services {
			want[s.ListenPort.ValueInt64()] = s
		}
		have := map[int64]client.LBService{}
		for _, s := range cur.Live.Services {
			have[s.ListenPort] = s
		}
		// deletions first (frees ports, e.g. 80 before an HTTPS redirect is enabled)
		for p, s := range have {
			w, ok := want[p]
			if ok && w.Protocol.ValueString() == s.Protocol {
				continue
			}
			if err := c.LBWrite(ctx, http.MethodDelete, id, "services/"+strconv.FormatInt(p, 10), map[string]any{"confirm": true}); err != nil {
				return fmt.Errorf("delete service %d: %w", p, err)
			}
			delete(have, p)
		}
		ports := make([]int64, 0, len(want))
		for p := range want {
			ports = append(ports, p)
		}
		sort.Slice(ports, func(a, b int) bool { return ports[a] < ports[b] })
		for _, p := range ports {
			s := want[p]
			if _, ok := have[p]; ok {
				if err := c.LBWrite(ctx, http.MethodPatch, id, "services/"+strconv.FormatInt(p, 10), serviceBody(ctx, s, false)); err != nil {
					return fmt.Errorf("update service %d: %w", p, err)
				}
				continue
			}
			if err := c.LBWrite(ctx, http.MethodPost, id, "services", serviceBody(ctx, s, true)); err != nil {
				return fmt.Errorf("add service %d: %w", p, err)
			}
		}
	}
	return nil
}

/* ---------------------------------------------------------------- CRUD */

func (r *lbResource) waitLive(ctx context.Context, id int64) (*client.LoadBalancer, error) {
	wctx, cancel := context.WithTimeout(ctx, r.pd.pollTimeout)
	defer cancel()
	for {
		lb, err := r.pd.c.LoadBalancer(wctx, id)
		if err == nil && lb.Live != nil && lb.Live.IPv4 != nil && *lb.Live.IPv4 != "" && lb.Status == "active" {
			return lb, nil
		}
		select {
		case <-wctx.Done():
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("load balancer %d is not active after %s (status %q)", id, r.pd.pollTimeout, lb.Status)
		case <-time.After(r.pd.pollInterval):
		}
	}
}

func (r *lbResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m lbModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.ConfirmOrder.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("confirm_order"), "Order not confirmed",
			"Creating a gyro_load_balancer places a paid order from your account balance. Set confirm_order = true to proceed.")
		return
	}
	res, err := r.pd.c.CreateLoadBalancer(ctx, client.CreateLoadBalancerRequest{Size: m.Size.ValueString(), Region: m.Region.ValueString(), Cycle: m.Cycle.ValueString(), Confirm: true}, client.NewIdempotencyKey())
	if err != nil {
		resp.Diagnostics.AddError("Ordering the load balancer failed", err.Error())
		return
	}
	if res.LoadBalancer == nil || res.LoadBalancer.ID == 0 {
		resp.Diagnostics.AddError("Ordering the load balancer failed", "The API did not return an id.")
		return
	}
	id := res.LoadBalancer.ID
	if res.Payment != nil && res.Payment.Status != "" && res.Payment.Status != "paid" {
		resp.Diagnostics.AddError("Payment failed", fmt.Sprintf("Load balancer %d was ordered but payment status is %q: %s. Pay the invoice, then import it.", id, res.Payment.Status, res.Payment.Error))
		return
	}
	partial := m
	partial.Services, partial.Targets = nil, nil
	applyLB(&partial, res.LoadBalancer)
	partial.Services, partial.Targets = m.Services, m.Targets
	resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
	lb, err := r.waitLive(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Load balancer did not become ready", err.Error())
		return
	}
	if err := r.reconcile(ctx, id, m, lb); err != nil {
		resp.Diagnostics.AddError("Configuring the load balancer failed", err.Error())
		return
	}
	if lb, err = r.pd.c.LoadBalancer(ctx, id); err != nil {
		resp.Diagnostics.AddError("Reading the load balancer failed", err.Error())
		return
	}
	applyLB(&m, lb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *lbResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m lbModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	lb, err := r.pd.c.LoadBalancer(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading load balancer failed", err.Error())
		return
	}
	if lb.Status == "cancelled" || (lb.Cancellation != nil && *lb.Cancellation != "") {
		resp.State.RemoveResource(ctx)
		return
	}
	applyLB(&m, lb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *lbResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lbModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	lb, err := r.pd.c.LoadBalancer(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading load balancer failed", err.Error())
		return
	}
	if err := r.reconcile(ctx, id, plan, lb); err != nil {
		resp.Diagnostics.AddError("Updating the load balancer failed", err.Error())
		return
	}
	if lb, err = r.pd.c.LoadBalancer(ctx, id); err != nil {
		resp.Diagnostics.AddError("Reading load balancer failed", err.Error())
		return
	}
	applyLB(&plan, lb)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lbResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m lbModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteLoadBalancer(ctx, id, m.Immediate.IsNull() || m.Immediate.ValueBool()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cancelling load balancer failed", err.Error())
	}
}

func (r *lbResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric load balancer id, e.g. terraform import gyro_load_balancer.web 47561")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("confirm_order"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_immediately"), true)...)
}
