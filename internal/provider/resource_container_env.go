package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_container_environment (resource, API 2.30.0): order a prepaid Gyro Containers (ECS) environment, extra nodes, egress change
// approval window; destroy = DELETE /cloud/containers/{id} (same as the panel's Spending > Delete, hourly charges stop, data deleted).

var _ resource.ResourceWithImportState = (*containerEnvResource)(nil)

type containerEnvResource struct{ pd *providerData }

func newContainerEnvironmentResource() resource.Resource { return &containerEnvResource{} }

type containerEnvResModel struct {
	ID               types.String `tfsdk:"id"`
	Plan             types.String `tfsdk:"plan"`
	Name             types.String `tfsdk:"name"`
	ExtraNodes       types.Int64  `tfsdk:"extra_nodes"`
	EgressApprovalH  types.Int64  `tfsdk:"egress_approval_hours"`
	ServiceID        types.Int64  `tfsdk:"service_id"`
	Code             types.String `tfsdk:"code"`
	Namespace        types.String `tfsdk:"namespace"`
	Mode             types.String `tfsdk:"mode"`
	NodeCount        types.Int64  `tfsdk:"node_count"`
	EgressIPs        types.List   `tfsdk:"egress_ips"`
	PendingEgressIPs types.List   `tfsdk:"pending_egress_ips"`
}

type containerEnvDetail struct {
	Code         string   `json:"code"`
	Namespace    string   `json:"namespace"`
	Mode         string   `json:"mode"`
	EgressIPs    []string `json:"egress_ips"`
	Pending      []string `json:"pending_egress_ips"`
	ExtraNodes   *int64   `json:"extra_nodes"`
	NodeCount    int64    `json:"node_count"`
	HoldHours    *int64   `json:"egress_hold_hours"`
	BaseNodes    int64    `json:"base_nodes"`
	MaxExtra     int64    `json:"max_extra_nodes"`
	EgressChange *struct {
		State string `json:"state"`
	} `json:"egress_change"`
}

func (r *containerEnvResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_environment"
}

func (r *containerEnvResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepI := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A Gyro Containers (ECS) environment in Istanbul (tr-ist-1), prepaid: charged hourly from the account balance. List the plans with " +
			"`gyro container plans` (GET /cloud/containers/plans). extra_nodes adds nodes of the plan's size (same price as a plan node, at most 20 nodes in total); " +
			"lowering it drains the removed nodes first and is refused when the remaining nodes cannot hold the running services. egress_approval_hours (1-72) makes node " +
			"changes wait for your approval before the new egress IPs carry traffic (0 = off). DESTROY DELETES THE ENVIRONMENT at once with every service, node, registry " +
			"image and pipeline (hourly charges stop; cannot be undone) - use lifecycle { prevent_destroy = true } for environments you must keep. plan never triggers a replacement.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Service id."},
			"plan": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{noChangeString{"plan"}},
				Validators:  []validator.String{stringvalidator.RegexMatches(regexpCtrPlan, "a plan from GET /cloud/containers/plans, e.g. mini-1-1, pro or serverless")},
				Description: "mini-1-1 | small-1-2 | starter | standart | pro | serverless. A plan change is done in the panel; Terraform refuses it (it never deletes the environment for it)."},
			"name": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.LengthAtMost(60)}, Description: "Environment name (nickname). Changes in place; when not set, the name on the platform is kept (#3462)."},
			"extra_nodes": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.Between(0, 19)},
				Description: "Extra nodes on top of the plan (0 = plan only). Billed hourly at the plan's node price. Not for serverless."},
			"egress_approval_hours": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.Between(0, 72)},
				Description: "Approval window for egress IP changes in hours (0 = off, 24 recommended). Approve with `gyro container egress-change approve <id>`."},
			"service_id":         schema.Int64Attribute{Computed: true, PlanModifiers: keepI},
			"code":               schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Environment code (<service>-<code>.gyroapp.net)."},
			"namespace":          schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"mode":               schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "dedicated or serverless."},
			"node_count":         schema.Int64Attribute{Computed: true, Description: "Plan nodes + extra nodes."},
			"egress_ips":         schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Current egress IPv4 addresses."},
			"pending_egress_ips": schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "New egress IPs announced by a node change and not in use for sure yet."},
		},
	}
}

