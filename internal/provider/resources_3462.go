package provider

// #3462 tur 1 (R2 2026-11-15): gyro_server_addon (add-on purchase), gyro_server_agent (guest agent kept up to date),
// gyro_container_registry_policy (image registry clean-up policy).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

/* ================================================================== gyro_server_addon */

type serverAddonResource struct{ pd *providerData }

func newServerAddonResource() resource.Resource { return &serverAddonResource{} }

type serverAddonModel struct {
	ID         types.String `tfsdk:"id"`
	ServiceID  types.Int64  `tfsdk:"service_id"`
	AddonID    types.Int64  `tfsdk:"addon_id"`
	OptionID   types.Int64  `tfsdk:"option_id"`
	Addon      types.String `tfsdk:"addon"`
	Option     types.String `tfsdk:"option"`
	Quantity   types.Int64  `tfsdk:"quantity"`
	RecordID   types.Int64  `tfsdk:"record_id"`
	Name       types.String `tfsdk:"name"`
	OptionName types.String `tfsdk:"option_name"`
	Status     types.String `tfsdk:"status"`
	Billing    types.String `tfsdk:"billing"`
	Removable  types.Bool   `tfsdk:"removable"`
}

type addonAvail struct {
	AddonID int64  `json:"addon_id"`
	Name    string `json:"name"`
	Options []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"options"`
}

type addonOwned struct {
	ID        int64  `json:"id"`
	AddonID   int64  `json:"addon_id"`
	OptionID  int64  `json:"option_id"`
	Name      string `json:"name"`
	Option    string `json:"option"`
	Quantity  int64  `json:"quantity"`
	Status    string `json:"status"`
	Billing   string `json:"billing"`
	Removable bool   `json:"removable"`
}

func (r *serverAddonResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_addon"
}

func (r *serverAddonResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ri := []planmodifier.Int64{int64planmodifier.RequiresReplaceIfConfigured(), int64planmodifier.UseStateForUnknown()}
	rs := []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Buys an add-on for an existing service (POST /cloud/services/{id}/addons), e.g. the Network Firewall add-on that gyro_firewall and " +
			"gyro_security_group need. The resource waits until the add-on is active, so a server, its firewall add-on and its firewall rules can be created in " +
			"one apply: reference this resource from gyro_firewall / gyro_server_security_groups with depends_on. This spends money: on prepaid services (Gyro Cloud " +
			"Istanbul, containers) the add-on is charged hourly from the balance; on invoiced services an invoice is created and paid from the balance " +
			"(apply fails with the invoice link if the balance is not enough). Destroy removes prepaid add-ons at once (billing stops); invoiced add-ons cannot be " +
			"cancelled through the API - destroy then only forgets them (cancel in the panel). Pick the add-on by id (addon_id / option_id) or by name (addon / option, " +
			"case-insensitive, as listed by `gyro service addons available <id>` or GET /cloud/services/{id}/addons/available). Every argument is ForceNew.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<record_id>"},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Service (server) id."},
			"addon_id":   schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: ri, Description: "Add-on id (from the available list). Set this or addon."},
			"option_id":  schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: ri, Description: "Option id of the add-on. Left out: the option named by option, else the only / first option."},
			"addon":      schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: rs, Description: "Add-on name instead of addon_id (case-insensitive; a unique part of the name is enough)."},
			"option":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: rs, Description: "Option name instead of option_id (case-insensitive; a unique part is enough)."},
			"quantity": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), PlanModifiers: replaceI,
				Validators: []validator.Int64{int64validator.Between(1, 100)}, Description: "Quantity (1-100, default 1)."},
			"record_id":   schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Add-on record id on the service."},
			"name":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Add-on name as shown in the panel."},
			"option_name": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Option name as shown in the panel."},
			"status":      schema.StringAttribute{Computed: true, Description: "active, waiting, suspended ... (at the last read)."},
			"billing":     schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "prepaid (hourly from the balance, removable) or invoice."},
			"removable":   schema.BoolAttribute{Computed: true, Description: "true = destroy removes it through the API (prepaid add-ons)."},
		},
	}
}

func (r *serverAddonResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverAddonResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var a types.Int64
	var n types.String
	req.Config.GetAttribute(ctx, path.Root("addon_id"), &a)
	req.Config.GetAttribute(ctx, path.Root("addon"), &n)
	if a.IsNull() && n.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("addon_id"), "Add-on not chosen", "Set addon_id or addon (name).")
	}
	if !a.IsNull() && !n.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("addon"), "Both addon_id and addon set", "Set only one of addon_id / addon.")
	}
}

func matchName(have, want string) bool {
	return strings.Contains(strings.ToLower(have), strings.ToLower(strings.TrimSpace(want)))
}

// resolve picks addon_id / option_id from the available list (ids win; names are unique case-insensitive matches).
func (r *serverAddonResource) resolve(ctx context.Context, m *serverAddonModel) error {
	var av struct {
		Addons []addonAvail `json:"addons"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d/addons/available", m.ServiceID.ValueInt64()), nil, nil, "", &av); err != nil {
		return err
	}
	names := []string{}
	var pick *addonAvail
	for i := range av.Addons {
		a := &av.Addons[i]
		names = append(names, fmt.Sprintf("%q (addon_id %d)", a.Name, a.AddonID))
		if !m.AddonID.IsNull() && !m.AddonID.IsUnknown() {
			if a.AddonID == m.AddonID.ValueInt64() {
				pick = a
			}
		}
	}
	if pick == nil && (m.AddonID.IsNull() || m.AddonID.IsUnknown()) { // by name: an exact (case-insensitive) name wins, else one unique partial match
		want := strings.ToLower(strings.TrimSpace(m.Addon.ValueString()))
		var part []*addonAvail
		for i := range av.Addons {
			a := &av.Addons[i]
			if strings.ToLower(a.Name) == want {
				pick, part = a, nil
				break
			}
			if matchName(a.Name, want) {
				part = append(part, a)
			}
		}
		if pick == nil && len(part) > 1 {
			return fmt.Errorf("addon %q matches more than one add-on; write the full name or use addon_id. Available: %s", m.Addon.ValueString(), strings.Join(names, ", "))
		}
		if pick == nil && len(part) == 1 {
			pick = part[0]
		}
	}
	if pick == nil {
		return fmt.Errorf("the add-on is not available for service %d. Available: %s", m.ServiceID.ValueInt64(), strings.Join(names, ", "))
	}
	opt := int64(-1)
	opts := []string{}
	for _, o := range pick.Options {
		opts = append(opts, fmt.Sprintf("%q (option_id %d)", o.Name, o.ID))
		switch {
		case !m.OptionID.IsNull() && !m.OptionID.IsUnknown():
			if o.ID == m.OptionID.ValueInt64() {
				opt = o.ID
			}
		case !m.Option.IsNull() && !m.Option.IsUnknown() && m.Option.ValueString() != "":
			if matchName(o.Name, m.Option.ValueString()) {
				if opt >= 0 {
					return fmt.Errorf("option %q matches more than one option; use option_id. Options: %s", m.Option.ValueString(), strings.Join(opts, ", "))
				}
				opt = o.ID
			}
		default:
			if opt < 0 {
				opt = o.ID // the first option
			}
		}
	}
	if opt < 0 {
		return fmt.Errorf("option not found for add-on %q. Options: %s", pick.Name, strings.Join(opts, ", "))
	}
	m.AddonID, m.OptionID = types.Int64Value(pick.AddonID), types.Int64Value(opt)
	if m.Addon.IsNull() || m.Addon.IsUnknown() {
		m.Addon = types.StringValue(pick.Name)
	}
	if m.Option.IsNull() || m.Option.IsUnknown() {
		for _, o := range pick.Options {
			if o.ID == opt {
				m.Option = types.StringValue(o.Name)
			}
		}
	}
	return nil
}

