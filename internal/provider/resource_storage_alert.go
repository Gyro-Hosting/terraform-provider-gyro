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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_storage_bucket_alert: per-bucket fill alert (e-mail) of Object / Protected Storage (Gyro Public API >= 2.43.0).
// API: GET|PUT|DELETE /cloud/storage/{service}/buckets/{bucket}/alert (one alert per bucket; PUT replaces it).

var _ resource.ResourceWithImportState = (*bucketAlertResource)(nil)

type bucketAlertResource struct{ pd *providerData }

func newBucketAlertResource() resource.Resource { return &bucketAlertResource{} }

type bucketAlertModel struct {
	ID          types.String  `tfsdk:"id"`
	ServiceID   types.Int64   `tfsdk:"service_id"`
	Bucket      types.String  `tfsdk:"bucket"`
	Type        types.String  `tfsdk:"type"`
	Value       types.Int64   `tfsdk:"value"`
	ThresholdGB types.Float64 `tfsdk:"threshold_gb"`
	State       types.String  `tfsdk:"state"`
	LastSentAt  types.String  `tfsdk:"last_sent_at"`
}

type bucketAlertItem struct {
	Bucket      string   `json:"bucket"`
	Enabled     bool     `json:"enabled"`
	Type        *string  `json:"type"`
	Value       *int64   `json:"value"`
	ThresholdGB *float64 `json:"threshold_gb"`
	State       string   `json:"state"`
	LastSentAt  *string  `json:"last_sent_at"`
}

func (r *bucketAlertResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_alert"
}

func (r *bucketAlertResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fill alert of a Gyro Object Storage / Protected Storage bucket: one e-mail to the account owner when the bucket's usage reaches the threshold. " +
			"It re-arms when usage drops below 95 % of the threshold, so the next crossing e-mails again. Checked every 5 minutes. " +
			"Protected Storage counts old versions and delete markers too. One alert per bucket; destroy removes the alert (no data is deleted).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<bucket>"},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Object / Protected Storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Bucket name."},
			"type": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("percent", "gb")},
				Description: "percent = share of the package size, gb = absolute size."},
			"value": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(1)},
				Description: "percent: 1-100; gb: 1 - package size in GB."},
			"threshold_gb": schema.Float64Attribute{Computed: true, Description: "Threshold in GB (percent rules follow the package size)."},
			"state":        schema.StringAttribute{Computed: true, Description: "armed or triggered (e-mail sent, waiting for usage to drop below 95 % of the threshold)."},
			"last_sent_at": schema.StringAttribute{Computed: true, Description: "Time of the last alert e-mail (ISO 8601) or empty."},
		},
	}
}

func (r *bucketAlertResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *bucketAlertResource) apply(m *bucketAlertModel, it *bucketAlertItem) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	if it.Type != nil {
		m.Type = types.StringValue(*it.Type)
	}
	if it.Value != nil {
		m.Value = types.Int64Value(*it.Value)
	}
	th := 0.0
	if it.ThresholdGB != nil {
		th = *it.ThresholdGB
	}
	m.ThresholdGB = types.Float64Value(th)
	m.State = types.StringValue(it.State)
	ls := ""
	if it.LastSentAt != nil {
		ls = *it.LastSentAt
	}
	m.LastSentAt = types.StringValue(ls)
}

func (r *bucketAlertResource) get(ctx context.Context, m *bucketAlertModel) (*bucketAlertItem, error) {
	var out struct {
		Item bucketAlertItem `json:"item"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, bucketPath(m.ServiceID, m.Bucket, "alert"), nil, nil, "", &out); err != nil {
		return nil, err
	}
	return &out.Item, nil
}

func (r *bucketAlertResource) put(ctx context.Context, m *bucketAlertModel) error {
	var out struct {
		Item bucketAlertItem `json:"item"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "alert"), nil,
		map[string]any{"type": m.Type.ValueString(), "value": m.Value.ValueInt64()}, "", &out); err != nil {
		return err
	}
	it, err := r.get(ctx, m)
	if err != nil {
		return err
	}
	r.apply(m, it)
	return nil
}

func (r *bucketAlertResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketAlertModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Setting bucket alert failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketAlertResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketAlertModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	it, err := r.get(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "tool_action_failed") || (err == nil && !it.Enabled) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket alert failed", err.Error())
		return
	}
	r.apply(&m, it)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketAlertResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m bucketAlertModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Updating bucket alert failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketAlertResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketAlertModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, bucketPath(m.ServiceID, m.Bucket, "alert"), map[string]any{})
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Removing bucket alert failed", err.Error())
	}
}

func (r *bucketAlertResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importServiceBucket(ctx, req, resp)
}
