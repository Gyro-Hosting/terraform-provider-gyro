package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*serverResource)(nil)
	_ resource.ResourceWithImportState = (*serverResource)(nil)
)

type serverResource struct{ pd *providerData }

func newServerResource() resource.Resource { return &serverResource{} }

type serverModel struct {
	ID           types.String  `tfsdk:"id"`
	ProductID    types.Int64   `tfsdk:"product_id"`
	Plan         types.String  `tfsdk:"plan"`
	Region       types.String  `tfsdk:"region"`
	ServerType   types.String  `tfsdk:"server_type"`
	Location     types.String  `tfsdk:"location"`
	Cycle        types.String  `tfsdk:"cycle"`
	Image        types.String  `tfsdk:"image"`
	Hostname     types.String  `tfsdk:"hostname"`
	SSHKeyIDs    types.List    `tfsdk:"ssh_key_ids"`
	UserData     types.String  `tfsdk:"user_data"`
	ConfirmOrder types.Bool    `tfsdk:"confirm_order"`
	PowerState   types.String  `tfsdk:"power_state"`
	AutoRenew    types.Bool    `tfsdk:"auto_renew"`
	Status       types.String  `tfsdk:"status"`
	IPv4         types.String  `tfsdk:"ipv4"`
	IPv6Network  types.String  `tfsdk:"ipv6_network"`
	Cores        types.Int64   `tfsdk:"cores"`
	MemoryGB     types.Float64 `tfsdk:"memory_gb"`
	DiskGB       types.Int64   `tfsdk:"disk_gb"`
	Price        types.Float64 `tfsdk:"price"`
	Currency     types.String  `tfsdk:"currency"`
	DueDate      types.String  `tfsdk:"due_date"`
	Immediate    types.Bool    `tfsdk:"delete_immediately"`
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *serverResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	}
	resp.Schema = schema.Schema{
		Description: "A Gyro cloud server. Creating one places an order that is paid from the account balance " +
			"(confirm_order must be true). Pick the product with product_id, or with plan + region (see gyro_products and gyro_locations). " +
			"Destroying it cancels the service (see delete_immediately); the machine and its data are wiped after the grace period.",
		Attributes: map[string]schema.Attribute{
			"id": str("Server id (service id in the client area)."),
			"product_id": schema.Int64Attribute{Optional: true, Computed: true,
				Description:   "Product id from gyro_products. Alternative to plan + region.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIfConfigured(), int64planmodifier.UseStateForUnknown()}},
			"plan": schema.StringAttribute{Optional: true, Computed: true,
				Description:   "Plan name from gyro_products, e.g. standart-2-4. Use together with region. Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"region": schema.StringAttribute{Optional: true, Computed: true,
				Description:   "Region from gyro_locations, e.g. eu-de-1 or tr-ist-1 (Istanbul; images from gyro_images with region = \"tr-ist-1\"). Use together with plan. Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"server_type": schema.StringAttribute{Optional: true, Computed: true,
				Description:        "Deprecated: legacy product code, kept so existing configurations and state keep working. Use plan.",
				DeprecationMessage: "server_type is deprecated and will be removed in a future major version. Use plan (see gyro_products) instead; switching does not replace the server.",
				PlanModifiers:      []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"location": schema.StringAttribute{Optional: true, Computed: true,
				Description:        "Deprecated: legacy location code, kept so existing configurations and state keep working. Use region.",
				DeprecationMessage: "location is deprecated and will be removed in a future major version. Use region (see gyro_locations) instead; switching does not replace the server.",
				PlanModifiers:      []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"cycle": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("monthly"),
				Description:   "Billing cycle: monthly (default), quarterly, semiannually, annually. Ignored for prepaid products (billed hourly from the account balance, e.g. region tr-ist-1).",
				Validators:    []validator.String{stringvalidator.OneOf("monthly", "quarterly", "semiannually", "annually")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"image": schema.StringAttribute{Required: true, Description: "Image slug from gyro_images, e.g. debian-12, or custom:<id> (gyro_server_template.image, tr-ist-1). Changing it replaces the server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"hostname": schema.StringAttribute{Optional: true, Computed: true, Description: "Host name. Changes are applied in place (must be a FQDN when renaming).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"ssh_key_ids": schema.ListAttribute{ElementType: types.Int64Type, Optional: true, Description: "gyro_ssh_key ids to install at creation. Global regions: max 1. tr-ist-1 (Istanbul): up to 10, installed for root at creation through cloud-init (API 2.14.0+).",
				PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()}},
			"user_data": schema.StringAttribute{Optional: true, Sensitive: true, Description: "cloud-init user data (max 32 KiB): #cloud-config or a #! script. Every region, tr-ist-1 included since API 2.14.0.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"confirm_order": schema.BoolAttribute{Required: true, Description: "Must be true: creating a server spends account balance."},
			"power_state": schema.StringAttribute{Optional: true, Computed: true, Description: "Desired power state: running or stopped.",
				Validators: []validator.String{stringvalidator.OneOf("running", "stopped")}},
			"auto_renew":   schema.BoolAttribute{Optional: true, Computed: true, Description: "Renew automatically from the account's payment source."},
			"status":       str("Service status (active, suspended, ...)."),
			"ipv4":         str("Primary IPv4 address."),
			"ipv6_network": str("IPv6 /64 network."),
			"cores":        schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"memory_gb":    schema.Float64Attribute{Computed: true},
			"disk_gb":      schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"price":        schema.Float64Attribute{Computed: true, Description: "Recurring price per cycle (VAT excluded)."},
			"currency":     str("Price currency."),
			"due_date":     schema.StringAttribute{Computed: true},
			"delete_immediately": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "destroy cancels at once (default; billing stops, server powered off and wiped after the grace period). false = file a cancellation request instead."},
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func sp(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// keepKnown fills an immutable attribute from the API unless state already holds a value for it.
func keepKnown(dst *types.String, api *string) {
	if dst.IsNull() || dst.IsUnknown() || dst.ValueString() == "" {
		*dst = sp(api)
	}
}

// apply copies API state into the model. Arguments that the API cannot echo back (image after a
// snapshot restore, user_data, ssh_key_ids, confirm_order) keep their configured values.
//
// plan, region, server_type and location are immutable (changing them replaces the server). The API accepts
// aliases for them (a region in location, a city name in region, ...) and answers with canonical values, so a
// value that is already known in state is kept: overwriting it with the canonical form would show up as a diff
// against the configuration and force a replacement. Empty values (create without them, import, state written
// by an older provider version) are filled from the API.
func apply(m *serverModel, s *client.Server) {
	m.ID = types.StringValue(strconv.FormatInt(s.ID, 10))
	m.ProductID = types.Int64Value(s.ProductID)
	keepKnown(&m.Plan, s.Plan)
	keepKnown(&m.Region, s.Region)
	if s.Region == nil && s.Live != nil {
		keepKnown(&m.Region, s.Live.Region)
	}
	keepKnown(&m.ServerType, s.ServerType)
	keepKnown(&m.Location, s.Location)
	if s.Cycle != "" && s.Cycle != "hourly" {
		m.Cycle = types.StringValue(s.Cycle)
	} else if s.Cycle == "hourly" && (m.Cycle.IsNull() || m.Cycle.IsUnknown()) {
		// prepaid (hourly from the balance, e.g. tr-ist-1): no billing cycle; keep the configured value, default on import
		m.Cycle = types.StringValue("monthly")
	}
	m.Hostname = sp(s.Hostname)
	m.AutoRenew = types.BoolValue(s.AutoRenew)
	m.Status = types.StringValue(s.Status)
	m.IPv4 = sp(s.IPv4)
	m.Price = types.Float64Value(s.Price.Amount)
	m.Currency = sp(s.Price.Currency)
	m.DueDate = sp(s.DueDate)
	m.IPv6Network = sp(s.IPv6)
	if s.Live != nil {
		m.PowerState = types.StringValue(s.Live.PowerState)
		m.Cores = types.Int64Value(s.Live.Cores)
		m.MemoryGB = types.Float64Value(s.Live.MemoryGB)
		m.DiskGB = types.Int64Value(s.Live.DiskGB)
		if s.Live.IPv6Network != nil {
			m.IPv6Network = sp(s.Live.IPv6Network)
		}
		if m.Image.IsNull() && s.Live.Image != nil {
			m.Image = types.StringValue(*s.Live.Image) // import
		}
	} else {
		if m.PowerState.IsUnknown() {
			m.PowerState = types.StringNull()
		}
		if m.Cores.IsUnknown() {
			m.Cores = types.Int64Null()
		}
		if m.MemoryGB.IsUnknown() {
			m.MemoryGB = types.Float64Null()
		}
		if m.DiskGB.IsUnknown() {
			m.DiskGB = types.Int64Null()
		}
	}
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !m.ConfirmOrder.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("confirm_order"), "Order not confirmed",
			"Creating a gyro_server places a paid order from your account balance. Set confirm_order = true to proceed.")
		return
	}
	in := client.CreateServerRequest{
		ProductID: m.ProductID.ValueInt64(), Plan: m.Plan.ValueString(), Region: m.Region.ValueString(),
		ServerType: m.ServerType.ValueString(), Location: m.Location.ValueString(),
		Cycle: m.Cycle.ValueString(), Image: m.Image.ValueString(), Hostname: m.Hostname.ValueString(),
		UserData: m.UserData.ValueString(), Confirm: true,
	}
	if in.ProductID == 0 && ((in.Plan == "" && in.ServerType == "") || (in.Region == "" && in.Location == "")) {
		resp.Diagnostics.AddError("Missing product", "Set product_id, or both plan and region.")
		return
	}
	if !m.SSHKeyIDs.IsNull() && !m.SSHKeyIDs.IsUnknown() {
		var ids []int64
		resp.Diagnostics.Append(m.SSHKeyIDs.ElementsAs(ctx, &ids, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		in.SSHKeys = ids
	}
	// One key per logical order: the client retries with it, the API replays instead of ordering twice.
	var res *client.CreateServerResult
	var err error
	if tid := client.TemplateImageID(in.Image); tid > 0 { // image = custom:<id> -> server from your own template (tr-ist-1, API 2.15.0)
		res, err = r.pd.c.CreateServerFromTemplate(ctx, tid, in, client.NewIdempotencyKey())
	} else {
		res, err = r.pd.c.CreateServer(ctx, in, client.NewIdempotencyKey())
	}
	if err != nil {
		resp.Diagnostics.AddError("Ordering the server failed", err.Error())
		return
	}
	if res.Server == nil || res.Server.ID == 0 {
		resp.Diagnostics.AddError("Ordering the server failed", "The API did not return a server id.")
		return
	}
	if res.Payment != nil && res.Payment.Status != "paid" {
		resp.Diagnostics.AddError("Payment failed", fmt.Sprintf("Order %v was created but payment status is %q: %s. Pay the invoice in the client area, then import server %d.",
			res.Order, res.Payment.Status, res.Payment.Error, res.Server.ID))
		return
	}
	id := res.Server.ID
	// Persist the id right away so a timeout never leaves an untracked paid server.
	partial := m
	apply(&partial, res.Server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)

	wctx, cancel := context.WithTimeout(ctx, r.pd.pollTimeout)
	defer cancel()
	s, err := r.pd.c.WaitReady(wctx, id, "", r.pd.pollInterval)
	if err != nil {
		resp.Diagnostics.AddError("Server did not become ready", err.Error())
		return
	}
	want := m.PowerState.ValueString()
	if !m.PowerState.IsUnknown() && want != "" && s.Live != nil && s.Live.PowerState != want {
		if s, err = r.setPower(ctx, id, want); err != nil {
			resp.Diagnostics.AddError("Setting power state failed", err.Error())
			return
		}
	}
	if !m.AutoRenew.IsUnknown() && !m.AutoRenew.IsNull() && m.AutoRenew.ValueBool() != s.AutoRenew {
		if s, err = r.pd.c.UpdateServer(ctx, id, map[string]any{"auto_renew": m.AutoRenew.ValueBool()}); err != nil {
			resp.Diagnostics.AddError("Setting auto_renew failed", err.Error())
			return
		}
	}
	apply(&m, s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverResource) setPower(ctx context.Context, id int64, want string) (*client.Server, error) {
	action := "poweron"
	if want == "stopped" {
		action = "shutdown"
	}
	if _, err := r.pd.c.Action(ctx, id, action, nil); err != nil && !client.HasCode(err, "tool_action_failed") {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, r.pd.pollTimeout)
	defer cancel()
	return r.pd.c.WaitReady(wctx, id, want, r.pd.pollInterval)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	s, err := r.pd.c.Server(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading server failed", err.Error())
		return
	}
	if s.Status == "cancelled" || (s.Cancellation != nil && *s.Cancellation != "") {
		resp.State.RemoveResource(ctx)
		return
	}
	apply(&m, s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	fields := map[string]any{}
	if !plan.Hostname.IsUnknown() && !plan.Hostname.IsNull() && plan.Hostname.ValueString() != state.Hostname.ValueString() {
		fields["hostname"] = plan.Hostname.ValueString()
	}
	if !plan.AutoRenew.IsUnknown() && !plan.AutoRenew.IsNull() && plan.AutoRenew.ValueBool() != state.AutoRenew.ValueBool() {
		fields["auto_renew"] = plan.AutoRenew.ValueBool()
	}
	s, err := r.pd.c.Server(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading server failed", err.Error())
		return
	}
	if len(fields) > 0 {
		if s, err = r.pd.c.UpdateServer(ctx, id, fields); err != nil {
			resp.Diagnostics.AddError("Updating server failed", err.Error())
			return
		}
	}
	if want := plan.PowerState.ValueString(); !plan.PowerState.IsUnknown() && want != "" && (s.Live == nil || s.Live.PowerState != want) {
		if s, err = r.setPower(ctx, id, want); err != nil {
			resp.Diagnostics.AddError("Setting power state failed", err.Error())
			return
		}
	}
	apply(&plan, s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteServer(ctx, id, m.Immediate.IsNull() || m.Immediate.ValueBool()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cancelling server failed", err.Error())
	}
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric server id, e.g. terraform import gyro_server.web 47466")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("confirm_order"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_immediately"), true)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ssh_key_ids"), types.ListNull(types.Int64Type))...)
}
