package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Security groups (GyroAPI >= 2.9.0): reusable allow lists attached to servers with the network firewall add-on.
// gyro_security_group owns the group and all of its rules; gyro_server_security_groups owns the attached list of one server.

var (
	_ resource.ResourceWithImportState = (*securityGroupResource)(nil)
	_ resource.ResourceWithImportState = (*serverSecurityGroupsResource)(nil)
)

type securityGroupResource struct{ pd *providerData }

func newSecurityGroupResource() resource.Resource { return &securityGroupResource{} }

type sgRuleModel struct {
	Direction   types.String   `tfsdk:"direction"`
	Type        types.String   `tfsdk:"type"`
	Port        types.String   `tfsdk:"port"`
	Source      []types.String `tfsdk:"source"`
	Description types.String   `tfsdk:"description"`
	RuleID      types.String   `tfsdk:"rule_id"`
}

type sgModel struct {
	ID          types.String  `tfsdk:"id"`
	Name        types.String  `tfsdk:"name"`
	Description types.String  `tfsdk:"description"`
	Confirm     types.Bool    `tfsdk:"confirm_trial"`
	Rules       []sgRuleModel `tfsdk:"rule"`
	Status      types.String  `tfsdk:"status"`
	Servers     types.List    `tfsdk:"servers"`
}

type sgRuleJSON struct {
	ID          string   `json:"id"`
	Direction   string   `json:"direction"`
	Type        string   `json:"type"`
	Port        string   `json:"port"`
	Source      []string `json:"source"`
	Description string   `json:"description"`
}

type sgJSON struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Status      string       `json:"status"`
	Rules       []sgRuleJSON `json:"rules"`
	Servers     []int64      `json:"servers"`
	Trial       bool         `json:"trial"`
}

func (r *securityGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

var sgTypes = "ssh, http, https, rdp, mysql, postgresql, mssql, redis, mongodb, smtp, smtps, imaps, dns_udp, dns_tcp, all_tcp, all_udp, all_icmp, all, custom_tcp, custom_udp"

func (r *securityGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Security group (AWS security group model) for servers with the Network Firewall add-on: a reusable, stateful list of ALLOW rules. " +
			"Attach it with gyro_server_security_groups (up to 5 groups per server). The resource owns all rules of the group; a group without " +
			"outbound rules does not restrict outbound traffic. Destroy fails while the group is attached.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Group id (sg-xxxxxxxx)."},
			"name":        schema.StringAttribute{Required: true, Description: "Display name (2-64 characters, unique in the account). Renaming keeps the group."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Up to 255 characters."},
			"confirm_trial": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "A rule change that could cut SSH/RDP access on an attached server runs as a 5 minute trial; true (default) confirms it right after apply."},
			"status":  schema.StringAttribute{Computed: true, Description: "unused, applying, active or trial."},
			"servers": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Servers the group is attached to."},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "Allow rules (inbound and outbound).",
				NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
					"direction":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("in"), Description: "in (to the servers) or out (new connections from the servers; source = remote address)."},
					"type":        schema.StringAttribute{Required: true, Description: "One of " + sgTypes + ". The type fixes protocol and port; custom_tcp / custom_udp take port."},
					"port":        schema.StringAttribute{Optional: true, Computed: true, Description: "For custom_tcp / custom_udp: 8080 or 8000-8100. Computed for the other types."},
					"source": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("0.0.0.0/0")})),
				Description: "IPv4 / CIDR list; default [\"0.0.0.0/0\"] (anywhere)."},
					"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Up to 60 characters."},
					"rule_id":     schema.StringAttribute{Computed: true, Description: "Rule id assigned by the API (gfr-xxxxxxxx)."},
				}},
			},
		},
	}
}

