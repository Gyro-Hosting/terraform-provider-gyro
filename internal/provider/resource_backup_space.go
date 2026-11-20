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
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
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

// Gyro Backup Space (Gyro Yedekleme Alanı), GyroAPI >= 2.44.0:
// GET /cloud/backup-spaces/plans, POST /cloud/backup-spaces, GET /cloud/backup-spaces/{id}, GET /cloud/backup-spaces/{id}/credentials,
// PUT /cloud/backup-spaces/{id}/server, DELETE /cloud/backup-spaces/{id}?confirm=true&immediate=true, PUT /cloud/services/{id}/label (name).
// plan and cycle never replace the resource (that would delete every file): a change is refused at plan time.

var _ resource.ResourceWithImportState = (*backupSpaceResource)(nil)
var _ resource.ResourceWithModifyPlan = (*backupSpaceResource)(nil)

type backupSpaceResource struct{ pd *providerData }

func newBackupSpaceResource() resource.Resource { return &backupSpaceResource{} }

type backupSpaceModel struct {
	ID                types.String `tfsdk:"id"`
	Plan              types.String `tfsdk:"plan"`
	Cycle             types.String `tfsdk:"cycle"`
	ServerID          types.Int64  `tfsdk:"server_id"`
	Name              types.String `tfsdk:"name"`
	DeleteImmediately types.Bool   `tfsdk:"delete_immediately"`
	ServiceID         types.Int64  `tfsdk:"service_id"`
	ProductID         types.Int64  `tfsdk:"product_id"`
	Status            types.String `tfsdk:"status"`
	State             types.String `tfsdk:"state"`
	SizeGB            types.Int64  `tfsdk:"size_gb"`
	Host              types.String `tfsdk:"host"`
	Port              types.Int64  `tfsdk:"port"`
	User              types.String `tfsdk:"user"`
	Path              types.String `tfsdk:"path"`
	Password          types.String `tfsdk:"password"`
	AllowedIPs        types.List   `tfsdk:"allowed_ips"`
	DueDate           types.String `tfsdk:"due_date"`
}

type backupConn struct {
	Host        string   `json:"host"`
	Port        int64    `json:"port"`
	User        string   `json:"user"`
	Path        string   `json:"path"`
	SizeGB      int64    `json:"size_gb"`
	SizePlan    string   `json:"size_plan"`
	UsedBytes   *int64   `json:"used_bytes"`
	UsedPercent *float64 `json:"used_percent"`
	ServerID    *int64   `json:"server_id"`
	AllowedIPs  []string `json:"allowed_ips"`
	State       string   `json:"state"`
}

type backupDetail struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	Product     int64      `json:"product_id"`
	Nickname    string     `json:"nickname"`
	Cycle       string     `json:"cycle"`
	DueDate     string     `json:"due_date"`
	BackupSpace backupConn `json:"backup_space"`
}

type backupCreds struct {
	Password string `json:"password"`
}

var regexpBackupPlan = regexp.MustCompile(`^[0-9]+(gb|tb)$`)

// backupNoChange refuses an in-place change of plan / cycle (re-creating the space would delete every file).
type backupNoChange struct{ what string }

func (n backupNoChange) Description(context.Context) string {
	return n.what + " cannot change in place"
}
func (n backupNoChange) MarkdownDescription(context.Context) string {
	return n.what + " cannot change in place"
}
func (n backupNoChange) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}
	if req.PlanValue.ValueString() != req.StateValue.ValueString() {
		resp.Diagnostics.AddAttributeError(req.Path, n.what+" cannot be changed by Terraform",
			fmt.Sprintf("%s %q -> %q: Terraform does not re-create a backup space (that would delete every file in it). Change the size or billing cycle in the panel "+
				"(or GET /cloud/services/{id}/resize-options + POST /cloud/services/{id}/resize, `gyro service resize`), then set the new value here. "+
				"To really start over, destroy and create a new resource.", n.what, req.StateValue.ValueString(), req.PlanValue.ValueString()))
	}
}

