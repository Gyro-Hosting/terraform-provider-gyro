package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Protected Storage (versioning + object lock / WORM), Gyro Public API >= 2.14.0:
//   gyro_storage_bucket_object_lock           -> /cloud/storage/{service}/buckets/{bucket}/object-lock
//   gyro_storage_bucket_noncurrent_expiration -> /cloud/storage/{service}/buckets/{bucket}/lifecycle/noncurrent

var (
	_ resource.ResourceWithImportState = (*bucketObjectLockResource)(nil)
	_ resource.ResourceWithImportState = (*bucketNoncurrentResource)(nil)
)

/* ================================================================== gyro_storage_bucket_object_lock */

type bucketObjectLockResource struct{ pd *providerData }

func newBucketObjectLockResource() resource.Resource { return &bucketObjectLockResource{} }

type bucketObjectLockModel struct {
	ID                    types.String `tfsdk:"id"`
	ServiceID             types.Int64  `tfsdk:"service_id"`
	Bucket                types.String `tfsdk:"bucket"`
	Mode                  types.String `tfsdk:"mode"`
	Days                  types.Int64  `tfsdk:"days"`
	AcknowledgeCompliance types.Bool   `tfsdk:"acknowledge_compliance"`
	DisableOnDestroy      types.Bool   `tfsdk:"disable_on_destroy"`
}

type bucketLockState struct {
	Bucket     string `json:"bucket"`
	ObjectLock bool   `json:"object_lock"`
	Default    *struct {
		Mode  string `json:"mode"`
		Days  *int64 `json:"days"`
		Years *int64 `json:"years"`
	} `json:"default_retention"`
}

func (r *bucketObjectLockResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_object_lock"
}

func (r *bucketObjectLockResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Default retention of a locked bucket on a Gyro Protected Storage service: every new object version is locked for `days` automatically. " +
			"GOVERNANCE can only be bypassed by Gyro staff on request; COMPLIANCE cannot be shortened or removed by anyone (Gyro included) and needs " +
			"`acknowledge_compliance = true`. Versions locked under this rule stay locked until their own date even after the rule changes. " +
			"Destroy only removes the resource from the Terraform state and leaves the rule in place (a warning is shown), unless `disable_on_destroy = true` " +
			"(then new objects are no longer locked automatically; existing locks are not affected). The bucket must be created with `object_lock = true`.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Protected Storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Full bucket name (gyro_storage_bucket.<x>.full_name)."},
			"mode": schema.StringAttribute{Required: true, Description: "GOVERNANCE or COMPLIANCE.",
				Validators: []validator.String{stringvalidator.OneOf("GOVERNANCE", "COMPLIANCE")}},
			"days": schema.Int64Attribute{Required: true, Description: "Retention in days, 1-3650.",
				Validators: []validator.Int64{int64validator.Between(1, 3650)}},
			"acknowledge_compliance": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Must be true for mode = COMPLIANCE: you accept that COMPLIANCE-locked versions cannot be deleted by anyone until they expire, " +
					"the data is kept and the service keeps being billed until then (even after cancellation)."},
			"disable_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Turn the default retention off when the resource is destroyed (default false: destroy only forgets the resource)."},
		},
	}
}