func (r *containerEnvResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *containerEnvResource) status(ctx context.Context, id int64) (string, string, error) {
	var s struct {
		Status   string `json:"status"`
		Nickname string `json:"nickname"`
	}
	_, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d", id), nil, nil, "", &s)
	return s.Status, s.Nickname, err
}

// planOf: plan code of the environment's product (GET /cloud/containers/plans: id = product id); "" when unknown (#3462: import left plan empty).
func (r *containerEnvResource) planOf(ctx context.Context, id int64) string {
	var sv struct {
		ProductID int64 `json:"product_id"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/services/%d", id), nil, nil, "", &sv); err != nil || sv.ProductID == 0 {
		return ""
	}
	product := sv.ProductID
	var l []struct {
		ID   int64  `json:"id"`
		Plan string `json:"plan"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, "cloud/containers/plans", nil, nil, "", &l); err != nil {
		var w struct {
			Plans []struct {
				ID   int64  `json:"id"`
				Plan string `json:"plan"`
			} `json:"plans"`
		}
		if _, err := r.pd.c.Do(ctx, http.MethodGet, "cloud/containers/plans", nil, nil, "", &w); err != nil {
			return ""
		}
		for _, p := range w.Plans {
			if p.ID == product {
				return p.Plan
			}
		}
		return ""
	}
	for _, p := range l {
		if p.ID == product {
			return p.Plan
		}
	}
	return ""
}

func (r *containerEnvResource) detail(ctx context.Context, id int64) (*containerEnvDetail, error) {
	var d containerEnvDetail
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d", id), nil, nil, "", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *containerEnvResource) apply(ctx context.Context, m *containerEnvResModel, d *containerEnvDetail) {
	m.Code, m.Namespace, m.Mode = types.StringValue(d.Code), types.StringValue(d.Namespace), types.StringValue(d.Mode)
	m.NodeCount = types.Int64Value(d.NodeCount)
	if d.ExtraNodes != nil {
		m.ExtraNodes = types.Int64Value(*d.ExtraNodes)
	}
	if d.HoldHours != nil {
		m.EgressApprovalH = types.Int64Value(*d.HoldHours)
	}
	if d.Mode == "serverless" {
		m.ExtraNodes, m.EgressApprovalH = types.Int64Value(0), types.Int64Value(0)
	}
	if d.EgressIPs == nil {
		d.EgressIPs = []string{}
	}
	if d.Pending == nil {
		d.Pending = []string{}
	}
	m.EgressIPs, _ = types.ListValueFrom(ctx, types.StringType, d.EgressIPs)
	m.PendingEgressIPs, _ = types.ListValueFrom(ctx, types.StringType, d.Pending)
}

func (r *containerEnvResource) setNodes(ctx context.Context, id, extra int64) error {
	_, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/nodes", id), nil, map[string]any{"extra_nodes": extra, "confirm": true}, client.NewIdempotencyKey(), nil)
	return err
}

func (r *containerEnvResource) setHold(ctx context.Context, id, h int64) error {
	_, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/egress-approval", id), nil, map[string]any{"hours": h, "confirm": true}, "", nil)
	return err
}