func (r *backupSpaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_backup_space"
}

func (r *backupSpaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepI := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A Gyro Backup Space (Gyro Yedekleme Alanı, region tr-ist-1): an SFTP space with a hard quota, bound to ONE server of the account and reachable only " +
			"from that server's IPv4 addresses (port 2201, writable directory /veri). Ordered and paid from the account balance (fixed EUR prices charged in the account currency; " +
			"renews every cycle from the balance). List the plans and the servers it can be bound to with the gyro_backup_space_plans data source (`gyro backup-space plans`). " +
			"DESTROY DELETES THE SPACE AND EVERY FILE IN IT within about a minute (no snapshot, cannot be undone). Use lifecycle { prevent_destroy = true } for backups you must keep. " +
			"plan and cycle never trigger a replacement: changing them is refused (resize in the panel or with POST /cloud/services/{id}/resize). Tags and project: gyro_service_label, gyro_service_project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Service id."},
			"plan": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{backupNoChange{"plan"}},
				Validators:  []validator.String{stringvalidator.RegexMatches(regexpBackupPlan, "a plan from GET /cloud/backup-spaces/plans, e.g. 100gb or 1tb")},
				Description: "Size: 50gb | 100gb | 250gb | 500gb | 1tb (see gyro_backup_space_plans). Cannot change in place."},
			"cycle": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("monthly"), PlanModifiers: []planmodifier.String{backupNoChange{"cycle"}},
				Validators:  []validator.String{stringvalidator.OneOf("monthly", "quarterly", "semiannually", "annually")},
				Description: "Billing cycle: monthly (default), quarterly, semiannually, annually (only cycles the plan offers)."},
			"server_id": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepI,
				Description: "Server the space is bound to (one of your active servers). Optional when the account has exactly one. Changing it re-binds the space in place " +
					"(PUT /cloud/backup-spaces/{id}/server): the old server loses access at once, files are not touched."},
			"name": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:  []validator.String{stringvalidator.LengthAtMost(60)},
				Description: "Service name (nickname, max 60). Changing it renames the service in place; when not set, the name on the platform is kept."},
			"delete_immediately": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "destroy cancels at once and deletes every file (default). false = file a cancellation request instead (the space keeps working until it is processed)."},
			"service_id": schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Service id (number)."},
			"product_id": schema.Int64Attribute{Computed: true, PlanModifiers: keepI},
			"status":     schema.StringAttribute{Computed: true, Description: "Service status: active, suspended, cancelled..."},
			"state":      schema.StringAttribute{Computed: true, Description: "pending, active, noserver (no bound server: nobody can connect), suspended, cancelled, purged."},
			"size_gb":    schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Hard quota in GB (writes are refused when full)."},
			"host":       schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "SFTP host."},
			"port":       schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "SFTP port (2201)."},
			"user":       schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "SFTP user."},
			"path":       schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Writable directory (/veri; the root is read-only)."},
			"password": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep,
				Description: "SFTP password (GET /cloud/backup-spaces/{id}/credentials; kept in the state - protect the state file). A reset in the panel shows up on the next refresh."},
			"allowed_ips": schema.ListAttribute{Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				Description: "IPv4 addresses of the bound server allowed to connect (follows IP changes of the server automatically)."},
			"due_date": schema.StringAttribute{Computed: true, Description: "Next renewal date."},
		},
	}
}

// ModifyPlan: a re-bind changes the allowed IPs (and possibly the state), so they are unknown until the apply.
func (r *backupSpaceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var pl, st types.Int64
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("server_id"), &pl)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("server_id"), &st)...)
	if pl.IsUnknown() || pl.IsNull() || (!st.IsNull() && pl.ValueInt64() == st.ValueInt64()) {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("allowed_ips"), types.ListUnknown(types.StringType))...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("state"), types.StringUnknown())...)
}

