package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*serverTemplateResource)(nil)
	_ resource.ResourceWithImportState = (*serverTemplateResource)(nil)
)

// gyro_server_template: a custom template captured from one of your tr-ist-1 servers (Gyro Public API 2.15.0).
type serverTemplateResource struct{ pd *providerData }

func newServerTemplateResource() resource.Resource { return &serverTemplateResource{} }

type serverTemplateModel struct {
	ID          types.String  `tfsdk:"id"`
	ServerID    types.Int64   `tfsdk:"server_id"`
	Name        types.String  `tfsdk:"name"`
	Description types.String  `tfsdk:"description"`
	Mode        types.String  `tfsdk:"mode"`
	Image       types.String  `tfsdk:"image"`
	Status      types.String  `tfsdk:"status"`
	SizeGB      types.Float64 `tfsdk:"size_gb"`
	MinDiskGB   types.Int64   `tfsdk:"min_disk_gb"`
	BaseImage   types.String  `tfsdk:"base_image"`
	CreatedAt   types.String  `tfsdk:"created_at"`
}

func (r *serverTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_template"
}

func (r *serverTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A custom template captured from one of your tr-ist-1 (Istanbul) servers. Use its `image` (`custom:<id>`) on gyro_server to create " +
			"servers from it. Only the root disk is copied; new servers get a new host name, IP, root password, SSH host keys and machine id. " +
			"Billed 0.05 EUR per GB-month of the space the template really uses (prepaid, hourly); destroying the template stops the charge. " +
			"At most 10 per account; images cannot be uploaded from outside.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"server_id": schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Description: "tr-ist-1 server to capture. Changing it captures a new template."},
			"name":        schema.StringAttribute{Required: true, Description: "1-60 characters (letters, digits, space . _ -)."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Up to 200 characters."},
			"mode": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("snapshot"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Description:   "snapshot (default: the server keeps running, file system frozen briefly) or shutdown (stopped while copied, then started again)."},
			"image":       schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Image slug for gyro_server: custom:<id>."},
			"status":      schema.StringAttribute{Computed: true, Description: "creating, ready, error, deleting or suspended (template storage suspended for balance)."},
			"size_gb":     schema.Float64Attribute{Computed: true, Description: "Space the template really uses (billed)."},
			"min_disk_gb": schema.Int64Attribute{Computed: true, Description: "Smallest disk a server from this template can have."},
			"base_image":  schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Catalogue image the source server was installed from."},
			"created_at":  schema.StringAttribute{Computed: true, PlanModifiers: keep},
		},
	}
}

func (r *serverTemplateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func applyTemplate(m *serverTemplateModel, t *client.Template) {
	m.ID = types.StringValue(strconv.FormatInt(t.ID, 10))
	m.Name = types.StringValue(t.Name)
	m.Description = types.StringValue(t.Description)
	m.Image = types.StringValue(t.Image)
	m.Status = types.StringValue(t.Status)
	m.SizeGB = types.Float64Value(t.SizeGB)
	m.MinDiskGB = types.Int64Value(t.MinDiskGB)
	m.BaseImage = types.StringValue(t.BaseImage)
	if t.Mode != "" {
		m.Mode = types.StringValue(t.Mode)
	}
	if t.CreatedAt != nil {
		m.CreatedAt = types.StringValue(*t.CreatedAt)
	} else if m.CreatedAt.IsUnknown() {
		m.CreatedAt = types.StringValue("")
	}
	if t.SourceServerID != nil && *t.SourceServerID > 0 {
		m.ServerID = types.Int64Value(*t.SourceServerID)
	}
}

// waitTemplate polls until the template leaves "creating" (ready = done, error = failure).
func (r *serverTemplateResource) waitTemplate(ctx context.Context, id int64) (*client.Template, error) {
	ctx, cancel := context.WithTimeout(ctx, r.pd.pollTimeout)
	defer cancel()
	for {
		t, err := r.pd.c.Template(ctx, id)
		if err != nil {
			return nil, err
		}
		switch t.Status {
		case "ready", "suspended":
			return t, nil
		case "error":
			return t, fmt.Errorf("template %d could not be created; destroy it and try again", id)
		}
		select {
		case <-ctx.Done():
			return t, fmt.Errorf("template %d still %q: %w", id, t.Status, ctx.Err())
		case <-time.After(r.pd.pollInterval):
		}
	}
}

func (r *serverTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.pd.c.CreateTemplate(ctx, m.ServerID.ValueInt64(), m.Name.ValueString(), m.Description.ValueString(), m.Mode.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating the template failed", err.Error())
		return
	}
	// keep the id even if the wait fails, so the template (it is billed) is never untracked
	partial := m
	applyTemplate(&partial, t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
	ready, err := r.waitTemplate(ctx, t.ID)
	if ready != nil {
		applyTemplate(&m, ready)
	}
	if err != nil {
		resp.Diagnostics.AddError("Template did not become ready", err.Error())
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	t, err := r.pd.c.Template(ctx, id)
	if client.IsNotFound(err) || (err == nil && t.Status == "deleting") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the template failed", err.Error())
		return
	}
	applyTemplate(&m, t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	t, err := r.pd.c.UpdateTemplate(ctx, id, m.Name.ValueString(), m.Description.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Updating the template failed", err.Error())
		return
	}
	applyTemplate(&m, t)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteTemplate(ctx, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting the template failed", err.Error())
	}
}

func (r *serverTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