func (r *serverAddonResource) owned(ctx context.Context, sid, rec int64) (*addonOwned, error) {
	var out struct {
		Addons []addonOwned `json:"addons"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d/addons", sid), nil, nil, "", &out); err != nil {
		return nil, err
	}
	for i := range out.Addons {
		if out.Addons[i].ID == rec {
			return &out.Addons[i], nil
		}
	}
	return nil, nil
}

func (r *serverAddonResource) fill(m *serverAddonModel, a *addonOwned) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%d", m.ServiceID.ValueInt64(), a.ID))
	m.RecordID = types.Int64Value(a.ID)
	m.AddonID, m.OptionID = types.Int64Value(a.AddonID), types.Int64Value(a.OptionID)
	if m.Addon.IsNull() || m.Addon.IsUnknown() {
		m.Addon = types.StringValue(a.Name)
	}
	if m.Option.IsNull() || m.Option.IsUnknown() {
		m.Option = types.StringValue(a.Option)
	}
	if a.Quantity > 0 {
		m.Quantity = types.Int64Value(a.Quantity)
	}
	m.Name, m.OptionName, m.Status = types.StringValue(a.Name), types.StringValue(a.Option), types.StringValue(a.Status)
	m.Billing, m.Removable = types.StringValue(a.Billing), types.BoolValue(a.Removable)
}

func (r *serverAddonResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverAddonModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.resolve(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Choosing the add-on failed", err.Error())
		return
	}
	sid := m.ServiceID.ValueInt64()
	var res struct {
		RecordID int64          `json:"addon_record_id"`
		Status   string         `json:"status"`
		Payment  map[string]any `json:"payment"`
	}
	body := map[string]any{"addon_id": m.AddonID.ValueInt64(), "option_id": m.OptionID.ValueInt64(), "quantity": m.Quantity.ValueInt64(), "confirm": true}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/services/%d/addons", sid), nil, body, client.NewIdempotencyKey(), &res); err != nil {
		resp.Diagnostics.AddError("Buying the add-on failed", err.Error())
		return
	}
	if res.RecordID < 1 {
		resp.Diagnostics.AddError("Buying the add-on failed", "the API did not return the add-on record id")
		return
	}
	m.ID, m.RecordID = types.StringValue(fmt.Sprintf("%d/%d", sid, res.RecordID)), types.Int64Value(res.RecordID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...) // keep it in state at once: it was paid for
	if res.Status == "awaiting_payment" {
		resp.Diagnostics.AddError("The add-on invoice is not paid", fmt.Sprintf("The balance did not cover the invoice: %v. Pay it in the panel, then run terraform apply again.", res.Payment))
		return
	}
	// wait until the add-on is active (firewall rules and other dependents need it)
	deadline := time.Now().Add(r.pd.pollTimeout)
	for {
		a, err := r.owned(ctx, sid, res.RecordID)
		if err != nil {
			resp.Diagnostics.AddError("Reading the add-on failed", err.Error())
			return
		}
		if a != nil {
			r.fill(&m, a)
			if a.Status == "active" {
				break
			}
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("The add-on is not active yet", fmt.Sprintf("Status %q after %s; run terraform apply again later.", m.Status.ValueString(), r.pd.pollTimeout))
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
		if !sleepCtx(ctx, r.pd.pollInterval) {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverAddonResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverAddonModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := r.owned(ctx, m.ServiceID.ValueInt64(), m.RecordID.ValueInt64())
	if client.IsNotFound(err) || (err == nil && a == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the add-on failed", err.Error())
		return
	}
	r.fill(&m, a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverAddonResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverAddonModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverAddonResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverAddonModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid, rec := m.ServiceID.ValueInt64(), m.RecordID.ValueInt64()
	_, err := r.pd.c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/services/%d/addons/%d", sid, rec), url.Values{"confirm": {"true"}}, map[string]any{"confirm": true}, "", nil)
	var ae *client.APIError
	switch {
	case err == nil || client.IsNotFound(err):
	case errors.As(err, &ae) && ae.Code == "not_removable":
		resp.Diagnostics.AddWarning("Invoiced add-on not cancelled",
			fmt.Sprintf("Add-on record %d of service %d is invoiced; the API cannot cancel it. It was removed from the Terraform state only - cancel it in the panel if you no longer need it.", rec, sid))
	default:
		resp.Diagnostics.AddError("Removing the add-on failed", err.Error())
	}
}

func (r *serverAddonResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	a, b, ok := splitImport(req.ID, "<service_id>/<record_id>, e.g. 17500/9123", resp)
	if !ok {
		return
	}
	sid, e1 := strconv.ParseInt(a, 10, 64)
	rec, e2 := strconv.ParseInt(b, 10, 64)
	if e1 != nil || e2 != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<record_id> (numbers); record ids: GET /cloud/services/{id}/addons.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), sid)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("record_id"), rec)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("quantity"), int64(1))...)
}

/* ================================================================== gyro_server_agent */

type serverAgentResource struct{ pd *providerData }

func newServerAgentResource() resource.Resource { return &serverAgentResource{} }

type serverAgentModel struct {
	ID              types.String `tfsdk:"id"`
	ServerID        types.Int64  `tfsdk:"server_id"`
	KeepUpdated     types.Bool   `tfsdk:"keep_updated"`
	Version         types.String `tfsdk:"version"`
	Latest          types.String `tfsdk:"latest"`
	UpdateAvailable types.Bool   `tfsdk:"update_available"`
	NetworkApplied  types.Bool   `tfsdk:"network_applied"`
	PrivateNetwork  types.Bool   `tfsdk:"private_network_supported"`
}

type agentView struct {
	Version         string `json:"version"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	AutoUpdate      bool   `json:"auto_update"`
	VPCSupported    bool   `json:"private_network_supported"`
	NetworkApplied  bool   `json:"network_applied"`
	Supported       bool   `json:"supported"`
	UpdateCommand   string `json:"update_command"`
}

func (r *serverAgentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_agent"
}

func (r *serverAgentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Keeps the Gyro guest agent of a Gyro Cloud Istanbul (tr-ist-1) server up to date (GET /cloud/servers/{id}/agent, POST .../agent/update). " +
			"Servers installed before private networking (agent 3) need agent 4 before gyro_server_nic / gyro_server_public_ip can turn the public IP off. " +
			"With keep_updated = true (default) every apply that finds a newer agent updates it in place (only the Gyro agent is replaced; data, applications and " +
			"settings are not touched, no restart) and waits until the new version reports. Very old agents (before 3) cannot update themselves: apply fails with " +
			"the one-line command to run on the server. Destroy only stops managing it.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Same as server_id."},
			"server_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Gyro Cloud Istanbul server id (gyro_server.id)."},
			"keep_updated": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "true = update the agent when a newer version is available (the next plan shows it as an in-place change). false = only report."},
			"version":                   schema.StringAttribute{Computed: true, Description: "Agent version on the server."},
			"latest":                    schema.StringAttribute{Computed: true, Description: "Newest agent version."},
			"update_available":          schema.BoolAttribute{Computed: true, Description: "A newer agent exists."},
			"network_applied":           schema.BoolAttribute{Computed: true, Description: "The private network settings are applied on the server."},
			"private_network_supported": schema.BoolAttribute{Computed: true, Description: "The agent supports private networks (VPC, public IP off)."},
		},
	}
}

