package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// One additional disk of a tr-ist-1 server. API (GyroAPI >= 2.19.0):
// GET/POST /cloud/servers/{id}/disks, PUT/DELETE /cloud/servers/{id}/disks/{disk}?confirm=true.

var _ resource.ResourceWithImportState = (*serverDiskResource)(nil)

type serverDiskResource struct{ pd *providerData }

func newServerDiskResource() resource.Resource { return &serverDiskResource{} }

type serverDiskModel struct {
	ID         types.String  `tfsdk:"id"`
	ServerID   types.Int64   `tfsdk:"server_id"`
	SizeGB     types.Int64   `tfsdk:"size_gb"`
	Class      types.String  `tfsdk:"class"`
	DiskID     types.Int64   `tfsdk:"disk_id"`
	Name       types.String  `tfsdk:"name"`
	MonthlyEUR types.Float64 `tfsdk:"monthly_eur"`
	GBMonthEUR types.Float64 `tfsdk:"per_gb_monthly_eur"`
	MinHours   types.Int64   `tfsdk:"minimum_hours"`
}

type serverDiskList struct {
	Disks []struct {
		ID         int64   `json:"id"`
		Name       string  `json:"name"`
		Class      string  `json:"class"`
		SizeGB     int64   `json:"size_gb"`
		MonthlyEUR float64 `json:"monthly_eur"`
	} `json:"disks"`
	Pricing struct {
		GBMonthEUR float64 `json:"per_gb_monthly_eur"`
		MinHours   int64   `json:"minimum_hours"`
	} `json:"pricing"`
	Classes map[string]struct {
		GBMonthEUR float64 `json:"per_gb_monthly_eur"`
	} `json:"classes"`
}

// grows only: a smaller size_gb is refused at plan time.
type diskNoShrink struct{}

func (diskNoShrink) Description(context.Context) string         { return "size_gb can only grow" }
func (diskNoShrink) MarkdownDescription(context.Context) string { return "size_gb can only grow" }
func (diskNoShrink) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}
	if req.PlanValue.ValueInt64() < req.StateValue.ValueInt64() {
		resp.Diagnostics.AddAttributeError(req.Path, "Disk cannot shrink",
			fmt.Sprintf("size_gb %d is smaller than the current %d GB. Additional disks only grow; to get a smaller disk, create a new resource.", req.PlanValue.ValueInt64(), req.StateValue.ValueInt64()))
	}
}

func (r *serverDiskResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_disk"
}

func (r *serverDiskResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "One additional disk (class ssd or hdd) of a Gyro Cloud Istanbul (tr-ist-1) server. Billed per GB hourly from the prepaid balance until destroyed " +
			"(per_gb_monthly_eur, at least minimum_hours per disk). Up to 4 disks per server; sizes are multiples of 10 GB. Changing size_gb grows the disk in place " +
			"(never shrinks; not while the server has snapshots) - extend the partition / file system inside the server afterwards. " +
			"The new disk is empty: partition, format and mount it yourself (e.g. with cloud-init). Destroy deletes the disk WITH its data.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<server_id>/<disk_id>"},
			"server_id":          schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "tr-ist-1 server id."},
			"class": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("ssd"), PlanModifiers: replaceS,
				Validators: []validator.String{stringvalidator.OneOf("ssd", "hdd")},
				Description: "ssd (fast, default) or hdd (large storage for backups, archives and media; not for databases). Changing it replaces the disk."},
			"size_gb":            schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{diskNoShrink{}}, Description: "Disk size in GB (multiple of 10). Can only grow."},
			"disk_id":            schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Disk id on the server."},
			"name":               schema.StringAttribute{Computed: true, Description: "Display name (Additional disk N)."},
			"monthly_eur":        schema.Float64Attribute{Computed: true, Description: "Current monthly price of this disk in EUR (VAT excluded)."},
			"per_gb_monthly_eur": schema.Float64Attribute{Computed: true, Description: "Current price per GB-month in EUR (VAT excluded)."},
			"minimum_hours":      schema.Int64Attribute{Computed: true, Description: "Every disk is billed at least this many hours."},
		},
	}
}