func (r *backupSpaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func readBackupSpace(ctx context.Context, pd *providerData, id int64) (*backupDetail, error) {
	var d backupDetail
	if _, err := pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/backup-spaces/%d", id), nil, nil, "", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *backupSpaceResource) password(ctx context.Context, id int64) (string, error) {
	var c backupCreds
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/backup-spaces/%d/credentials", id), nil, nil, "", &c); err != nil {
		return "", err
	}
	return c.Password, nil
}

func (r *backupSpaceResource) apply(ctx context.Context, m *backupSpaceModel, d *backupDetail) {
	m.ID = types.StringValue(strconv.FormatInt(d.ID, 10))
	m.ServiceID = types.Int64Value(d.ID)
	m.ProductID = types.Int64Value(d.Product)
	m.Status = types.StringValue(d.Status)
	m.DueDate = types.StringValue(d.DueDate)
	if d.BackupSpace.SizePlan != "" {
		m.Plan = types.StringValue(d.BackupSpace.SizePlan)
	}
	if d.Cycle != "" {
		m.Cycle = types.StringValue(d.Cycle)
	}
	if d.BackupSpace.ServerID != nil {
		m.ServerID = types.Int64Value(*d.BackupSpace.ServerID)
	} else if m.ServerID.IsUnknown() {
		m.ServerID = types.Int64Null()
	}
	m.State = types.StringValue(d.BackupSpace.State)
	m.SizeGB = types.Int64Value(d.BackupSpace.SizeGB)
	m.Host = types.StringValue(d.BackupSpace.Host)
	m.Port = types.Int64Value(d.BackupSpace.Port)
	m.User = types.StringValue(d.BackupSpace.User)
	m.Path = types.StringValue(d.BackupSpace.Path)
	m.AllowedIPs, _ = types.ListValueFrom(ctx, types.StringType, nonNil(d.BackupSpace.AllowedIPs))
	m.Name = types.StringValue(d.Nickname)
	if m.Password.IsUnknown() {
		m.Password = types.StringNull()
	}
}

// refreshPassword keeps the stored password when the space is not ready (409) or not active.
func (r *backupSpaceResource) refreshPassword(ctx context.Context, m *backupSpaceModel, d *backupDetail) {
	if d.BackupSpace.User == "" || d.Status == "cancelled" {
		return
	}
	if pw, err := r.password(ctx, d.ID); err == nil && pw != "" {
		m.Password = types.StringValue(pw)
	}
}