func (r *serverAgentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverAgentResource) get(ctx context.Context, sid int64) (*agentView, error) {
	var v agentView
	_, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d/agent", sid), nil, nil, "", &v)
	return &v, err
}

func (r *serverAgentResource) fill(m *serverAgentModel, v *agentView) {
	m.ID = types.StringValue(strconv.FormatInt(m.ServerID.ValueInt64(), 10))
	m.Version, m.Latest, m.UpdateAvailable = types.StringValue(v.Version), types.StringValue(v.Latest), types.BoolValue(v.UpdateAvailable)
	m.NetworkApplied, m.PrivateNetwork = types.BoolValue(v.NetworkApplied), types.BoolValue(v.VPCSupported)
}

// ensure updates the agent when keep_updated and a newer version exists, then waits for it.
func (r *serverAgentResource) ensure(ctx context.Context, m *serverAgentModel) error {
	sid := m.ServerID.ValueInt64()
	v, err := r.get(ctx, sid)
	if err != nil {
		return err
	}
	if !m.KeepUpdated.ValueBool() || !v.UpdateAvailable {
		r.fill(m, v)
		return nil
	}
	_, err = r.pd.c.Do(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/agent/update", sid), nil, map[string]any{}, "", nil)
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Code == "already_latest" {
		err = nil
	}
	if errors.As(err, &ae) && (ae.Code == "manual_update_required" || ae.Code == "not_supported") {
		return fmt.Errorf("the agent of server %d (version %q) cannot update itself; run this once on the server as root, then apply again:\n%s", sid, v.Version, v.UpdateCommand)
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(r.pd.pollTimeout)
	for {
		if !sleepCtx(ctx, r.pd.pollInterval) {
			return ctx.Err()
		}
		if v, err = r.get(ctx, sid); err == nil && !v.UpdateAvailable {
			r.fill(m, v)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the agent update did not finish in %s (version %q); run terraform apply again", r.pd.pollTimeout, v.Version)
		}
	}
}

func (r *serverAgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverAgentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ensure(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Updating the guest agent failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Read: an outdated agent with keep_updated = true is stored as keep_updated = false, so the plan shows the in-place update.
func (r *serverAgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverAgentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.ServerID.IsNull() {
		n, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
		m.ServerID = types.Int64Value(n)
	}
	if m.KeepUpdated.IsNull() || m.KeepUpdated.IsUnknown() {
		m.KeepUpdated = types.BoolValue(true)
	}
	v, err := r.get(ctx, m.ServerID.ValueInt64())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the guest agent failed", err.Error())
		return
	}
	r.fill(&m, v)
	if m.KeepUpdated.ValueBool() && v.UpdateAvailable {
		m.KeepUpdated = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverAgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverAgentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ensure(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Updating the guest agent failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverAgentResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *serverAgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric server id.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("keep_updated"), true)...)
}

/* ================================================================== gyro_container_registry_policy */

type registryPolicyResource struct{ pd *providerData }

func newRegistryPolicyResource() resource.Resource { return &registryPolicyResource{} }

type registryPolicyModel struct {
	ID           types.String `tfsdk:"id"`
	ServiceID    types.Int64  `tfsdk:"service_id"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	Keep         types.Int64  `tfsdk:"keep"`
	UntaggedDays types.Int64  `tfsdk:"untagged_days"`
}

func (r *registryPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_registry_policy"
}

func (r *registryPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Clean-up policy of the private image registry of a container environment (PUT /cloud/containers/{id}/registry/policy). " +
			"Images used by the last `keep` revisions of every service are always kept; other tags older than `untagged_days` are deleted. " +
			"Destroy puts the platform default back (enabled, keep 10, 7 days).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Same as service_id."},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id."},
			"enabled":    schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Clean up automatically (default true)."},
			"keep": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(10), Validators: []validator.Int64{int64validator.Between(1, 50)},
				Description: "Images of the last N revisions of each service are always kept (1-50, default 10)."},
			"untagged_days": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(7), Validators: []validator.Int64{int64validator.Between(1, 90)},
				Description: "Unused tags older than this many days are deleted (1-90, default 7)."},
		},
	}
}

func (r *registryPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

type regPolicyView struct {
	Enabled      bool  `json:"enabled"`
	Keep         int64 `json:"keep"`
	UntaggedDays int64 `json:"untagged_days"`
}

func (r *registryPolicyResource) put(ctx context.Context, sid int64, en bool, keepN, days int64) error {
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/registry/policy", sid), map[string]any{"enabled": en, "keep": keepN, "untagged_days": days})
	return err
}

func (r *registryPolicyResource) read(ctx context.Context, m *registryPolicyModel) error {
	var v struct {
		Policy *regPolicyView `json:"policy"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d/registry", m.ServiceID.ValueInt64()), nil, nil, "", &v); err != nil {
		return err
	}
	if v.Policy == nil {
		return errors.New("the registry answer has no policy")
	}
	m.ID = types.StringValue(strconv.FormatInt(m.ServiceID.ValueInt64(), 10))
	m.Enabled, m.Keep, m.UntaggedDays = types.BoolValue(v.Policy.Enabled), types.Int64Value(v.Policy.Keep), types.Int64Value(v.Policy.UntaggedDays)
	return nil
}

func (r *registryPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m registryPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, m.ServiceID.ValueInt64(), m.Enabled.ValueBool(), m.Keep.ValueInt64(), m.UntaggedDays.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Setting the registry policy failed", err.Error())
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading the registry policy failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *registryPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m registryPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, &m); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading the registry policy failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *registryPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m registryPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, m.ServiceID.ValueInt64(), m.Enabled.ValueBool(), m.Keep.ValueInt64(), m.UntaggedDays.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Setting the registry policy failed", err.Error())
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading the registry policy failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *registryPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m registryPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, m.ServiceID.ValueInt64(), true, 10, 7); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Resetting the registry policy failed", err.Error())
	}
}

