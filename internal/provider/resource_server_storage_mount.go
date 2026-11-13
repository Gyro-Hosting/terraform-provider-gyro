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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// A Gyro Object Storage bucket mounted on a server as a folder. API (GyroAPI >= 2.17.5):
// GET/POST /cloud/servers/{id}/storage-mounts, GET/DELETE /cloud/servers/{id}/storage-mounts/{mid}.

var _ resource.ResourceWithImportState = (*storageMountResource)(nil)

type storageMountResource struct{ pd *providerData }

func newStorageMountResource() resource.Resource { return &storageMountResource{} }

type storageMountModel struct {
	ID         types.String `tfsdk:"id"`
	ServerID   types.Int64  `tfsdk:"server_id"`
	StorageID  types.Int64  `tfsdk:"storage_service_id"`
	Bucket     types.String `tfsdk:"bucket"`
	MountPoint types.String `tfsdk:"mount_point"`
	ReadOnly   types.Bool   `tfsdk:"read_only"`
	MountID    types.Int64  `tfsdk:"mount_id"`
	Name       types.String `tfsdk:"name"`
	State      types.String `tfsdk:"state"`
	Automatic  types.Bool   `tfsdk:"automatic"`
	InstallCmd types.String `tfsdk:"install_command"`
	RemoveCmd  types.String `tfsdk:"remove_command"`
}

type storageMount struct {
	ID         int64  `json:"id"`
	ServerID   int64  `json:"server_id"`
	StorageID  int64  `json:"storage_service_id"`
	Bucket     string `json:"bucket"`
	MountPoint string `json:"mount_point"`
	ReadOnly   bool   `json:"read_only"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Automatic  bool   `json:"automatic"`
	InstallCmd string `json:"install_command"`
	RemoveCmd  string `json:"remove_command"`
}

type storageMountOne struct {
	Mount storageMount `json:"storage_mount"`
}

func (r *storageMountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_storage_mount"
}

func (r *storageMountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replB := []planmodifier.Bool{boolplanmodifier.RequiresReplace()}
	replSK := []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Mounts a bucket of your Gyro Object Storage service on a Linux server (systemd) as a folder (rclone FUSE mount with a local " +
			"write cache, starts on boot). Each mount has its own access key limited to that one bucket (read-only or read/write), removed on destroy. " +
			"Gyro Cloud Istanbul (tr-ist-1) servers apply it automatically (automatic = true); on other servers run install_command once " +
			"(valid 60 minutes, single use). Free; S3 traffic counts towards the storage service. Not available for Protected Storage buckets.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<server_id>/<mount_id>"},
			"server_id":          schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Server service id."},
			"storage_service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Your active Object Storage service id."},
			"bucket":             schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Bucket name in that service (renaming the bucket later does not break the mount)."},
			"mount_point": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replSK,
				Description: "Absolute path under /mnt, /srv, /data, /media, /opt, /home or /var/www; default /mnt/<bucket>. Must be empty or missing on the server."},
			"read_only": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), PlanModifiers: replB},
			"mount_id":  schema.Int64Attribute{Computed: true},
			"name":      schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "systemd unit instance gyro-s3mount@<name>."},
			"state":     schema.StringAttribute{Computed: true, Description: "pending | installing | active | error | expired (command not used within 60 minutes: taint to get a new one)."},
			"automatic": schema.BoolAttribute{Computed: true, Description: "true when applied by the Gyro Compute guest agent (tr-ist-1)."},
			"install_command": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep,
				Description: "One-line installer (only known right after create; single use, 60 minutes). Run it on the server as root unless automatic is true."},
			"remove_command": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Cleans up the unit on a non tr-ist-1 server after destroy."},
		},
	}
}

func (r *storageMountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *storageMountResource) base(m *storageMountModel) string {
	return fmt.Sprintf("cloud/servers/%d/storage-mounts", sid(m.ServerID))
}

func (r *storageMountResource) set(m *storageMountModel, x *storageMount, withCmd bool) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%d", x.ServerID, x.ID))
	m.ServerID = types.Int64Value(x.ServerID)
	m.StorageID = types.Int64Value(x.StorageID)
	m.MountPoint = types.StringValue(x.MountPoint)
	m.ReadOnly = types.BoolValue(x.ReadOnly)
	m.MountID = types.Int64Value(x.ID)
	m.Name = types.StringValue(x.Name)
	m.State = types.StringValue(x.State)
	m.Automatic = types.BoolValue(x.Automatic)
	m.RemoveCmd = types.StringValue(x.RemoveCmd)
	if withCmd {
		m.InstallCmd = types.StringValue(x.InstallCmd)
	} else if m.InstallCmd.IsUnknown() || m.InstallCmd.IsNull() {
		m.InstallCmd = types.StringValue("")
	}
	if m.Bucket.IsUnknown() || m.Bucket.IsNull() {
		m.Bucket = types.StringValue(x.Bucket)
	}
}

func (r *storageMountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m storageMountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"storage_service_id": m.StorageID.ValueInt64(), "bucket": m.Bucket.ValueString(), "read_only": m.ReadOnly.ValueBool()}
	if !m.MountPoint.IsNull() && !m.MountPoint.IsUnknown() && m.MountPoint.ValueString() != "" {
		body["mount_point"] = m.MountPoint.ValueString()
	}
	var out storageMountOne
	if _, err := r.pd.c.Do(ctx, http.MethodPost, r.base(&m), nil, body, "", &out); err != nil {
		resp.Diagnostics.AddError("Mounting the bucket failed", err.Error())
		return
	}
	r.set(&m, &out.Mount, true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageMountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m storageMountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var out storageMountOne
	_, err := r.pd.c.Do(ctx, http.MethodGet, r.base(&m)+"/"+strconv.FormatInt(m.MountID.ValueInt64(), 10), nil, nil, "", &out)
	if client.IsNotFound(err) || (err == nil && (out.Mount.State == "removed" || out.Mount.State == "ended")) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the storage mount failed", err.Error())
		return
	}
	r.set(&m, &out.Mount, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: every input forces a new mount.
func (r *storageMountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m storageMountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageMountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m storageMountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.Do(ctx, http.MethodDelete, r.base(&m)+"/"+strconv.FormatInt(m.MountID.ValueInt64(), 10), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the storage mount failed", err.Error())
	}
}

func (r *storageMountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	s, err1 := strconv.ParseInt(p[0], 10, 64)
	var mid int64
	var err2 error
	if len(p) == 2 {
		mid, err2 = strconv.ParseInt(p[1], 10, 64)
	}
	if len(p) != 2 || err1 != nil || err2 != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use <server_id>/<mount_id>, e.g. 17547/12")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), s)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mount_id"), mid)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
