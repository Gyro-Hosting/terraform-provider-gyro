package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Network firewall of a server (GyroAPI >= 2.8.0): /cloud/servers/{id}/firewall/policy.
// AWS network ACL / ufw model: ordered rules, first match wins, default_action for unmatched inbound traffic.

var _ resource.ResourceWithImportState = (*firewallResource)(nil)

type firewallResource struct{ pd *providerData }

func newFirewallResource() resource.Resource { return &firewallResource{} }

type firewallRuleModel struct {
	Type        types.String `tfsdk:"type"`
	Direction   types.String `tfsdk:"direction"`
	Action      types.String `tfsdk:"action"`
	Protocol    types.String `tfsdk:"protocol"`
	Source      types.String `tfsdk:"source"`
	Destination types.String `tfsdk:"destination"`
	Port        types.String `tfsdk:"port"`
	Description types.String `tfsdk:"description"`
}

type firewallModel struct {
	ID             types.String        `tfsdk:"id"`
	ServerID       types.Int64         `tfsdk:"server_id"`
	DefaultAction  types.String        `tfsdk:"default_action"`
	Ping           types.Bool          `tfsdk:"ping"`
	Permanent      types.Bool          `tfsdk:"permanent"`
	AcceptWarnings types.Bool          `tfsdk:"accept_warnings"`
	Rules          []firewallRuleModel `tfsdk:"rule"`
	Status         types.String        `tfsdk:"status"`
	Engine         types.String        `tfsdk:"engine"`
}