func (r *backupSpaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m backupSpaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"plan": m.Plan.ValueString(), "cycle": m.Cycle.ValueString(), "confirm": true}
	if !m.ServerID.IsNull() && !m.ServerID.IsUnknown() {
		body["server_id"] = m.ServerID.ValueInt64()
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() && m.Name.ValueString() != "" {
		body["name"] = m.Name.ValueString()
	}
	var out struct {
		BackupSpace struct {
			ID int64 `json:"id"`
		} `json:"backup_space"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/backup-spaces", nil, body, client.NewIdempotencyKey(), &out); err != nil {
		resp.Diagnostics.AddError("Ordering the backup space failed", err.Error())
		return
	}
	if out.BackupSpace.ID == 0 {
		resp.Diagnostics.AddError("Ordering the backup space failed", "the API did not return the new service id")
		return
	}
	id := out.BackupSpace.ID
	m.ID = types.StringValue(strconv.FormatInt(id, 10))
	// set up takes about a minute: wait until the SFTP user exists (state active / noserver)
	deadline := time.Now().Add(r.pd.pollTimeout)
	var d *backupDetail
	for {
		x, err := readBackupSpace(ctx, r.pd, id)
		if err == nil {
			d = x
			if x.Status == "active" && x.BackupSpace.User != "" && (x.BackupSpace.State == "active" || x.BackupSpace.State == "noserver") {
				break
			}
			if x.Status == "cancelled" || x.Status == "suspended" {
				resp.Diagnostics.AddError("Backup space did not become active", "status: "+x.Status)
				break
			}
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddWarning("Backup space is still being set up", "It was ordered and paid (id "+m.ID.ValueString()+"); run terraform refresh later.")
			break
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Cancelled while waiting", ctx.Err().Error())
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), m.ID)...)
			return
		case <-time.After(r.pd.pollInterval):
		}
	}
	if d != nil {
		r.apply(ctx, &m, d)
		r.refreshPassword(ctx, &m, d)
	} else {
		m.ServiceID = types.Int64Value(id)
		for _, s := range []*types.String{&m.Status, &m.State, &m.Host, &m.User, &m.Path, &m.DueDate} {
			if s.IsUnknown() {
				*s = types.StringValue("")
			}
		}
	}
	if m.Password.IsUnknown() {
		m.Password = types.StringNull()
	}
	nullUnknownInts(&m.ProductID, &m.SizeGB, &m.Port, &m.ServerID)
	if m.AllowedIPs.IsUnknown() {
		m.AllowedIPs, _ = types.ListValueFrom(ctx, types.StringType, []string{})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func nullUnknownInts(xs ...*types.Int64) {
	for _, x := range xs {
		if x.IsUnknown() {
			*x = types.Int64Null()
		}
	}
}

func (r *backupSpaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m backupSpaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	d, err := readBackupSpace(ctx, r.pd, id)
	if client.IsNotFound(err) || (err == nil && d.Status == "cancelled") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the backup space failed", err.Error())
		return
	}
	r.apply(ctx, &m, d)
	r.refreshPassword(ctx, &m, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: name (label), server_id (re-bind) and delete_immediately change in place; plan / cycle are refused at plan time.
func (r *backupSpaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st, cfg backupSpaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID = st.ID
	id, _ := strconv.ParseInt(st.ID.ValueString(), 10, 64)
	if !cfg.Name.IsNull() && !cfg.Name.IsUnknown() && m.Name.ValueString() != st.Name.ValueString() {
		if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/services/%d/label", id), nil, map[string]any{"nickname": m.Name.ValueString()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Renaming the backup space failed", err.Error())
			return
		}
	}
	if !cfg.ServerID.IsNull() && !cfg.ServerID.IsUnknown() && (st.ServerID.IsNull() || cfg.ServerID.ValueInt64() != st.ServerID.ValueInt64()) {
		if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/backup-spaces/%d/server", id), nil, map[string]any{"server_id": cfg.ServerID.ValueInt64()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Binding the backup space to the server failed", err.Error())
			return
		}
	}
	d, err := readBackupSpace(ctx, r.pd, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading the backup space failed", err.Error())
		return
	}
	want := m.Name
	m.Password = st.Password
	r.apply(ctx, &m, d)
	r.refreshPassword(ctx, &m, d)
	if !cfg.Name.IsNull() {
		m.Name = want
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *backupSpaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m backupSpaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	q := url.Values{"confirm": []string{"true"}}
	if m.DeleteImmediately.IsNull() || m.DeleteImmediately.ValueBool() {
		q.Set("immediate", "true")
	}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, "cloud/backup-spaces/"+m.ID.ValueString(), q, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cancelling the backup space failed", err.Error())
	}
}

func (r *backupSpaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the backup space (service) id, e.g. 12345")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_immediately"), true)...)
}

/* ------------------------------------------------------------------ data source gyro_backup_space */

type backupSpaceDS struct{ pd *providerData }

func newBackupSpaceDataSource() datasource.DataSource { return &backupSpaceDS{} }

type backupSpaceDSModel struct {
	ID          types.Int64   `tfsdk:"id"`
	Name        types.String  `tfsdk:"name"`
	Status      types.String  `tfsdk:"status"`
	State       types.String  `tfsdk:"state"`
	Plan        types.String  `tfsdk:"plan"`
	Cycle       types.String  `tfsdk:"cycle"`
	SizeGB      types.Int64   `tfsdk:"size_gb"`
	UsedBytes   types.Int64   `tfsdk:"used_bytes"`
	UsedPercent types.Float64 `tfsdk:"used_percent"`
	Host        types.String  `tfsdk:"host"`
	Port        types.Int64   `tfsdk:"port"`
	User        types.String  `tfsdk:"user"`
	Path        types.String  `tfsdk:"path"`
	ServerID    types.Int64   `tfsdk:"server_id"`
	AllowedIPs  types.List    `tfsdk:"allowed_ips"`
	DueDate     types.String  `tfsdk:"due_date"`
}

func (d *backupSpaceDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_backup_space"
}

func (d *backupSpaceDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Connection details, usage and state of an existing Gyro Backup Space (GET /cloud/backup-spaces/{id}). The password is not exposed here; " +
			"use the gyro_backup_space resource (sensitive password) or `gyro backup-space credentials <id>`.",
		Attributes: map[string]dschema.Attribute{
			"id":           dschema.Int64Attribute{Required: true, Description: "Backup space (service) id."},
			"name":         dschema.StringAttribute{Computed: true},
			"status":       dschema.StringAttribute{Computed: true, Description: "Service status."},
			"state":        dschema.StringAttribute{Computed: true, Description: "pending, active, noserver, suspended, cancelled, purged."},
			"plan":         dschema.StringAttribute{Computed: true, Description: "Size plan, e.g. 100gb."},
			"cycle":        dschema.StringAttribute{Computed: true},
			"size_gb":      dschema.Int64Attribute{Computed: true},
			"used_bytes":   dschema.Int64Attribute{Computed: true, Description: "Used space (null when it could not be measured)."},
			"used_percent": dschema.Float64Attribute{Computed: true},
			"host":         dschema.StringAttribute{Computed: true},
			"port":         dschema.Int64Attribute{Computed: true},
			"user":         dschema.StringAttribute{Computed: true},
			"path":         dschema.StringAttribute{Computed: true},
			"server_id":    dschema.Int64Attribute{Computed: true, Description: "Bound server (null = none)."},
			"allowed_ips":  dschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"due_date":     dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *backupSpaceDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *backupSpaceDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m backupSpaceDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	x, err := readBackupSpace(ctx, d.pd, m.ID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Reading the backup space failed", err.Error())
		return
	}
	b := x.BackupSpace
	m.Name, m.Status, m.State, m.Plan, m.Cycle = types.StringValue(x.Nickname), types.StringValue(x.Status), types.StringValue(b.State), types.StringValue(b.SizePlan), types.StringValue(x.Cycle)
	m.SizeGB, m.Port = types.Int64Value(b.SizeGB), types.Int64Value(b.Port)
	m.UsedBytes, m.ServerID = i64(b.UsedBytes), i64(b.ServerID)
	if b.UsedPercent != nil {
		m.UsedPercent = types.Float64Value(*b.UsedPercent)
	} else {
		m.UsedPercent = types.Float64Null()
	}
	m.Host, m.User, m.Path, m.DueDate = types.StringValue(b.Host), types.StringValue(b.User), types.StringValue(b.Path), types.StringValue(x.DueDate)
	m.AllowedIPs, _ = types.ListValueFrom(ctx, types.StringType, nonNil(b.AllowedIPs))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

/* ------------------------------------------------------------------ data source gyro_backup_space_plans */

type backupPlansDS struct{ pd *providerData }

func newBackupPlansDataSource() datasource.DataSource { return &backupPlansDS{} }

type backupPriceModel struct {
	Cycle      types.String  `tfsdk:"cycle"`
	Amount     types.Float64 `tfsdk:"amount"`
	ListAmount types.Float64 `tfsdk:"list_amount"`
	Currency   types.String  `tfsdk:"currency"`
}

type backupPlanModel struct {
	ID        types.Int64        `tfsdk:"id"`
	Plan      types.String       `tfsdk:"plan"`
	Name      types.String       `tfsdk:"name"`
	Region    types.String       `tfsdk:"region"`
	SizeGB    types.Int64        `tfsdk:"size_gb"`
	Available types.Bool         `tfsdk:"available"`
	Prices    []backupPriceModel `tfsdk:"prices"`
}

type backupServerModel struct {
	ServerID types.Int64  `tfsdk:"server_id"`
	Name     types.String `tfsdk:"name"`
	IP       types.String `tfsdk:"ip"`
}

type backupPlansModel struct {
	Plans   []backupPlanModel   `tfsdk:"plans"`
	Servers []backupServerModel `tfsdk:"servers"`
}

func (d *backupPlansDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_backup_space_plans"
}

func (d *backupPlansDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Orderable Gyro Backup Space plans (GET /cloud/backup-spaces/plans): sizes with prices per billing cycle (fixed EUR, VAT excluded; list_amount = " +
			"strike-through list price) and the servers of the account a space can be bound to. Empty plans = not orderable for this account yet.",
		Attributes: map[string]dschema.Attribute{
			"plans": dschema.ListNestedAttribute{Computed: true, NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
				"id":        dschema.Int64Attribute{Computed: true, Description: "Product id."},
				"plan":      dschema.StringAttribute{Computed: true, Description: "Plan name for gyro_backup_space.plan (50gb, 100gb, 250gb, 500gb, 1tb)."},
				"name":      dschema.StringAttribute{Computed: true},
				"region":    dschema.StringAttribute{Computed: true},
				"size_gb":   dschema.Int64Attribute{Computed: true},
				"available": dschema.BoolAttribute{Computed: true, Description: "false = no capacity right now."},
				"prices": dschema.ListNestedAttribute{Computed: true, NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
					"cycle":       dschema.StringAttribute{Computed: true},
					"amount":      dschema.Float64Attribute{Computed: true},
					"list_amount": dschema.Float64Attribute{Computed: true},
					"currency":    dschema.StringAttribute{Computed: true},
				}}},
			}}},
			"servers": dschema.ListNestedAttribute{Computed: true, Description: "Servers a backup space can be bound to.", NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
				"server_id": dschema.Int64Attribute{Computed: true},
				"name":      dschema.StringAttribute{Computed: true},
				"ip":        dschema.StringAttribute{Computed: true},
			}}},
		},
	}
}

func (d *backupPlansDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *backupPlansDS) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var out struct {
		Plans []struct {
			ID        int64  `json:"id"`
			Plan      string `json:"plan"`
			Name      string `json:"name"`
			Region    string `json:"region"`
			SizeGB    int64  `json:"size_gb"`
			Available bool   `json:"available"`
			Prices    []struct {
				Cycle      string   `json:"cycle"`
				Amount     float64  `json:"amount"`
				ListAmount *float64 `json:"list_amount"`
				Currency   string   `json:"currency"`
			} `json:"prices"`
		} `json:"plans"`
		Servers []struct {
			ServerID int64  `json:"server_id"`
			Name     string `json:"name"`
			IP       string `json:"ip"`
		} `json:"servers"`
	}
	if _, err := d.pd.c.Do(ctx, http.MethodGet, "cloud/backup-spaces/plans", nil, nil, "", &out); err != nil {
		resp.Diagnostics.AddError("Reading the backup space plans failed", err.Error())
		return
	}
	m := backupPlansModel{Plans: []backupPlanModel{}, Servers: []backupServerModel{}}
	for _, p := range out.Plans {
		pm := backupPlanModel{ID: types.Int64Value(p.ID), Plan: types.StringValue(p.Plan), Name: types.StringValue(p.Name), Region: types.StringValue(p.Region),
			SizeGB: types.Int64Value(p.SizeGB), Available: types.BoolValue(p.Available), Prices: []backupPriceModel{}}
		for _, pr := range p.Prices {
			la := types.Float64Null()
			if pr.ListAmount != nil {
				la = types.Float64Value(*pr.ListAmount)
			}
			pm.Prices = append(pm.Prices, backupPriceModel{Cycle: types.StringValue(pr.Cycle), Amount: types.Float64Value(pr.Amount), ListAmount: la, Currency: types.StringValue(pr.Currency)})
		}
		m.Plans = append(m.Plans, pm)
	}
	for _, s := range out.Servers {
		m.Servers = append(m.Servers, backupServerModel{ServerID: types.Int64Value(s.ServerID), Name: types.StringValue(s.Name), IP: types.StringValue(s.IP)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
