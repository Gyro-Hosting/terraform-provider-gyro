package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_storage_bucket_notification: bucket event notification webhook (GYR-18, Gyro Public API >= 2.37.0).
// API: POST /cloud/storage/{service}/buckets/{bucket}/events, GET|PATCH|DELETE /cloud/storage/{service}/events/{id}.

var _ resource.ResourceWithImportState = (*bucketNotificationResource)(nil)

var storageEventTypes = []string{"storage.object.created", "storage.object.deleted", "storage.objects.deleted_bulk"}

type bucketNotificationResource struct{ pd *providerData }

func newBucketNotificationResource() resource.Resource { return &bucketNotificationResource{} }

type bucketNotificationModel struct {
	ID             types.String `tfsdk:"id"`
	ServiceID      types.Int64  `tfsdk:"service_id"`
	Bucket         types.String `tfsdk:"bucket"`
	URL            types.String `tfsdk:"url"`
	Events         types.List   `tfsdk:"events"`
	Prefix         types.String `tfsdk:"prefix"`
	Suffix         types.List   `tfsdk:"suffix"`
	Description    types.String `tfsdk:"description"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	WebhookID      types.Int64  `tfsdk:"webhook_id"`
	Secret         types.String `tfsdk:"secret"`
	Status         types.String `tfsdk:"status"`
	DisabledReason types.String `tfsdk:"disabled_reason"`
}

type storageEventItem struct {
	ID             int64    `json:"id"`
	Bucket         string   `json:"bucket"`
	URL            string   `json:"url"`
	Events         []string `json:"events"`
	Prefix         string   `json:"prefix"`
	Suffix         []string `json:"suffix"`
	Description    *string  `json:"description"`
	Status         string   `json:"status"`
	DisabledReason *string  `json:"disabled_reason"`
	Secret         string   `json:"secret"`
}

func (r *bucketNotificationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_notification"
}

func (r *bucketNotificationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Bucket event notification (webhook) of Gyro Object Storage / Protected Storage: a signed HTTPS POST is sent to `url` when an object is " +
			"created, deleted, or a bulk delete runs. Signature: X-Gyro-Signature = sha256=HMAC-SHA256(secret, X-Gyro-Timestamp + \".\" + raw body). " +
			"Delivery is at least once (retried up to 8 times, about 2 hours); 20 failures in a row disable the webhook (status disabled, disabled_reason auto_failures; " +
			"apply again with enabled = true to re-enable). Monthly free allowance per bucket plus prepaid event packs, no overage invoice.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<webhook_id>"},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Object / Protected Storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Bucket name."},
			"url":        schema.StringAttribute{Required: true, Description: "Target address: https only, port 443 or 8443, on the public internet."},
			"events": schema.ListAttribute{ElementType: types.StringType, Optional: true, Computed: true,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, evStrList(storageEventTypes))),
				Description: "storage.object.created, storage.object.deleted, storage.objects.deleted_bulk (default: all three)."},
			"prefix": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Only keys starting with this text."},
			"suffix": schema.ListAttribute{ElementType: types.StringType, Optional: true, Computed: true, Default: listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
				Description: "Only keys ending with one of these (case-insensitive, at most 10), e.g. [\".jpg\", \".png\"]."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Free text (at most 128 characters)."},
			"enabled":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "false = paused (no events are sent)."},
			"webhook_id":  schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Webhook id."},
			"secret": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Description: "Signing secret (returned only when created; empty after import - rotate it with the API / CLI if needed)."},
			"status":          schema.StringAttribute{Computed: true, Description: "active or disabled."},
			"disabled_reason": schema.StringAttribute{Computed: true, Description: "user, auto_failures or empty."},
		},
	}
}

func (r *bucketNotificationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func evStrList(v []string) []attr.Value {
	out := make([]attr.Value, 0, len(v))
	for _, s := range v {
		out = append(out, types.StringValue(s))
	}
	return out
}

func listStrings(ctx context.Context, l types.List) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	l.ElementsAs(ctx, &out, false)
	return out
}

func (r *bucketNotificationResource) body(ctx context.Context, m *bucketNotificationModel) map[string]any {
	st := "active"
	if !m.Enabled.ValueBool() {
		st = "disabled"
	}
	return map[string]any{"url": m.URL.ValueString(), "events": listStrings(ctx, m.Events), "prefix": m.Prefix.ValueString(),
		"suffix": listStrings(ctx, m.Suffix), "description": m.Description.ValueString(), "status": st}
}

func (r *bucketNotificationResource) apply(m *bucketNotificationModel, it *storageEventItem) {
	m.WebhookID = types.Int64Value(it.ID)
	m.ID = types.StringValue(fmt.Sprintf("%d/%d", sid(m.ServiceID), it.ID))
	if it.Bucket != "" {
		m.Bucket = types.StringValue(it.Bucket)
	}
	m.URL = types.StringValue(it.URL)
	if len(it.Events) == 0 {
		it.Events = storageEventTypes
	}
	m.Events = types.ListValueMust(types.StringType, evStrList(it.Events))
	m.Prefix = types.StringValue(it.Prefix)
	if it.Suffix == nil {
		it.Suffix = []string{}
	}
	// the API stores extensions in lower case: keep the configured spelling when it only differs in case (no diff, no inconsistent result)
	if prev := listStrings(context.Background(), m.Suffix); len(prev) == len(it.Suffix) {
		same := true
		for i := range prev {
			if !strings.EqualFold(prev[i], it.Suffix[i]) {
				same = false
				break
			}
		}
		if same {
			it.Suffix = prev
		}
	}
	m.Suffix = types.ListValueMust(types.StringType, evStrList(it.Suffix))
	d := ""
	if it.Description != nil {
		d = *it.Description
	}
	m.Description = types.StringValue(d)
	m.Status = types.StringValue(it.Status)
	m.Enabled = types.BoolValue(it.Status == "active")
	dr := ""
	if it.DisabledReason != nil {
		dr = *it.DisabledReason
	}
	m.DisabledReason = types.StringValue(dr)
	if it.Secret != "" {
		m.Secret = types.StringValue(it.Secret)
	} else if m.Secret.IsNull() || m.Secret.IsUnknown() {
		m.Secret = types.StringValue("")
	}
}

func (r *bucketNotificationResource) get(ctx context.Context, m *bucketNotificationModel) (*storageEventItem, error) {
	var out struct {
		Item storageEventItem `json:"item"`
	}
	_, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/storage/%d/events/%d", sid(m.ServiceID), m.WebhookID.ValueInt64()), nil, nil, "", &out)
	if err != nil {
		return nil, err
	}
	return &out.Item, nil
}

func (r *bucketNotificationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketNotificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := r.body(ctx, &m)
	enabled := b["status"] == "active"
	delete(b, "status")
	var out struct {
		Item storageEventItem `json:"item"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, bucketPath(m.ServiceID, m.Bucket, "events"), nil, b, "", &out); err != nil {
		resp.Diagnostics.AddError("Creating bucket notification failed", err.Error())
		return
	}
	secret := out.Item.Secret
	m.WebhookID = types.Int64Value(out.Item.ID)
	if !enabled {
		if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, fmt.Sprintf("cloud/storage/%d/events/%d", sid(m.ServiceID), out.Item.ID), map[string]any{"status": "disabled"}); err != nil {
			resp.Diagnostics.AddError("Disabling bucket notification failed", err.Error())
		}
	}
	it, err := r.get(ctx, &m)
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket notification failed", err.Error())
		return
	}
	it.Secret = secret
	r.apply(&m, it)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNotificationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketNotificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	it, err := r.get(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "tool_action_failed") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket notification failed", err.Error())
		return
	}
	r.apply(&m, it)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNotificationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st bucketNotificationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.WebhookID = st.WebhookID
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, fmt.Sprintf("cloud/storage/%d/events/%d", sid(m.ServiceID), m.WebhookID.ValueInt64()), r.body(ctx, &m)); err != nil {
		resp.Diagnostics.AddError("Updating bucket notification failed", err.Error())
		return
	}
	it, err := r.get(ctx, &m)
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket notification failed", err.Error())
		return
	}
	m.Secret = st.Secret
	r.apply(&m, it)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketNotificationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketNotificationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, fmt.Sprintf("cloud/storage/%d/events/%d", sid(m.ServiceID), m.WebhookID.ValueInt64()), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Deleting bucket notification failed", err.Error())
	}
}

func (r *bucketNotificationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	s, err1 := strconv.ParseInt(p[0], 10, 64)
	var w int64
	var err2 error
	if len(p) == 2 {
		w, err2 = strconv.ParseInt(p[1], 10, 64)
	}
	if len(p) != 2 || err1 != nil || err2 != nil || w <= 0 {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<webhook_id>, e.g. 12345/7")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), s)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("webhook_id"), w)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
