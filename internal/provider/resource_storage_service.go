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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// One Object Storage / Protected Storage service. API (GyroAPI >= 2.25.0):
// GET /cloud/storage/plans, POST /cloud/storage, GET /cloud/storage/{id}, DELETE /cloud/storage/{id}?confirm=true&immediate=true,
// PUT /cloud/services/{id}/label (name). Buckets and keys: gyro_storage_bucket, gyro_storage_key.

var _ resource.ResourceWithImportState = (*storageServiceResource)(nil)

type storageServiceResource struct{ pd *providerData }

func newStorageServiceResource() resource.Resource { return &storageServiceResource{} }

type storageServiceModel struct {
	ID                types.String `tfsdk:"id"`
	Plan              types.String `tfsdk:"plan"`
	Cycle             types.String `tfsdk:"cycle"`
	Name              types.String `tfsdk:"name"`
	ObjectLockBuckets types.Int64  `tfsdk:"object_lock_buckets"`
	DeleteImmediately types.Bool   `tfsdk:"delete_immediately"`
	ServiceID         types.Int64  `tfsdk:"service_id"`
	ProductID         types.Int64  `tfsdk:"product_id"`
	Kind              types.String `tfsdk:"kind"`
	Status            types.String `tfsdk:"status"`
	SizeGB            types.Int64  `tfsdk:"size_gb"`
	MaxBuckets        types.Int64  `tfsdk:"max_buckets"`
	IncludedTrafficGB types.Int64  `tfsdk:"included_traffic_gb_month"`
	Endpoint          types.String `tfsdk:"endpoint"`
	Region            types.String `tfsdk:"region"`
	DueDate           types.String `tfsdk:"due_date"`
}

type storageDetail struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	Product  int64  `json:"product_id"`
	Nickname string `json:"nickname"`
	Cycle    string `json:"cycle"`
	DueDate  string `json:"due_date"`
	Storage  struct {
		Plan            string `json:"plan"`
		Kind            string `json:"kind"`
		SizeGB          int64  `json:"size_gb"`
		MaxBuckets      int64  `json:"max_buckets"`
		IncludedTraffic int64  `json:"included_traffic_gb_month"`
		Endpoint        string `json:"endpoint"`
		Region          string `json:"region"`
	} `json:"storage"`
}

// immutable without a new service: changing it would mean deleting the data, so it is refused at plan time instead of replacing.
type noChangeString struct{ what string }

func (n noChangeString) Description(context.Context) string {
	return n.what + " cannot change in place"
}
func (n noChangeString) MarkdownDescription(context.Context) string {
	return n.what + " cannot change in place"
}
func (n noChangeString) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
		return
	}
	if req.PlanValue.ValueString() != req.StateValue.ValueString() {
		resp.Diagnostics.AddAttributeError(req.Path, n.what+" cannot be changed by Terraform",
			fmt.Sprintf("%s %q -> %q: Terraform does not re-create a storage service (that would delete every bucket and object). Change the plan or billing cycle in the panel "+
				"(or POST /cloud/services/{id}/resize), then set the new value here. To really start over, destroy and create a new resource.", n.what, req.StateValue.ValueString(), req.PlanValue.ValueString()))
	}
}

type noChangeInt struct{ what string }

func (n noChangeInt) Description(context.Context) string { return n.what + " is set at creation" }
func (n noChangeInt) MarkdownDescription(context.Context) string {
	return n.what + " is set at creation"
}
func (n noChangeInt) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() {
		return
	}
	if req.PlanValue.IsNull() || req.PlanValue.ValueInt64() != req.StateValue.ValueInt64() {
		resp.Diagnostics.AddAttributeError(req.Path, n.what+" is set at creation",
			"Buy more object lock rights in the panel (add-ons) or with POST /cloud/services/{id}/addons; Terraform does not re-create a storage service.")
	}
}

func (r *storageServiceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage"
}

func (r *storageServiceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepI := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A Gyro Object Storage (S3) or Protected Storage service (region tr-ist-1). Ordered and paid from the account balance (fixed EUR prices, " +
			"charged in the account currency; renews every cycle from the balance). The first bucket and access key are created automatically; manage buckets and " +
			"keys with gyro_storage_bucket and gyro_storage_key. List the plans with `gyro storage plans` (GET /cloud/storage/plans); put the service in a project with gyro_service_project. " +
			"DESTROY DELETES EVERY BUCKET AND OBJECT of the service within minutes (cannot be undone; Protected Storage versions under a COMPLIANCE lock or legal hold " +
			"stay until the lock ends). Use lifecycle { prevent_destroy = true } for data you must keep. plan and cycle never trigger a replacement: changing them is refused.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Service id."},
			"plan": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{noChangeString{"plan"}},
				Validators:  []validator.String{stringvalidator.RegexMatches(regexpPlan, "a plan from GET /cloud/storage/plans, e.g. standard-1tb or protected-250gb")},
				Description: "standard-250gb | standard-1tb | standard-5tb | standard-10tb | protected-250gb | protected-1tb | protected-5tb | protected-10tb."},
			"cycle": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("monthly"), PlanModifiers: []planmodifier.String{noChangeString{"cycle"}},
				Validators:  []validator.String{stringvalidator.OneOf("monthly", "quarterly", "semiannually", "annually")},
				Description: "Billing cycle: monthly (default), quarterly, semiannually, annually."},
			"name": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.LengthAtMost(60)},
				Description: "Service name (nickname, max 60). Changing it renames the service in place."},
			"object_lock_buckets": schema.Int64Attribute{Optional: true, PlanModifiers: []planmodifier.Int64{noChangeInt{"object_lock_buckets"}},
				Validators:  []validator.Int64{int64validator.Between(1, 50)},
				Description: "Protected plans only: object lock (WORM) bucket rights bought with the service (billed every cycle). Set at creation."},
			"delete_immediately": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "destroy cancels at once and deletes the data (default). false = file a cancellation request instead (the service keeps running until it is processed)."},
			"service_id":                schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Service id (number)."},
			"product_id":                schema.Int64Attribute{Computed: true, PlanModifiers: keepI},
			"kind":                      schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "standard or protected."},
			"status":                    schema.StringAttribute{Computed: true, Description: "active, suspended, cancelled..."},
			"size_gb":                   schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Storage quota in GB."},
			"max_buckets":               schema.Int64Attribute{Computed: true, PlanModifiers: keepI},
			"included_traffic_gb_month": schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Outbound traffic included every calendar month."},
			"endpoint":                  schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "S3 endpoint URL."},
			"region":                    schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "S3 region (tr-ist-1)."},
			"due_date":                  schema.StringAttribute{Computed: true, Description: "Next renewal date."},
		},
	}
}