func (r *securityGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func sgNormSrc(l []string) string {
	o := make([]string, 0, len(l))
	for _, s := range l {
		s = strings.TrimSpace(s)
		if s != "" && !strings.Contains(s, "/") {
			s += "/32"
		}
		o = append(o, s)
	}
	if len(o) == 0 {
		return "0.0.0.0/0"
	}
	return strings.Join(o, ",")
}

func sgStrs(l []types.String) []string {
	o := make([]string, 0, len(l))
	for _, x := range l {
		o = append(o, x.ValueString())
	}
	return o
}

func sgSame(a sgRuleModel, b sgRuleJSON) bool {
	at := a.Type.ValueString()
	if (at == "custom_tcp" || at == "custom_udp") && strings.TrimSpace(a.Port.ValueString()) != b.Port {
		return false
	}
	return fwNorm(a.Direction.ValueString(), "in") == fwNorm(b.Direction, "in") && at == b.Type && sgNormSrc(sgStrs(a.Source)) == sgNormSrc(b.Source) && a.Description.ValueString() == b.Description
}

func (r *securityGroupResource) apply(m *sgModel, g *sgJSON) {
	m.ID = types.StringValue(g.ID)
	m.Name = types.StringValue(g.Name)
	m.Description = types.StringValue(g.Description)
	m.Status = types.StringValue(g.Status)
	sv := make([]attr.Value, 0, len(g.Servers))
	for _, s := range g.Servers {
		sv = append(sv, types.Int64Value(s))
	}
	m.Servers = types.ListValueMust(types.Int64Type, sv)
	same := len(m.Rules) == len(g.Rules)
	for i := 0; same && i < len(g.Rules); i++ {
		same = sgSame(m.Rules[i], g.Rules[i])
	}
	if same { // keep the user's spelling, fill computed values
		for i, x := range g.Rules {
			m.Rules[i].RuleID = types.StringValue(x.ID)
			m.Rules[i].Port = types.StringValue(x.Port)
			m.Rules[i].Direction = types.StringValue(fwNorm(x.Direction, "in"))
			if m.Rules[i].Source == nil {
				m.Rules[i].Source = []types.String{}
				for _, s := range x.Source {
					m.Rules[i].Source = append(m.Rules[i].Source, types.StringValue(s))
				}
			}
		}
		return
	}
	m.Rules = make([]sgRuleModel, 0, len(g.Rules))
	for _, x := range g.Rules {
		src := []types.String{}
		for _, s := range x.Source {
			src = append(src, types.StringValue(s))
		}
		m.Rules = append(m.Rules, sgRuleModel{Direction: types.StringValue(fwNorm(x.Direction, "in")), Type: types.StringValue(x.Type), Port: types.StringValue(x.Port),
			Source: src, Description: types.StringValue(x.Description), RuleID: types.StringValue(x.ID)})
	}
}

func sgRulesBody(rules []sgRuleModel) []map[string]any {
	rl := make([]map[string]any, 0, len(rules))
	for _, x := range rules {
		src := sgStrs(x.Source)
		if len(src) == 0 {
			src = []string{"0.0.0.0/0"}
		}
		o := map[string]any{"direction": fwNorm(x.Direction.ValueString(), "in"), "type": x.Type.ValueString(), "source": src, "description": x.Description.ValueString()}
		if t := x.Type.ValueString(); t == "custom_tcp" || t == "custom_udp" {
			o["port"] = x.Port.ValueString()
		}
		rl = append(rl, o)
	}
	return rl
}

func (r *securityGroupResource) get(ctx context.Context, id string) (*sgJSON, error) {
	var g sgJSON
	if _, err := r.pd.c.Do(ctx, http.MethodGet, "cloud/security-groups/"+id, nil, nil, "", &g); err != nil {
		return nil, err
	}
	return &g, nil
}

func (r *securityGroupResource) putRules(ctx context.Context, m *sgModel) error {
	var g sgJSON
	if _, err := r.pd.c.Do(ctx, http.MethodPut, "cloud/security-groups/"+m.ID.ValueString()+"/rules", nil, map[string]any{"rules": sgRulesBody(m.Rules)}, "", &g); err != nil {
		return err
	}
	if g.Trial && m.Confirm.ValueBool() {
		return sgRetry(ctx, func() error {
			_, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/security-groups/"+m.ID.ValueString()+"/confirm", nil, map[string]any{}, "", nil)
			return err
		})
	}
	return nil
}

// sgRetry repeats a call while the server / group is busy applying the previous change (relay round trip, a few seconds).
func sgRetry(ctx context.Context, f func() error) error {
	var err error
	for i := 0; i < 30; i++ {
		if err = f(); err == nil || !(client.HasCode(err, "err_busy") || client.HasCode(err, "err_no_trial") && i < 3) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return err
}

func (r *securityGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m sgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var g sgJSON
	if _, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/security-groups", nil, map[string]any{"name": m.Name.ValueString(), "description": m.Description.ValueString(),
		"rules": sgRulesBody(m.Rules)}, "", &g); err != nil {
		resp.Diagnostics.AddError("Creating security group failed", err.Error())
		return
	}
	r.apply(&m, &g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *securityGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	g, err := r.get(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading security group failed", err.Error())
		return
	}
	r.apply(&m, g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *securityGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st sgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID = st.ID
	if m.Name.ValueString() != st.Name.ValueString() || m.Description.ValueString() != st.Description.ValueString() {
		if _, err := r.pd.c.Do(ctx, http.MethodPatch, "cloud/security-groups/"+m.ID.ValueString(), nil, map[string]any{"name": m.Name.ValueString(), "description": m.Description.ValueString()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Updating security group failed", err.Error())
			return
		}
	}
	if err := r.putRules(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Saving security group rules failed", err.Error())
		return
	}
	g, err := r.get(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading security group failed", err.Error())
		return
	}
	r.apply(&m, g)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *securityGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m sgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.Do(ctx, http.MethodDelete, "cloud/security-groups/"+m.ID.ValueString(), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting security group failed", err.Error())
	}
}

func (r *securityGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !strings.HasPrefix(req.ID, "sg-") {
		resp.Diagnostics.AddError("Invalid import id", "Use the group id, e.g. sg-1a2b3c4d")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("confirm_trial"), true)...)
}

/* ------------------------------------------------------------------ gyro_server_security_groups */

type serverSecurityGroupsResource struct{ pd *providerData }

func newServerSecurityGroupsResource() resource.Resource { return &serverSecurityGroupsResource{} }

type ssgModel struct {
	ID       types.String   `tfsdk:"id"`
	ServerID types.Int64    `tfsdk:"server_id"`
	Groups   []types.String `tfsdk:"security_group_ids"`
	Confirm  types.Bool     `tfsdk:"confirm_trial"`
}

type ssgJSON struct {
	Groups []struct {
		ID string `json:"id"`
	} `json:"security_groups"`
	Trial bool `json:"trial"`
}

func (r *serverSecurityGroupsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_security_groups"
}

func (r *serverSecurityGroupsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The exact list of security groups attached to a server (Network Firewall add-on), in evaluation order (max 5). " +
			"With groups attached and no gyro_firewall rules, inbound traffic that no group allows is denied. Destroy detaches all groups.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"server_id":          schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Server id."},
			"security_group_ids": schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "Group ids (sg-xxxxxxxx), max 5."},
			"confirm_trial": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "A change that could cut SSH/RDP access runs as a 5 minute trial; true (default) confirms it right after apply."},
		},
	}
}

