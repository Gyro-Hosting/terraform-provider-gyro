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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// API 2.18.0: customer projects. gyro_project manages one project; services join it through project_id on
// gyro_server / gyro_load_balancer or through gyro_service_project (any service).

var projectColors = []string{"gray", "blue", "green", "red", "orange", "yellow", "purple", "pink", "teal", "indigo"}
var projectIcons = []string{"folder", "briefcase", "building", "cart3", "cloud", "code-slash", "controller", "globe2", "heart", "house", "kanban",
	"lightning", "people", "rocket-takeoff", "shield-lock", "shop", "star", "tools", "truck", "box-seam", "mortarboard", "camera"}

var (
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
	_ resource.Resource                = (*serviceProjectResource)(nil)
	_ resource.ResourceWithImportState = (*serviceProjectResource)(nil)
)

type projectResource struct{ pd *providerData }

func newProjectResource() resource.Resource { return &projectResource{} }

type projectModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	Color        types.String `tfsdk:"color"`
	Icon         types.String `tfsdk:"icon"`
	ServiceCount types.Int64  `tfsdk:"service_count"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A project groups services in the client area and the API (e.g. one per business, client or environment). " +
			"Destroying it only deletes the project: its services go back to the default project (id 0). At most 50 projects per account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Project id."},
			"name": schema.StringAttribute{Required: true, Description: "1-40 characters: letter or digit first, then letters, digits, space and . _ - / + # & ( ). Unique per account; Genel/General/Default are reserved.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 40)}},
			"description": schema.StringAttribute{Optional: true, Description: "Up to 255 characters.", Validators: []validator.String{stringvalidator.LengthAtMost(255)}},
			"color": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("blue"), Description: "One of gray, blue, green, red, orange, yellow, purple, pink, teal, indigo.",
				Validators: []validator.String{stringvalidator.OneOf(projectColors...)}},
			"icon": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("folder"), Description: "Bootstrap Icons name shown in the client area (folder, rocket-takeoff, controller, ...).",
				Validators: []validator.String{stringvalidator.OneOf(projectIcons...)}},
			"service_count": schema.Int64Attribute{Computed: true, Description: "Live services in the project (active, suspended, in process, waiting).",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func applyProject(m *projectModel, p *client.Project) {
	m.ID = types.StringValue(strconv.FormatInt(p.ID, 10))
	m.Name = types.StringValue(p.Name)
	if p.Description != nil && *p.Description != "" {
		m.Description = types.StringValue(*p.Description)
	} else if !m.Description.IsNull() && m.Description.ValueString() == "" {
		// configured "" stays ""
	} else {
		m.Description = types.StringNull()
	}
	m.Color = types.StringValue(p.Color)
	m.Icon = types.StringValue(p.Icon)
	m.ServiceCount = types.Int64Value(p.ServiceCount)
}

func projectBody(m projectModel) map[string]any {
	b := map[string]any{"name": m.Name.ValueString(), "color": m.Color.ValueString(), "icon": m.Icon.ValueString()}
	if m.Description.IsNull() {
		b["description"] = nil
	} else {
		b["description"] = m.Description.ValueString()
	}
	return b
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.pd.c.CreateProject(ctx, projectBody(m))
	if err != nil {
		resp.Diagnostics.AddError("Creating project failed", err.Error())
		return
	}
	applyProject(&m, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil || id < 1 {
		resp.Diagnostics.AddError("Invalid id", "gyro_project id must be a positive number (the default project cannot be managed).")
		return
	}
	p, err := r.pd.c.Project(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading project failed", err.Error())
		return
	}
	applyProject(&m, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	p, err := r.pd.c.UpdateProject(ctx, id, projectBody(plan))
	if err != nil {
		resp.Diagnostics.AddError("Updating project failed", err.Error())
		return
	}
	applyProject(&plan, p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteProject(ctx, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting project failed", err.Error())
	}
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if v, err := strconv.ParseInt(req.ID, 10, 64); err != nil || v < 1 {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric project id, e.g. terraform import gyro_project.web 6")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

/* ------------------------------------------------------------------ gyro_service_project */

type serviceProjectResource struct{ pd *providerData }

func newServiceProjectResource() resource.Resource { return &serviceProjectResource{} }

type serviceProjectModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	ProjectID types.Int64  `tfsdk:"project_id"`
}

func (r *serviceProjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_project"
}

func (r *serviceProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Puts any existing service (DNS, storage, mail, containers, VDS, ...) into a project. Destroying it moves the service back to the default project. " +
			"For gyro_server and gyro_load_balancer prefer their project_id argument; do not manage the same service with both.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Same as service_id."},
			"service_id": schema.Int64Attribute{Required: true, Description: "Service id (gyro_services, client area). Changing it replaces the resource.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"project_id": schema.Int64Attribute{Required: true, Description: "gyro_project id, or 0 for the default project."},
		},
	}
}

func (r *serviceProjectResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceProjectResource) set(ctx context.Context, m *serviceProjectModel) error {
	if err := r.pd.c.SetServiceProject(ctx, m.ServiceID.ValueInt64(), m.ProjectID.ValueInt64()); err != nil {
		return err
	}
	m.ID = types.StringValue(strconv.FormatInt(m.ServiceID.ValueInt64(), 10))
	return nil
}

func (r *serviceProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serviceProjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting the service project failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serviceProjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	pid, err := r.pd.c.ServiceProjectID(ctx, sid)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the service project failed", err.Error())
		return
	}
	m.ServiceID = types.Int64Value(sid)
	if pid != nil {
		m.ProjectID = types.Int64Value(*pid)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serviceProjectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting the service project failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serviceProjectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.pd.c.SetServiceProject(ctx, m.ServiceID.ValueInt64(), 0); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Moving the service back to the default project failed", err.Error())
	}
}

func (r *serviceProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric service id, e.g. terraform import gyro_service_project.dns 12345")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

/* ------------------------------------------------------------------ project_id on gyro_server / gyro_load_balancer */

func projectIDSchema(kind string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, Computed: true,
		Description:   "Project of the " + kind + " (gyro_project id, 0 = default project). Set at order time; changing it later moves the " + kind + " in place.",
		PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}}
}

// syncProject moves the service when the planned project differs from the actual one and returns the actual project id.
func syncProject(ctx context.Context, c *client.Client, sid int64, planned types.Int64) (types.Int64, error) {
	cur, err := c.ServiceProjectID(ctx, sid)
	if err != nil {
		if planned.IsNull() || planned.IsUnknown() {
			return types.Int64Null(), nil // project_id not used: an API without projects is not an error
		}
		return types.Int64Null(), err
	}
	if cur == nil { // API without projects
		if planned.IsUnknown() {
			return types.Int64Null(), nil
		}
		return planned, nil
	}
	if !planned.IsNull() && !planned.IsUnknown() && planned.ValueInt64() != *cur {
		if err := c.SetServiceProject(ctx, sid, planned.ValueInt64()); err != nil {
			return types.Int64Null(), err
		}
		return planned, nil
	}
	return types.Int64Value(*cur), nil
}

func projectOrZero(v types.Int64) int64 {
	if v.IsNull() || v.IsUnknown() {
		return 0
	}
	return v.ValueInt64()
}