func (r *containerEnvResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerEnvResModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"plan": m.Plan.ValueString(), "confirm": true}
	if !m.Name.IsNull() && m.Name.ValueString() != "" {
		body["name"] = m.Name.ValueString()
	}
	var out struct {
		Environments []struct {
			ID int64 `json:"id"`
		} `json:"environments"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/containers", nil, body, client.NewIdempotencyKey(), &out); err != nil {
		resp.Diagnostics.AddError("Ordering the container environment failed", err.Error())
		return
	}
	if len(out.Environments) == 0 || out.Environments[0].ID == 0 {
		resp.Diagnostics.AddError("Ordering the container environment failed", "the API did not return the new environment id")
		return
	}
	id := out.Environments[0].ID
	m.ID, m.ServiceID = types.StringValue(strconv.FormatInt(id, 10)), types.Int64Value(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...) // ordered and charged: keep it in the state even if waiting fails
	deadline := time.Now().Add(r.pd.pollTimeout)
	var d *containerEnvDetail
	for {
		if st, _, err := r.status(ctx, id); err == nil && st == "active" {
			if x, err := r.detail(ctx, id); err == nil {
				d = x
				break
			}
		} else if err == nil && (st == "cancelled" || st == "suspended") {
			resp.Diagnostics.AddError("Container environment did not become active", "status: "+st)
			return
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddWarning("Container environment is still being set up", "It was ordered (id "+m.ID.ValueString()+"); run terraform apply again later.")
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Cancelled while waiting", ctx.Err().Error())
			return
		case <-time.After(r.pd.pollInterval):
		}
	}
	if h := m.EgressApprovalH.ValueInt64(); h > 0 && d.Mode != "serverless" {
		if err := r.setHold(ctx, id, h); err != nil {
			resp.Diagnostics.AddError("Setting egress_approval_hours failed", err.Error())
			return
		}
	}
	if x := m.ExtraNodes.ValueInt64(); x > 0 {
		if err := r.setNodes(ctx, id, x); err != nil {
			resp.Diagnostics.AddError("Adding extra nodes failed", err.Error())
			return
		}
	}
	if x, err := r.detail(ctx, id); err == nil {
		d = x
	}
	want := m
	r.apply(ctx, &m, d)
	m.ExtraNodes, m.EgressApprovalH = want.ExtraNodes, want.EgressApprovalH
	if m.Name.IsUnknown() || m.Name.IsNull() { // name not configured: the platform's (default nickname)
		m.Name = types.StringValue("")
		if _, nick, err := r.status(ctx, id); err == nil {
			m.Name = types.StringValue(nick)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerEnvResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerEnvResModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	st, nick, err := r.status(ctx, id)
	if client.IsNotFound(err) || (err == nil && (st == "cancelled" || st == "deleted")) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the container environment failed", err.Error())
		return
	}
	m.ServiceID = types.Int64Value(id)
	m.Name = types.StringValue(nick)
	if m.Plan.IsNull() || m.Plan.ValueString() == "" { // imported: the plan comes from the product of the service
		if p := r.planOf(ctx, id); p != "" {
			m.Plan = types.StringValue(p)
		}
	}
	if st == "active" {
		if d, err := r.detail(ctx, id); err == nil {
			r.apply(ctx, &m, d)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerEnvResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st containerEnvResModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID, m.ServiceID = st.ID, st.ServiceID
	id, _ := strconv.ParseInt(st.ID.ValueString(), 10, 64)
	var cfg containerEnvResModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if !cfg.Name.IsNull() && !cfg.Name.IsUnknown() && m.Name.ValueString() != st.Name.ValueString() { // #3462: no name in the config = keep the platform's
		if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/services/%d/label", id), nil, map[string]any{"nickname": m.Name.ValueString()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Renaming the container environment failed", err.Error())
			return
		}
	}
	if m.EgressApprovalH.ValueInt64() != st.EgressApprovalH.ValueInt64() {
		if err := r.setHold(ctx, id, m.EgressApprovalH.ValueInt64()); err != nil {
			resp.Diagnostics.AddError("Setting egress_approval_hours failed", err.Error())
			return
		}
	}
	if m.ExtraNodes.ValueInt64() != st.ExtraNodes.ValueInt64() {
		if err := r.setNodes(ctx, id, m.ExtraNodes.ValueInt64()); err != nil {
			resp.Diagnostics.AddError("Changing extra_nodes failed", err.Error())
			return
		}
	}
	want := m
	if d, err := r.detail(ctx, id); err == nil {
		r.apply(ctx, &m, d)
	}
	m.ExtraNodes, m.EgressApprovalH, m.Name = want.ExtraNodes, want.EgressApprovalH, want.Name
	if m.Name.IsUnknown() || m.Name.IsNull() {
		m.Name = st.Name
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerEnvResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerEnvResModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.Do(ctx, http.MethodDelete, "cloud/containers/"+m.ID.ValueString(), url.Values{"confirm": []string{"true"}}, map[string]any{"confirm": true}, client.NewIdempotencyKey(), nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting the container environment failed", err.Error())
	}
}

func (r *containerEnvResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the container environment (service) id, e.g. 12345")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

var regexpCtrPlan = regexp.MustCompile(`^(mini-1-1|small-1-2|starter|standart|pro|serverless|[a-z0-9-]{2,40})$`)