func (r *registryPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric container environment (service) id.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

/* ================================================================== gyro_server_snapshot_schedule (#3462 madde 10, API 2.40.0) */

type snapScheduleResource struct{ pd *providerData }

func newSnapScheduleResource() resource.Resource { return &snapScheduleResource{} }

type snapScheduleModel struct {
	ID            types.String `tfsdk:"id"`
	ServerID      types.Int64  `tfsdk:"server_id"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	Frequency     types.String `tfsdk:"frequency"`
	Weekday       types.String `tfsdk:"weekday"`
	Hour          types.Int64  `tfsdk:"hour"`
	Minute        types.Int64  `tfsdk:"minute"`
	Keep          types.Int64  `tfsdk:"keep"`
	SnapshotLimit types.Int64  `tfsdk:"snapshot_limit"`
	NextRunAt     types.String `tfsdk:"next_run_at"`
	LastRunAt     types.String `tfsdk:"last_run_at"`
	LastStatus    types.String `tfsdk:"last_status"`
	LastError     types.String `tfsdk:"last_error"`
}

type snapScheduleView struct {
	Configured    bool    `json:"configured"`
	Enabled       bool    `json:"enabled"`
	Frequency     *string `json:"frequency"`
	Weekday       *string `json:"weekday"`
	Hour          *int64  `json:"hour"`
	Minute        *int64  `json:"minute"`
	Keep          *int64  `json:"keep"`
	SnapshotLimit int64   `json:"snapshot_limit"`
	NextRunAt     *string `json:"next_run_at"`
	LastRunAt     *string `json:"last_run_at"`
	LastStatus    *string `json:"last_status"`
	LastError     *string `json:"last_error"`
}

var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func (r *snapScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_snapshot_schedule"
}

func (r *snapScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Scheduled snapshots of a Gyro Cloud Istanbul (tr-ist-1) server (GET|PUT|DELETE /cloud/servers/{id}/snapshot-schedule, Gyro Public API 2.40.0). " +
			"Every day or every week at hour:minute Türkiye time a snapshot named auto-YYYYMMDD-HHMM is taken; the oldest automatic snapshots are deleted so at most " +
			"`keep` stay. Manual snapshots are never deleted but count toward the server's snapshot limit (when it is full the run is skipped, see last_status). " +
			"Snapshots above the free allowance of a prepaid server are billed hourly. While the server has snapshots an extra disk (gyro_server_disk) cannot be " +
			"grown: set enabled = false and delete the snapshots first. Destroy removes the schedule; existing snapshots stay.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Same as server_id."},
			"server_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Gyro Cloud Istanbul server id (gyro_server.id)."},
			"enabled":   schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "false pauses the schedule (kept)."},
			"frequency": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("daily"),
				Validators: []validator.String{stringvalidator.OneOf("daily", "weekly")}, Description: "daily (default) or weekly."},
			"weekday": schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{stringvalidator.OneOf(weekdays...)},
				Description: "Day for frequency = weekly: sunday ... saturday (required then)."},
			"hour": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3), Validators: []validator.Int64{int64validator.Between(0, 23)},
				Description: "Hour, Türkiye time (Europe/Istanbul), default 3."},
			"minute": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.Between(0, 59)},
				Description: "Minute, default 0."},
			"keep": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(7), Validators: []validator.Int64{int64validator.Between(1, 20)},
				Description: "Automatic snapshots to keep (1 to the server's snapshot limit, default 7)."},
			"snapshot_limit": schema.Int64Attribute{Computed: true, Description: "The server's snapshot limit (manual + automatic)."},
			"next_run_at":    schema.StringAttribute{Computed: true, Description: "Next run (RFC 3339, UTC) at the last read; empty when paused."},
			"last_run_at":    schema.StringAttribute{Computed: true, Description: "Last run (RFC 3339, UTC) at the last read."},
			"last_status":    schema.StringAttribute{Computed: true, Description: "queued (snapshot started) or skipped (see last_error) at the last read."},
			"last_error":     schema.StringAttribute{Computed: true, Description: "Why the last run was skipped (e.g. snapshot_limit)."},
		},
	}
}

func (r *snapScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *snapScheduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var f, w types.String
	req.Config.GetAttribute(ctx, path.Root("frequency"), &f)
	req.Config.GetAttribute(ctx, path.Root("weekday"), &w)
	if !f.IsUnknown() && f.ValueString() == "weekly" && w.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("weekday"), "weekday required", "Set weekday (sunday ... saturday) for frequency = \"weekly\".")
	}
	if !f.IsUnknown() && f.ValueString() != "weekly" && !w.IsNull() && !w.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("weekday"), "weekday only for weekly", "weekday is used only with frequency = \"weekly\".")
	}
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r *snapScheduleResource) fill(m *snapScheduleModel, v *snapScheduleView) {
	m.ID = types.StringValue(strconv.FormatInt(m.ServerID.ValueInt64(), 10))
	m.Enabled, m.Frequency = types.BoolValue(v.Enabled), types.StringValue(strOr(v.Frequency))
	if v.Weekday != nil && *v.Weekday != "" {
		m.Weekday = types.StringValue(*v.Weekday)
	} else {
		m.Weekday = types.StringNull()
	}
	if v.Hour != nil {
		m.Hour = types.Int64Value(*v.Hour)
	}
	if v.Minute != nil {
		m.Minute = types.Int64Value(*v.Minute)
	}
	if v.Keep != nil {
		m.Keep = types.Int64Value(*v.Keep)
	}
	m.SnapshotLimit = types.Int64Value(v.SnapshotLimit)
	m.NextRunAt, m.LastRunAt = types.StringValue(strOr(v.NextRunAt)), types.StringValue(strOr(v.LastRunAt))
	m.LastStatus, m.LastError = types.StringValue(strOr(v.LastStatus)), types.StringValue(strOr(v.LastError))
}

func (r *snapScheduleResource) put(ctx context.Context, m *snapScheduleModel) error {
	body := map[string]any{"enabled": m.Enabled.ValueBool(), "frequency": m.Frequency.ValueString(), "hour": m.Hour.ValueInt64(),
		"minute": m.Minute.ValueInt64(), "keep": m.Keep.ValueInt64()}
	if m.Frequency.ValueString() == "weekly" && !m.Weekday.IsNull() && !m.Weekday.IsUnknown() {
		body["weekday"] = m.Weekday.ValueString()
	}
	var v snapScheduleView
	if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/servers/%d/snapshot-schedule", m.ServerID.ValueInt64()), nil, body, "", &v); err != nil {
		return err
	}
	r.fill(m, &v)
	return nil
}

func (r *snapScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m snapScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting the snapshot schedule failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *snapScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m snapScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.ServerID.IsNull() {
		n, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
		m.ServerID = types.Int64Value(n)
	}
	var v snapScheduleView
	_, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d/snapshot-schedule", m.ServerID.ValueInt64()), nil, nil, "", &v)
	if client.IsNotFound(err) || (err == nil && !v.Configured) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the snapshot schedule failed", err.Error())
		return
	}
	r.fill(&m, &v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *snapScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m snapScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Changing the snapshot schedule failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *snapScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m snapScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, fmt.Sprintf("cloud/servers/%d/snapshot-schedule", m.ServerID.ValueInt64()), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the snapshot schedule failed", err.Error())
	}
}

func (r *snapScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric server id.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