func (r *serverSecurityGroupsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func ssgPath(m *ssgModel) string { return fmt.Sprintf("cloud/servers/%d/security-groups", sid(m.ServerID)) }

func (r *serverSecurityGroupsResource) set(ctx context.Context, m *ssgModel, ids []string) error {
	var out ssgJSON
	if err := sgRetry(ctx, func() error {
		_, e := r.pd.c.Do(ctx, http.MethodPut, ssgPath(m), nil, map[string]any{"security_groups": ids}, "", &out)
		return e
	}); err != nil {
		return err
	}
	if out.Trial && m.Confirm.ValueBool() {
		return sgRetry(ctx, func() error {
			_, e := r.pd.c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/firewall/policy/confirm", sid(m.ServerID)), nil, map[string]any{}, "", nil)
			return e
		})
	}
	return nil
}

func (r *serverSecurityGroupsResource) read(ctx context.Context, m *ssgModel) error {
	var out ssgJSON
	if _, err := r.pd.c.Do(ctx, http.MethodGet, ssgPath(m), nil, nil, "", &out); err != nil {
		return err
	}
	m.ID = types.StringValue(strconv.FormatInt(sid(m.ServerID), 10))
	m.Groups = make([]types.String, 0, len(out.Groups))
	for _, g := range out.Groups {
		m.Groups = append(m.Groups, types.StringValue(g.ID))
	}
	return nil
}

func (r *serverSecurityGroupsResource) save(ctx context.Context, m *ssgModel, d interface{ AddError(string, string) }) bool {
	if err := r.set(ctx, m, sgStrs(m.Groups)); err != nil {
		d.AddError("Attaching security groups failed", err.Error())
		return false
	}
	if err := r.read(ctx, m); err != nil {
		d.AddError("Reading attached security groups failed", err.Error())
		return false
	}
	return true
}

func (r *serverSecurityGroupsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m ssgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverSecurityGroupsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m ssgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	err := r.read(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "firewall_not_enabled") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading attached security groups failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverSecurityGroupsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m ssgModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverSecurityGroupsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m ssgModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if err := r.set(ctx, &m, []string{}); err != nil && !client.IsNotFound(err) && !client.HasCode(err, "firewall_not_enabled") {
		resp.Diagnostics.AddError("Detaching security groups failed", err.Error())
	}
}

func (r *serverSecurityGroupsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the server id, e.g. 1234")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("confirm_trial"), true)...)
}