type firewallRuleJSON struct {
	Type        string `json:"type"`
	Direction   string `json:"direction"`
	Action      string `json:"action"`
	Protocol    string `json:"protocol"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Port        string `json:"port"`
	Description string `json:"description"`
}

type firewallPolicyJSON struct {
	DefaultAction string             `json:"default_action"`
	Ping          bool               `json:"ping"`
	Status        string             `json:"status"`
	Engine        string             `json:"engine"`
	Rules         []firewallRuleJSON `json:"rules"`
}

func (r *firewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall"
}

func (r *firewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	str := func(def, d string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(def), Description: d}
	}
	resp.Schema = schema.Schema{
		Description: "Network firewall of a server (requires the Network Firewall add-on). Works like an AWS network ACL / ufw: rules are evaluated " +
			"in the given order and the first match wins; default_action applies to inbound traffic that matches no rule. The resource owns every " +
			"customer rule of the server: apply replaces them, destroy removes them (default allow). Rules applied by Gyro support are not managed here. " +
			"Gyro Cloud servers (engine = cloud): allow rules only, default_action deny, inbound only, no destination or country sources.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"server_id":      schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Server id."},
			"default_action": str("allow", "Default rule (*) for inbound traffic that matches no rule: allow or deny."),
			"ping":           schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Answer ping (ICMP echo)."},
			"permanent": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Make each change permanent at once (default). false = the change is a 5 minute trial that reverts unless confirmed in the panel or via the API."},
			"accept_warnings": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Apply even when the API reports warnings (lock-out risk, shadowed or duplicate rules, private sources). Without it such a change fails and lists the warnings."},
			"status": schema.StringAttribute{Computed: true, Description: "none, applying, trial, active, open, removing."},
			"engine": schema.StringAttribute{Computed: true, Description: "network (Istanbul core router) or cloud (provider firewall)."},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "Rules in evaluation order (first match wins).",
				NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
					"type":        str("rule", "rule, brute_force (port = protected ports) or port_scan."),
					"direction":   str("in", "in (to the server) or out (new outbound connections; source = remote address)."),
					"action":      str("allow", "allow or deny."),
					"protocol":    str("tcp", "tcp, udp, tcp_udp, icmp or all."),
					"source":      str("any", "any, an IPv4 address/CIDR or a comma separated list. Country sources only when the platform offers them (policy capabilities.country_filter; currently not offered)."),
					"destination": str("any", "any (all IP addresses of the server) or one IP address of the server."),
					"port":        str("any", "any, 22, 80,443 or 1000-2000 (not for icmp/all)."),
					"description": str("", "Up to 60 characters."),
				}},
			},
		},
	}
}

func (r *firewallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func fwPath(m *firewallModel) string {
	return fmt.Sprintf("cloud/servers/%d/firewall/policy", sid(m.ServerID))
}

// normalised comparison: the API stores 1.2.3.4 as 1.2.3.4/32, lists with ", ", empty port as any.
func fwNormSrc(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "0.0.0.0/0" {
		return "any"
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	for i, p := range parts {
		if !strings.Contains(p, "/") && !strings.Contains(p, ":") && p != "any" {
			parts[i] = p + "/32"
		}
	}
	return strings.Join(parts, ",")
}

func fwNorm(v, def string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return def
	}
	return v
}

func fwSame(a firewallRuleModel, b firewallRuleJSON) bool {
	port := b.Port
	if b.Type == "port_scan" {
		port = "any"
	}
	return fwNorm(a.Type.ValueString(), "rule") == fwNorm(b.Type, "rule") && fwNorm(a.Direction.ValueString(), "in") == fwNorm(b.Direction, "in") &&
		(b.Type != "rule" && b.Type != "" || fwNorm(a.Action.ValueString(), "allow") == fwNorm(b.Action, "allow") && fwNorm(a.Protocol.ValueString(), "tcp") == fwNorm(b.Protocol, "tcp") &&
			fwNormSrc(a.Source.ValueString()) == fwNormSrc(b.Source) && fwNorm(a.Destination.ValueString(), "any") == fwNorm(b.Destination, "any")) &&
		strings.ReplaceAll(fwNorm(a.Port.ValueString(), "any"), " ", "") == strings.ReplaceAll(fwNorm(port, "any"), " ", "") && a.Description.ValueString() == b.Description
}

func (r *firewallResource) get(ctx context.Context, m *firewallModel) (*firewallPolicyJSON, error) {
	var out firewallPolicyJSON
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fwPath(m), nil, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// apply keeps the user's spelling of equivalent rules (no perpetual diff) and takes the API's values on drift.
func (r *firewallResource) apply(m *firewallModel, p *firewallPolicyJSON) {
	m.ID = types.StringValue(strconv.FormatInt(sid(m.ServerID), 10))
	m.DefaultAction = types.StringValue(p.DefaultAction)
	m.Ping = types.BoolValue(p.Ping)
	m.Status = types.StringValue(p.Status)
	m.Engine = types.StringValue(p.Engine)
	same := len(m.Rules) == len(p.Rules)
	for i := 0; same && i < len(p.Rules); i++ {
		same = fwSame(m.Rules[i], p.Rules[i])
	}
	if same {
		return
	}
	m.Rules = make([]firewallRuleModel, 0, len(p.Rules))
	for _, x := range p.Rules {
		if x.Type == "" {
			x.Type = "rule"
		}
		def := func(v, d string) types.String {
			if v == "" {
				return types.StringValue(d)
			}
			return types.StringValue(v)
		}
		m.Rules = append(m.Rules, firewallRuleModel{Type: def(x.Type, "rule"), Direction: def(x.Direction, "in"), Action: def(x.Action, "allow"), Protocol: def(x.Protocol, "tcp"),
			Source: def(x.Source, "any"), Destination: def(x.Destination, "any"), Port: def(x.Port, "any"), Description: types.StringValue(x.Description)})
	}
}

func (r *firewallResource) put(ctx context.Context, m *firewallModel, rules []firewallRuleModel, def string) error {
	rl := make([]map[string]any, 0, len(rules))
	for _, x := range rules {
		rl = append(rl, map[string]any{"type": x.Type.ValueString(), "direction": x.Direction.ValueString(), "action": x.Action.ValueString(), "protocol": x.Protocol.ValueString(),
			"source": x.Source.ValueString(), "destination": x.Destination.ValueString(), "port": x.Port.ValueString(), "description": x.Description.ValueString(),
			"threshold": 10})
	}
	_, err := r.pd.c.Do(ctx, http.MethodPut, fwPath(m), nil, map[string]any{"default_action": def, "ping": m.Ping.ValueBool(), "rules": rl,
		"permanent": m.Permanent.ValueBool(), "confirm_warnings": m.AcceptWarnings.ValueBool()}, "", nil)
	return err
}

func (r *firewallResource) save(ctx context.Context, m *firewallModel, d interface{ AddError(string, string) }) bool {
	if err := r.put(ctx, m, m.Rules, m.DefaultAction.ValueString()); err != nil {
		d.AddError("Saving firewall failed", err.Error())
		return false
	}
	p, err := r.get(ctx, m)
	if err != nil {
		d.AddError("Reading firewall failed", err.Error())
		return false
	}
	r.apply(m, p)
	return true
}

func (r *firewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *firewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	p, err := r.get(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "firewall_not_enabled") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading firewall failed", err.Error())
		return
	}
	r.apply(&m, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *firewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *firewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.Do(ctx, http.MethodDelete, fwPath(&m), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "firewall_not_enabled") {
		resp.Diagnostics.AddError("Removing firewall rules failed", err.Error())
	}
}

func (r *firewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the server id, e.g. 1234")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("permanent"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("accept_warnings"), false)...)
}