func (r *serverDiskResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverDiskResource) path(m *serverDiskModel) string {
	return fmt.Sprintf("cloud/servers/%d/disks", sid(m.ServerID))
}

func (r *serverDiskResource) fill(m *serverDiskModel, l *serverDiskList) bool {
	for _, d := range l.Disks {
		if d.ID == m.DiskID.ValueInt64() {
			m.ID = types.StringValue(fmt.Sprintf("%d/%d", sid(m.ServerID), d.ID))
			m.SizeGB = types.Int64Value(d.SizeGB)
			m.Name = types.StringValue(d.Name)
			if d.Class == "" {
				d.Class = "ssd"
			}
			m.Class = types.StringValue(d.Class)
			m.MonthlyEUR = types.Float64Value(d.MonthlyEUR)
			m.GBMonthEUR = types.Float64Value(l.Pricing.GBMonthEUR)
			if c, ok := l.Classes[d.Class]; ok {
				m.GBMonthEUR = types.Float64Value(c.GBMonthEUR)
			}
			m.MinHours = types.Int64Value(l.Pricing.MinHours)
			return true
		}
	}
	return false
}

func (r *serverDiskResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverDiskModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var before serverDiskList
	if _, err := r.pd.c.Do(ctx, http.MethodGet, r.path(&m), nil, nil, "", &before); err != nil {
		resp.Diagnostics.AddError("Reading the server's disks failed", err.Error())
		return
	}
	seen := map[int64]bool{}
	for _, d := range before.Disks {
		seen[d.ID] = true
	}
	var out serverDiskList
	if _, err := r.pd.c.Do(ctx, http.MethodPost, r.path(&m), nil, map[string]any{"size_gb": m.SizeGB.ValueInt64(), "class": m.Class.ValueString()}, "", &out); err != nil {
		resp.Diagnostics.AddError("Adding the disk failed", err.Error())
		return
	}
	for _, d := range out.Disks {
		if !seen[d.ID] {
			m.DiskID = types.Int64Value(d.ID)
		}
	}
	if m.DiskID.IsUnknown() || m.DiskID.IsNull() || !r.fill(&m, &out) {
		resp.Diagnostics.AddError("Adding the disk failed", "the API did not return the new disk (it may still be attaching; run terraform refresh / import <server_id>/<disk_id>)")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverDiskResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverDiskModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var out serverDiskList
	_, err := r.pd.c.Do(ctx, http.MethodGet, r.path(&m), nil, nil, "", &out)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading disks failed", err.Error())
		return
	}
	if !r.fill(&m, &out) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: size_gb grows the disk in place.
func (r *serverDiskResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st serverDiskModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.DiskID = st.DiskID
	var out serverDiskList
	if m.SizeGB.ValueInt64() != st.SizeGB.ValueInt64() {
		if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("%s/%d", r.path(&m), st.DiskID.ValueInt64()), nil, map[string]any{"size_gb": m.SizeGB.ValueInt64()}, "", &out); err != nil {
			resp.Diagnostics.AddError("Growing the disk failed", err.Error())
			return
		}
	} else if _, err := r.pd.c.Do(ctx, http.MethodGet, r.path(&m), nil, nil, "", &out); err != nil {
		resp.Diagnostics.AddError("Reading disks failed", err.Error())
		return
	}
	want := m.SizeGB.ValueInt64()
	if !r.fill(&m, &out) {
		resp.Diagnostics.AddError("Growing the disk failed", "the disk is no longer on the server")
		return
	}
	m.SizeGB = types.Int64Value(want) // the task finishes a few seconds later; keep the planned size
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverDiskResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverDiskModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	q := url.Values{"confirm": []string{"true"}}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", r.path(&m), m.DiskID.ValueInt64()), q, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the disk failed", err.Error())
	}
}

func (r *serverDiskResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	var d int64
	if len(p) == 2 {
		d, err = strconv.ParseInt(p[1], 10, 64)
	}
	if len(p) != 2 || err != nil || d <= 0 {
		resp.Diagnostics.AddError("Invalid import id", "Use <server_id>/<disk_id>, e.g. 123/2001")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("disk_id"), d)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
