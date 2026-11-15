package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// API 2.2.0+: customer nickname + colour tags of any service (GET/PUT cloud/services/{id}/label).
// The nickname is the big title of the service in the client area; the product name and #id stay below it.

var labelColors = []string{"gray", "blue", "green", "red", "orange", "yellow", "purple", "pink", "teal", "indigo"}

var (
	_ resource.Resource                = (*serviceLabelResource)(nil)
	_ resource.ResourceWithImportState = (*serviceLabelResource)(nil)
)

type serviceLabelResource struct{ pd *providerData }

func newServiceLabelResource() resource.Resource { return &serviceLabelResource{} }

type labelTagModel struct {
	Name  types.String `tfsdk:"name"`
	Color types.String `tfsdk:"color"`
}

type serviceLabelModel struct {
	ID        types.String    `tfsdk:"id"`
	ServiceID types.Int64     `tfsdk:"service_id"`
	Nickname  types.String    `tfsdk:"nickname"`
	Tags      []labelTagModel `tfsdk:"tags"`
	// ManageNickname (#3462): false = this resource owns only the tags; the nickname belongs to e.g. gyro_container_environment.name / gyro_storage.name
	ManageNickname types.Bool `tfsdk:"manage_nickname"`
}

func (m *serviceLabelModel) ownsNick() bool {
	return m.ManageNickname.IsNull() || m.ManageNickname.IsUnknown() || m.ManageNickname.ValueBool()
}

func (r *serviceLabelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_label"
}

func (r *serviceLabelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Nickname and colour tags of any existing service (cloud server, container environment, VDS, storage, DNS, mail, ...). " +
			"The nickname is shown as the service title in the client area (the product name and service number stay below it) and is returned by gyro_services. " +
			"By default this resource owns the whole label: destroying it clears the nickname and all tags. With manage_nickname = false it owns only the tags " +
			"(use this next to gyro_container_environment.name or gyro_storage.name, which set the nickname). Only the service owner sees the label (staff see it read only).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Same as service_id."},
			"service_id": schema.Int64Attribute{Required: true, Description: "Service id (gyro_services, gyro_server.id, client area #number). Changing it replaces the resource.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"nickname": schema.StringAttribute{Optional: true, Description: "1-60 characters, e.g. \"api-gateway\". Omit to keep the service without a nickname.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 60)}},
			"manage_nickname": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "true (default) = the resource owns nickname and tags. false = it owns only the tags: the nickname is never sent, read or cleared " +
					"(set it with gyro_container_environment.name, gyro_storage.name or the panel); nickname must then be left out."},
			"tags": schema.ListNestedAttribute{Optional: true, Description: "Up to 10 tags, kept in this order. Names are case-insensitively unique.",
				Validators: []validator.List{listvalidator.SizeAtMost(10)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{Required: true, Description: "1-24 characters: letter or digit first, then letters, digits, space and . _ - / + # &.",
						Validators: []validator.String{stringvalidator.LengthBetween(1, 24)}},
					"color": schema.StringAttribute{Optional: true, Computed: true, Description: "One of " + fmt.Sprint(labelColors) + "; omitted = a stable default chosen by the API.",
						Validators: []validator.String{stringvalidator.OneOf(labelColors...)}},
				}}},
		},
	}
}

func (r *serviceLabelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceLabelResource) put(ctx context.Context, m *serviceLabelModel) error {
	if !m.ownsNick() {
		l, err := r.pd.c.SetServiceLabelTags(ctx, m.ServiceID.ValueInt64(), labelTags(m))
		if err != nil {
			return err
		}
		applyLabel(m, l)
		return nil
	}
	var nick *string
	if !m.Nickname.IsNull() && !m.Nickname.IsUnknown() {
		v := m.Nickname.ValueString()
		nick = &v
	}
	l, err := r.pd.c.SetServiceLabel(ctx, m.ServiceID.ValueInt64(), nick, labelTags(m))
	if err != nil {
		return err
	}
	applyLabel(m, l)
	return nil
}

func labelTags(m *serviceLabelModel) []client.LabelTag {
	tags := make([]client.LabelTag, 0, len(m.Tags))
	for _, t := range m.Tags {
		c := ""
		if !t.Color.IsNull() && !t.Color.IsUnknown() {
			c = t.Color.ValueString()
		}
		tags = append(tags, client.LabelTag{Name: t.Name.ValueString(), Color: c})
	}
	return tags
}

// applyLabel copies the API answer into the model. Tags that were configured keep the configured name spelling.
func applyLabel(m *serviceLabelModel, l *client.ServiceLabel) {
	m.ID = types.StringValue(strconv.FormatInt(m.ServiceID.ValueInt64(), 10))
	if m.ManageNickname.IsNull() || m.ManageNickname.IsUnknown() {
		m.ManageNickname = types.BoolValue(true)
	}
	if !m.ownsNick() {
		m.Nickname = types.StringNull() // not ours: never read
	} else if l.Nickname != nil && *l.Nickname != "" {
		m.Nickname = types.StringValue(*l.Nickname)
	} else {
		m.Nickname = types.StringNull()
	}
	if len(l.Tags) == 0 {
		if m.Tags != nil && len(m.Tags) == 0 {
			m.Tags = []labelTagModel{} // configured tags = [] stays []
		} else {
			m.Tags = nil
		}
		return
	}
	out := make([]labelTagModel, 0, len(l.Tags))
	for _, t := range l.Tags {
		out = append(out, labelTagModel{Name: types.StringValue(t.Name), Color: types.StringValue(t.Color)})
	}
	m.Tags = out
}

func (r *serviceLabelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serviceLabelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting the service label failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceLabelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serviceLabelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil || sid < 1 {
		resp.Diagnostics.AddError("Invalid id", "gyro_service_label id must be the numeric service id.")
		return
	}
	m.ServiceID = types.Int64Value(sid)
	l, err := r.pd.c.ServiceLabel(ctx, sid)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the service label failed", err.Error())
		return
	}
	applyLabel(&m, l)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceLabelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serviceLabelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting the service label failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serviceLabelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serviceLabelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var err error
	if m.ownsNick() {
		_, err = r.pd.c.SetServiceLabel(ctx, m.ServiceID.ValueInt64(), nil, nil)
	} else {
		_, err = r.pd.c.SetServiceLabelTags(ctx, m.ServiceID.ValueInt64(), nil) // only the tags; the nickname stays
	}
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Clearing the service label failed", err.Error())
	}
}

func (r *serviceLabelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the numeric service id, e.g. terraform import gyro_service_label.api 12345")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ValidateConfig: nickname cannot be set when manage_nickname = false.
func (r *serviceLabelResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var mn types.Bool
	var nick types.String
	req.Config.GetAttribute(ctx, path.Root("manage_nickname"), &mn)
	req.Config.GetAttribute(ctx, path.Root("nickname"), &nick)
	if !mn.IsNull() && !mn.IsUnknown() && !mn.ValueBool() && !nick.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("nickname"), "nickname with manage_nickname = false",
			"With manage_nickname = false this resource manages only the tags; set the nickname with gyro_container_environment.name, gyro_storage.name or the panel, and leave nickname out here.")
	}
}