func (r *storageServiceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *storageServiceResource) read(ctx context.Context, id int64) (*storageDetail, error) {
	var d storageDetail
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/storage/%d", id), nil, nil, "", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *storageServiceResource) apply(m *storageServiceModel, d *storageDetail) {
	m.ID = types.StringValue(strconv.FormatInt(d.ID, 10))
	m.ServiceID = types.Int64Value(d.ID)
	m.ProductID = types.Int64Value(d.Product)
	m.Status = types.StringValue(d.Status)
	m.DueDate = types.StringValue(d.DueDate)
	if d.Storage.Plan != "" {
		m.Plan = types.StringValue(d.Storage.Plan)
	}
	if d.Cycle != "" {
		m.Cycle = types.StringValue(d.Cycle)
	}
	m.Kind = types.StringValue(d.Storage.Kind)
	m.SizeGB = types.Int64Value(d.Storage.SizeGB)
	m.MaxBuckets = types.Int64Value(d.Storage.MaxBuckets)
	m.IncludedTrafficGB = types.Int64Value(d.Storage.IncludedTraffic)
	m.Endpoint = types.StringValue(d.Storage.Endpoint)
	m.Region = types.StringValue(d.Storage.Region)
	if !m.Name.IsNull() || d.Nickname != "" {
		m.Name = types.StringValue(d.Nickname)
	}
}

func (r *storageServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m storageServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"plan": m.Plan.ValueString(), "cycle": m.Cycle.ValueString(), "confirm": true}
	if !m.Name.IsNull() && m.Name.ValueString() != "" {
		body["name"] = m.Name.ValueString()
	}
	if !m.ObjectLockBuckets.IsNull() {
		body["object_lock_buckets"] = m.ObjectLockBuckets.ValueInt64()
	}
	var out struct {
		Storage struct {
			ID int64 `json:"id"`
		} `json:"storage"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/storage", nil, body, client.NewIdempotencyKey(), &out); err != nil {
		resp.Diagnostics.AddError("Ordering the storage service failed", err.Error())
		return
	}
	if out.Storage.ID == 0 {
		resp.Diagnostics.AddError("Ordering the storage service failed", "the API did not return the new service id")
		return
	}
	m.ID = types.StringValue(strconv.FormatInt(out.Storage.ID, 10))
	// set up (first bucket + key) takes about a minute: wait for active
	deadline := time.Now().Add(r.pd.pollTimeout)
	var d *storageDetail
	for {
		x, err := r.read(ctx, out.Storage.ID)
		if err == nil {
			d = x
			if x.Status == "active" {
				break
			}
			if x.Status == "cancelled" || x.Status == "suspended" {
				resp.Diagnostics.AddError("Storage service did not become active", "status: "+x.Status)
				break
			}
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddWarning("Storage service is still being set up", "It was ordered and paid (id "+m.ID.ValueString()+"); run terraform refresh later.")
			break
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Cancelled while waiting", ctx.Err().Error())
			return
		case <-time.After(r.pd.pollInterval):
		}
	}
	if d != nil {
		r.apply(&m, d)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m storageServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	d, err := r.read(ctx, id)
	if client.IsNotFound(err) || (err == nil && d.Status == "cancelled") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the storage service failed", err.Error())
		return
	}
	r.apply(&m, d)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: only name (label) and delete_immediately change in place; plan / cycle / object_lock_buckets are refused at plan time.
func (r *storageServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st storageServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID = st.ID
	id, _ := strconv.ParseInt(st.ID.ValueString(), 10, 64)
	if m.Name.ValueString() != st.Name.ValueString() {
		if _, err := r.pd.c.Do(ctx, http.MethodPut, fmt.Sprintf("cloud/services/%d/label", id), nil, map[string]any{"nickname": m.Name.ValueString()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Renaming the storage service failed", err.Error())
			return
		}
	}
	d, err := r.read(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading the storage service failed", err.Error())
		return
	}
	want := m.Name
	r.apply(&m, d)
	m.Name = want
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m storageServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	q := url.Values{"confirm": []string{"true"}}
	if m.DeleteImmediately.IsNull() || m.DeleteImmediately.ValueBool() {
		q.Set("immediate", "true")
	}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, "cloud/storage/"+m.ID.ValueString(), q, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cancelling the storage service failed", err.Error())
	}
}

func (r *storageServiceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.ParseInt(req.ID, 10, 64); err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the storage service id, e.g. 12345")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_immediately"), true)...)
}

var _ = stringplanmodifier.UseStateForUnknown

var regexpPlan = regexp.MustCompile(`^(standard|protected)-[0-9]+(gb|tb)$`)