func (r *bucketObjectLockResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *bucketObjectLockResource) get(ctx context.Context, m *bucketObjectLockModel) (*bucketLockState, error) {
	var out bucketLockState
	if _, err := r.pd.c.Do(ctx, http.MethodGet, bucketPath(m.ServiceID, m.Bucket, "object-lock"), nil, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *bucketObjectLockResource) save(ctx context.Context, m *bucketObjectLockModel, d interface{ AddError(string, string) }) bool {
	mode := m.Mode.ValueString()
	body := map[string]any{"mode": mode, "days": m.Days.ValueInt64()}
	if mode == "COMPLIANCE" {
		if !m.AcknowledgeCompliance.ValueBool() {
			d.AddError("COMPLIANCE needs acknowledge_compliance = true",
				"COMPLIANCE retention cannot be shortened, switched to GOVERNANCE or removed by anyone (Gyro included) until it expires; the data is kept and "+
					"the service keeps being billed until then, even after cancellation. Set acknowledge_compliance = true to accept this.")
			return false
		}
		body["confirm"] = true
		body["acknowledge"] = "COMPLIANCE"
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "object-lock"), body); err != nil {
		d.AddError("Setting default retention failed", err.Error())
		return false
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	return true
}

func (r *bucketObjectLockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketObjectLockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketObjectLockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketObjectLockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	st, err := r.get(ctx, &m)
	if client.IsNotFound(err) || (err == nil && st.Default == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading default retention failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	m.Mode = types.StringValue(st.Default.Mode)
	switch {
	case st.Default.Days != nil:
		m.Days = types.Int64Value(*st.Default.Days)
	case st.Default.Years != nil:
		m.Days = types.Int64Value(*st.Default.Years * 365)
	}
	if m.AcknowledgeCompliance.IsNull() {
		m.AcknowledgeCompliance = types.BoolValue(st.Default.Mode == "COMPLIANCE")
	}
	if m.DisableOnDestroy.IsNull() {
		m.DisableOnDestroy = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketObjectLockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m bucketObjectLockModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketObjectLockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketObjectLockModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if !m.DisableOnDestroy.ValueBool() {
		resp.Diagnostics.AddWarning("Default retention left in place",
			fmt.Sprintf("gyro_storage_bucket_object_lock for %s was removed from the Terraform state only: the bucket still locks new objects (%s, %d days). "+
				"Set disable_on_destroy = true to turn it off on destroy. Object locks already applied cannot be removed.",
				m.Bucket.ValueString(), m.Mode.ValueString(), m.Days.ValueInt64()))
		return
	}
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "object-lock"), map[string]any{"mode": "OFF"})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Turning default retention off failed", err.Error())
		return
	}
	resp.Diagnostics.AddWarning("Default retention turned off", "New objects are no longer locked automatically. Versions locked before stay locked until their own date.")
}

func (r *bucketObjectLockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importServiceBucket(ctx, req, resp)
}

/* ================================================================== gyro_storage_bucket_noncurrent_expiration */

type bucketNoncurrentResource struct{ pd *providerData }

func newBucketNoncurrentResource() resource.Resource { return &bucketNoncurrentResource{} }

type bucketNoncurrentModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	Bucket    types.String `tfsdk:"bucket"`
	Days      types.Int64  `tfsdk:"days"`
}

type noncurrentState struct {
	NoncurrentDays *int64 `json:"noncurrent_days"`
	DefaultDays    int64  `json:"default_days"`
}

func (r *bucketNoncurrentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_noncurrent_expiration"
}

func (r *bucketNoncurrentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Old version expiry of a bucket on a Gyro Protected Storage service: non-current versions are deleted `days` after they stop being current " +
			"(0 = keep them; they count toward the quota). Locked versions are deleted only after their retention ends. New buckets start with 30 days. " +
			"Destroy sets the bucket back to the default (30 days).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Protected Storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Full bucket name (gyro_storage_bucket.<x>.full_name)."},
			"days": schema.Int64Attribute{Required: true, Description: "Days, 1-3650; 0 keeps old versions.",
				Validators: []validator.Int64{int64validator.Between(0, 3650)}},
		},
	}
}

func (r *bucketNoncurrentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *bucketNoncurrentResource) put(ctx context.Context, m *bucketNoncurrentModel, days int64) error {
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "lifecycle/noncurrent"), map[string]any{"days": days})
	return err
}

func (r *bucketNoncurrentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketNoncurrentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m, m.Days.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Setting old version expiry failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNoncurrentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketNoncurrentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var st noncurrentState
	_, err := r.pd.c.Do(ctx, http.MethodGet, bucketPath(m.ServiceID, m.Bucket, "lifecycle/noncurrent"), nil, nil, "", &st)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading old version expiry failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	if st.NoncurrentDays != nil {
		m.Days = types.Int64Value(*st.NoncurrentDays)
	} else {
		m.Days = types.Int64Value(0)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNoncurrentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m bucketNoncurrentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m, m.Days.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Setting old version expiry failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNoncurrentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketNoncurrentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if err := r.put(ctx, &m, 30); err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Restoring the default old version expiry failed", err.Error())
	}
}

func (r *bucketNoncurrentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importServiceBucket(ctx, req, resp)
}
