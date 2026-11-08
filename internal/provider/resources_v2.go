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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func pdOf(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
	}
	return pd
}

func idOf(s types.String) int64 { n, _ := strconv.ParseInt(s.ValueString(), 10, 64); return n }

func strList(ctx context.Context, l types.List) []string {
	var out []string
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	l.ElementsAs(ctx, &out, false)
	return out
}

func int64List(ctx context.Context, l types.List) []int64 {
	var out []int64
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	l.ElementsAs(ctx, &out, false)
	return out
}

var keep = []planmodifier.String{stringplanmodifier.UseStateForUnknown()}

/* ================================================================== gyro_webhook */

type webhookResource struct{ pd *providerData }

func newWebhookResource() resource.Resource { return &webhookResource{} }

type webhookModel struct {
	ID          types.String `tfsdk:"id"`
	URL         types.String `tfsdk:"url"`
	Events      types.List   `tfsdk:"events"`
	Description types.String `tfsdk:"description"`
	CustomerID  types.Int64  `tfsdk:"customer_id"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Secret      types.String `tfsdk:"secret"`
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A signed HTTPS webhook. The signing secret is only known at creation (kept in state, sensitive).",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"url":         schema.StringAttribute{Required: true, Description: "https:// URL on a public host."},
			"events":      schema.ListAttribute{ElementType: types.StringType, Optional: true, Description: "Events to send (empty = all)."},
			"description": schema.StringAttribute{Optional: true},
			"customer_id": schema.Int64Attribute{Optional: true, Description: "Reseller: deliver only this end-customer's events.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"secret":  schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep, Description: "HMAC-SHA256 signing secret."},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *webhookResource) body(ctx context.Context, m webhookModel) map[string]any {
	b := map[string]any{"url": m.URL.ValueString(), "events": strList(ctx, m.Events)}
	if b["events"] == nil {
		b["events"] = []string{}
	}
	if !m.Description.IsNull() {
		b["description"] = m.Description.ValueString()
	}
	return b
}

func (r *webhookResource) set(ctx context.Context, m *webhookModel, w *client.Webhook) {
	m.ID = types.StringValue(strconv.FormatInt(w.ID, 10))
	m.URL = types.StringValue(w.URL)
	if !m.Events.IsNull() || len(w.Events) > 0 {
		m.Events, _ = types.ListValueFrom(ctx, types.StringType, w.Events)
	}
	if w.Description != nil && *w.Description != "" {
		m.Description = types.StringValue(*w.Description)
	}
	m.Enabled = types.BoolValue(w.Status == "active")
	if w.CustomerID != nil {
		m.CustomerID = types.Int64Value(*w.CustomerID)
	}
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := r.body(ctx, m)
	if !m.CustomerID.IsNull() {
		b["customer_id"] = m.CustomerID.ValueInt64()
	}
	w, err := r.pd.c.CreateWebhook(ctx, b)
	if err != nil {
		resp.Diagnostics.AddError("Creating webhook failed", err.Error())
		return
	}
	m.Secret = types.StringValue(w.Secret)
	if !m.Enabled.ValueBool() {
		if w, err = r.pd.c.UpdateWebhook(ctx, w.ID, map[string]any{"status": "disabled"}); err != nil {
			resp.Diagnostics.AddError("Disabling webhook failed", err.Error())
			return
		}
	}
	r.set(ctx, &m, w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	w, err := r.pd.c.Webhook(ctx, idOf(m.ID))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading webhook failed", err.Error())
		return
	}
	r.set(ctx, &m, w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	b := r.body(ctx, m)
	b["status"] = map[bool]string{true: "active", false: "disabled"}[m.Enabled.ValueBool()]
	w, err := r.pd.c.UpdateWebhook(ctx, idOf(st.ID), b)
	if err != nil {
		resp.Diagnostics.AddError("Updating webhook failed", err.Error())
		return
	}
	m.Secret = st.Secret
	r.set(ctx, &m, w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if err := r.pd.c.DeleteWebhook(ctx, idOf(m.ID)); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting webhook failed", err.Error())
	}
}

/* ================================================================== gyro_reseller_customer */

type customerResource struct{ pd *providerData }

func newCustomerResource() resource.Resource { return &customerResource{} }

type customerModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ExternalRef types.String `tfsdk:"external_ref"`
	Status      types.String `tfsdk:"status"`
	AllowOrder  types.Bool   `tfsdk:"allow_order"`
}

func (r *customerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_reseller_customer"
}

func (r *customerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A reseller (bayi) end-customer. Deleting it revokes its keys and webhooks; services stay on the reseller account.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"name":         schema.StringAttribute{Required: true},
			"external_ref": schema.StringAttribute{Optional: true, Description: "Your own customer id (unique per reseller)."},
			"status": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("active"),
				Validators: []validator.String{stringvalidator.OneOf("active", "suspended")}},
			"allow_order": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "May this customer's keys order servers (paid from the reseller balance)?"},
		},
	}
}

func (r *customerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *customerResource) body(m customerModel) map[string]any {
	b := map[string]any{"name": m.Name.ValueString(), "status": m.Status.ValueString(), "allow_order": m.AllowOrder.ValueBool()}
	if !m.ExternalRef.IsNull() {
		b["external_ref"] = m.ExternalRef.ValueString()
	}
	return b
}

func (r *customerResource) set(m *customerModel, e *client.EndCustomer) {
	m.ID = types.StringValue(strconv.FormatInt(e.ID, 10))
	m.Name = types.StringValue(e.Name)
	if e.ExternalRef != nil {
		m.ExternalRef = types.StringValue(*e.ExternalRef)
	}
	m.Status = types.StringValue(e.Status)
	m.AllowOrder = types.BoolValue(e.AllowOrder)
}

func (r *customerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m customerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := r.body(m)
	delete(b, "status")
	e, err := r.pd.c.CreateCustomer(ctx, b)
	if err == nil && m.Status.ValueString() != "active" {
		e, err = r.pd.c.UpdateCustomer(ctx, e.ID, map[string]any{"status": m.Status.ValueString()})
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating customer failed", err.Error())
		return
	}
	r.set(&m, e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *customerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m customerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	e, err := r.pd.c.Customer(ctx, idOf(m.ID))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading customer failed", err.Error())
		return
	}
	r.set(&m, e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *customerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st customerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	e, err := r.pd.c.UpdateCustomer(ctx, idOf(st.ID), r.body(m))
	if err != nil {
		resp.Diagnostics.AddError("Updating customer failed", err.Error())
		return
	}
	r.set(&m, e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *customerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m customerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if err := r.pd.c.DeleteCustomer(ctx, idOf(m.ID)); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting customer failed", err.Error())
	}
}

func (r *customerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

/* ================================================================== gyro_api_key */

type apiKeyResource struct{ pd *providerData }

func newAPIKeyResource() resource.Resource { return &apiKeyResource{} }

type apiKeyModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	CustomerID types.Int64  `tfsdk:"customer_id"`
	Scopes     types.List   `tfsdk:"scopes"`
	Services   types.List   `tfsdk:"services"`
	Actions    types.List   `tfsdk:"actions"`
	IPs        types.List   `tfsdk:"ips"`
	RateLimit  types.Int64  `tfsdk:"rate_limit"`
	Token      types.String `tfsdk:"token"`
	Prefix     types.String `tfsdk:"token_prefix"`
}

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	rl := []planmodifier.List{listplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A sub-account API key (optionally for a reseller end-customer). Any change replaces the key. The token is sensitive and only known at creation.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"name":         schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"customer_id":  schema.Int64Attribute{Optional: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"scopes":       schema.ListAttribute{ElementType: types.StringType, Required: true, PlanModifiers: rl},
			"services":     schema.ListAttribute{ElementType: types.Int64Type, Optional: true, PlanModifiers: rl},
			"actions":      schema.ListAttribute{ElementType: types.StringType, Optional: true, PlanModifiers: rl},
			"ips":          schema.ListAttribute{ElementType: types.StringType, Optional: true, PlanModifiers: rl},
			"rate_limit":   schema.Int64Attribute{Optional: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"token":        schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep},
			"token_prefix": schema.StringAttribute{Computed: true, PlanModifiers: keep},
		},
	}
}

func (r *apiKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := map[string]any{"name": m.Name.ValueString(), "scopes": strList(ctx, m.Scopes)}
	if s := int64List(ctx, m.Services); len(s) > 0 {
		b["services"] = s
	}
	if s := strList(ctx, m.Actions); len(s) > 0 {
		b["actions"] = s
	}
	if s := strList(ctx, m.IPs); len(s) > 0 {
		b["ips"] = s
	}
	if !m.RateLimit.IsNull() {
		b["rate_limit"] = m.RateLimit.ValueInt64()
	}
	k, err := r.pd.c.CreateAPIKey(ctx, m.CustomerID.ValueInt64(), b)
	if err != nil {
		resp.Diagnostics.AddError("Creating API key failed", err.Error())
		return
	}
	m.ID = types.StringValue(strconv.FormatInt(k.ID, 10))
	m.Token = types.StringValue(k.Token)
	m.Prefix = types.StringValue(k.TokenPrefix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	keys, err := r.pd.c.APIKeys(ctx, m.CustomerID.ValueInt64())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading API keys failed", err.Error())
		return
	}
	for _, k := range keys {
		if k.ID == idOf(m.ID) && k.Status == "active" {
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_api_key arguments force replacement.")
}

func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if err := r.pd.c.DeleteAPIKey(ctx, m.CustomerID.ValueInt64(), idOf(m.ID)); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting API key failed", err.Error())
	}
}
