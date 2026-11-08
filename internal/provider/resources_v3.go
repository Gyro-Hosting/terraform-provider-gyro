package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	replaceS = []planmodifier.String{stringplanmodifier.RequiresReplace()}
	replaceI = []planmodifier.Int64{int64planmodifier.RequiresReplace()}
)

func sid(v types.Int64) int64 { return v.ValueInt64() }

/* ================================================================== gyro_mail_mailbox */

type mailboxResource struct{ pd *providerData }

func newMailboxResource() resource.Resource { return &mailboxResource{} }

type mailboxModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	LocalPart types.String `tfsdk:"local_part"`
	Email     types.String `tfsdk:"email"`
	Name      types.String `tfsdk:"name"`
	QuotaMB   types.Int64  `tfsdk:"quota_mb"`
	Password  types.String `tfsdk:"password"`
	Active    types.Bool   `tfsdk:"active"`
}

func (r *mailboxResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mail_mailbox"
}

func (r *mailboxResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A mailbox on a Gyro corporate mail service. Destroy deletes the mailbox and its mail.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Mail service id (gyro_services product_line = mail)."},
			"local_part": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Part before @ (the domain is the service's)."},
			"email":      schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"name":       schema.StringAttribute{Optional: true, Computed: true},
			"quota_mb":   schema.Int64Attribute{Optional: true, Computed: true},
			"password":   schema.StringAttribute{Required: true, Sensitive: true, Description: "Write-only; changing it sets a new password."},
			"active":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
		},
	}
}

func (r *mailboxResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *mailboxResource) base(m mailboxModel) string {
	return fmt.Sprintf("cloud/mail/%d/mailboxes", sid(m.ServiceID))
}

func (r *mailboxResource) find(ctx context.Context, m *mailboxModel) (bool, error) {
	list, err := r.pd.c.Mailboxes(ctx, sid(m.ServiceID))
	if err != nil {
		return false, err
	}
	for _, b := range list {
		if strings.EqualFold(strings.SplitN(b.Email, "@", 2)[0], m.LocalPart.ValueString()) {
			m.Email = types.StringValue(b.Email)
			m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), strings.ToLower(m.LocalPart.ValueString())))
			m.Name = types.StringValue(b.Name)
			m.QuotaMB = types.Int64Value(b.QuotaMB)
			m.Active = types.BoolValue(b.Active == 1)
			return true, nil
		}
	}
	return false, nil
}

func (r *mailboxResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m mailboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := map[string]any{"local_part": m.LocalPart.ValueString(), "password": m.Password.ValueString(), "confirm": true}
	if !m.Name.IsUnknown() && !m.Name.IsNull() {
		b["name"] = m.Name.ValueString()
	}
	if !m.QuotaMB.IsUnknown() && !m.QuotaMB.IsNull() {
		b["quota"] = m.QuotaMB.ValueInt64()
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, r.base(m), b); err != nil {
		resp.Diagnostics.AddError("Creating mailbox failed", err.Error())
		return
	}
	if !m.Active.ValueBool() {
		if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, r.base(m)+"/"+url.PathEscape(m.LocalPart.ValueString()), map[string]any{"active": 0}); err != nil {
			resp.Diagnostics.AddError("Deactivating mailbox failed", err.Error())
			return
		}
	}
	want := m.Active
	if ok, err := r.find(ctx, &m); err != nil || !ok {
		resp.Diagnostics.AddError("Mailbox not found after create", fmt.Sprint(err))
		return
	}
	m.Active = want
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailboxResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m mailboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ok, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading mailbox failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailboxResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st mailboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	p := r.base(m) + "/" + url.PathEscape(m.LocalPart.ValueString())
	b := map[string]any{"active": map[bool]int{true: 1, false: 0}[m.Active.ValueBool()]}
	if !m.Name.IsUnknown() && !m.Name.IsNull() {
		b["name"] = m.Name.ValueString()
	}
	if !m.QuotaMB.IsUnknown() && !m.QuotaMB.IsNull() {
		b["quota"] = m.QuotaMB.ValueInt64()
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, p, b); err != nil {
		resp.Diagnostics.AddError("Updating mailbox failed", err.Error())
		return
	}
	if m.Password.ValueString() != st.Password.ValueString() {
		if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, p+"/password", map[string]any{"password": m.Password.ValueString()}); err != nil {
			resp.Diagnostics.AddError("Changing mailbox password failed", err.Error())
			return
		}
	}
	r.find(ctx, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *mailboxResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m mailboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, r.base(m)+"/"+url.PathEscape(m.LocalPart.ValueString()), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting mailbox failed", err.Error())
	}
}

/* ================================================================== gyro_mail_alias */

type aliasResource struct{ pd *providerData }

func newAliasResource() resource.Resource { return &aliasResource{} }

type aliasModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	LocalPart types.String `tfsdk:"local_part"`
	Address   types.String `tfsdk:"address"`
	Goto      types.List   `tfsdk:"goto"`
}

func (r *aliasResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mail_alias"
}

func (r *aliasResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A forwarding address (alias) on a corporate mail service.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI},
			"local_part": schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"address":    schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"goto":       schema.ListAttribute{ElementType: types.StringType, Required: true, Description: "Destination addresses."},
		},
	}
}

func (r *aliasResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *aliasResource) find(ctx context.Context, m *aliasModel) (bool, error) {
	list, err := r.pd.c.MailAliases(ctx, sid(m.ServiceID))
	if err != nil {
		return false, err
	}
	for _, a := range list {
		if strings.EqualFold(strings.SplitN(a.Address, "@", 2)[0], m.LocalPart.ValueString()) {
			m.ID = types.StringValue(strconv.FormatInt(a.ID, 10))
			m.Address = types.StringValue(a.Address)
			m.Goto, _ = types.ListValueFrom(ctx, types.StringType, a.Goto)
			return true, nil
		}
	}
	return false, nil
}

func (r *aliasResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m aliasModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := map[string]any{"local_part": m.LocalPart.ValueString(), "goto": strings.Join(strList(ctx, m.Goto), ","), "kind": "forward", "confirm": true}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, fmt.Sprintf("cloud/mail/%d/aliases", sid(m.ServiceID)), b); err != nil {
		resp.Diagnostics.AddError("Creating alias failed", err.Error())
		return
	}
	if ok, err := r.find(ctx, &m); err != nil || !ok {
		resp.Diagnostics.AddError("Alias not found after create", fmt.Sprint(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *aliasResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m aliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ok, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading alias failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *aliasResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st aliasModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, fmt.Sprintf("cloud/mail/%d/aliases/%s", sid(m.ServiceID), st.ID.ValueString()),
		map[string]any{"goto": strings.Join(strList(ctx, m.Goto), ",")}); err != nil {
		resp.Diagnostics.AddError("Updating alias failed", err.Error())
		return
	}
	r.find(ctx, &m)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *aliasResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m aliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, fmt.Sprintf("cloud/mail/%d/aliases/%s", sid(m.ServiceID), m.ID.ValueString()), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting alias failed", err.Error())
	}
}

/* ================================================================== gyro_storage_bucket */

type bucketResource struct{ pd *providerData }

func newBucketResource() resource.Resource { return &bucketResource{} }

type bucketModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	Name      types.String `tfsdk:"name"`
	PathURL   types.String `tfsdk:"path_url"`
	VhostURL  types.String `tfsdk:"vhost_url"`
}

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket"
}

func (r *bucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An S3 bucket on a Gyro object storage service. Destroy deletes the bucket (it must be empty).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"path_url":   schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"vhost_url":  schema.StringAttribute{Computed: true, PlanModifiers: keep},
		},
	}
}

func (r *bucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *bucketResource) find(ctx context.Context, m *bucketModel) (bool, error) {
	list, err := r.pd.c.Buckets(ctx, sid(m.ServiceID))
	if err != nil {
		return false, err
	}
	for _, b := range list {
		if b.Name == m.Name.ValueString() {
			m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), b.Name))
			m.PathURL = types.StringValue(b.PathURL)
			m.VhostURL = types.StringValue(b.VhostURL)
			return true, nil
		}
	}
	return false, nil
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, fmt.Sprintf("cloud/storage/%d/buckets", sid(m.ServiceID)), map[string]any{"bucket": m.Name.ValueString(), "confirm": true}); err != nil {
		resp.Diagnostics.AddError("Creating bucket failed", err.Error())
		return
	}
	if ok, err := r.find(ctx, &m); err != nil || !ok {
		resp.Diagnostics.AddError("Bucket not found after create", fmt.Sprint(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ok, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading bucket failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_storage_bucket arguments force replacement.")
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, fmt.Sprintf("cloud/storage/%d/buckets/%s", sid(m.ServiceID), url.PathEscape(m.Name.ValueString())), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting bucket failed", err.Error())
	}
}

/* ================================================================== gyro_storage_key */

type storageKeyResource struct{ pd *providerData }

func newStorageKeyResource() resource.Resource { return &storageKeyResource{} }

type storageKeyModel struct {
	ID          types.String `tfsdk:"id"`
	ServiceID   types.Int64  `tfsdk:"service_id"`
	Label       types.String `tfsdk:"label"`
	AccessKeyID types.String `tfsdk:"access_key_id"`
	SecretKey   types.String `tfsdk:"secret_key"`
}

func (r *storageKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_key"
}

func (r *storageKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An S3 access key. The secret is only returned at creation (kept in state, sensitive).",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id":    schema.Int64Attribute{Required: true, PlanModifiers: replaceI},
			"label":         schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"access_key_id": schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"secret_key":    schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep},
		},
	}
}

func (r *storageKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *storageKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m storageKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	k, err := r.pd.c.CreateStorageKey(ctx, sid(m.ServiceID), m.Label.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating access key failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), k.ID))
	m.AccessKeyID = types.StringValue(k.ID)
	m.SecretKey = types.StringValue(k.Secret)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *storageKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m storageKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	list, err := r.pd.c.StorageKeys(ctx, sid(m.ServiceID))
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading access keys failed", err.Error())
		return
	}
	for _, k := range list {
		if k.ID == m.AccessKeyID.ValueString() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *storageKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_storage_key arguments force replacement.")
}

func (r *storageKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m storageKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, fmt.Sprintf("cloud/storage/%d/keys/%s", sid(m.ServiceID), url.PathEscape(m.AccessKeyID.ValueString())), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting access key failed", err.Error())
	}
}

/* ================================================================== gyro_domain_record */

type dnsResource struct{ pd *providerData }

func newDNSRecordResource() resource.Resource { return &dnsResource{} }

type dnsModel struct {
	ID       types.String `tfsdk:"id"`
	Domain   types.String `tfsdk:"domain"`
	Type     types.String `tfsdk:"type"`
	Name     types.String `tfsdk:"name"`
	Value    types.String `tfsdk:"value"`
	TTL      types.Int64  `tfsdk:"ttl"`
	Priority types.Int64  `tfsdk:"priority"`
	Identity types.String `tfsdk:"identity"`
}

func (r *dnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain_record"
}

func (r *dnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	ri := []planmodifier.Int64{int64planmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A DNS record of a domain registered at Gyro (DNS management add-on required). Any change replaces the record.",
		Attributes: map[string]schema.Attribute{
			"id":       schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"domain":   schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"type":     schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"name":     schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"value":    schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"ttl":      schema.Int64Attribute{Optional: true, PlanModifiers: ri},
			"priority": schema.Int64Attribute{Optional: true, PlanModifiers: ri},
			"identity": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Provider record id (may be empty)."},
		},
	}
}

func (r *dnsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *dnsResource) find(ctx context.Context, m *dnsModel) (bool, error) {
	list, err := r.pd.c.DNSRecords(ctx, m.Domain.ValueString())
	if err != nil {
		return false, err
	}
	for _, x := range list {
		if strings.EqualFold(x.Type, m.Type.ValueString()) && strings.TrimSuffix(x.Name, ".") == strings.TrimSuffix(m.Name.ValueString(), ".") && x.Value == m.Value.ValueString() {
			m.Identity = types.StringValue(fmt.Sprint(x.Identity))
			if x.Identity == nil {
				m.Identity = types.StringValue("")
			}
			m.ID = types.StringValue(m.Domain.ValueString() + "/" + strings.ToUpper(x.Type) + "/" + x.Name + "/" + x.Value)
			return true, nil
		}
	}
	return false, nil
}

func (r *dnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dnsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := map[string]any{"type": strings.ToUpper(m.Type.ValueString()), "name": m.Name.ValueString(), "value": m.Value.ValueString()}
	if !m.TTL.IsNull() {
		b["ttl"] = m.TTL.ValueInt64()
	}
	if !m.Priority.IsNull() {
		b["priority"] = m.Priority.ValueInt64()
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, "domains/"+url.PathEscape(m.Domain.ValueString())+"/dns-records", b); err != nil {
		resp.Diagnostics.AddError("Creating DNS record failed", err.Error())
		return
	}
	if ok, err := r.find(ctx, &m); err != nil || !ok {
		m.ID = types.StringValue(m.Domain.ValueString() + "/" + strings.ToUpper(m.Type.ValueString()) + "/" + m.Name.ValueString() + "/" + m.Value.ValueString())
		m.Identity = types.StringValue("")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ok, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading DNS records failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_domain_record arguments force replacement.")
}

func (r *dnsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	b := map[string]any{"type": strings.ToUpper(m.Type.ValueString()), "name": m.Name.ValueString(), "value": m.Value.ValueString()}
	if m.Identity.ValueString() != "" {
		b["identity"] = m.Identity.ValueString()
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, "domains/"+url.PathEscape(m.Domain.ValueString())+"/dns-records", b); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting DNS record failed", err.Error())
	}
}

/* ================================================================== gyro_app_install */

type appResource struct{ pd *providerData }

func newAppResource() resource.Resource { return &appResource{} }

type appModel struct {
	ID          types.String `tfsdk:"id"`
	ServerID    types.Int64  `tfsdk:"server_id"`
	App         types.String `tfsdk:"app"`
	OS          types.String `tfsdk:"os"`
	Domain      types.String `tfsdk:"domain"`
	Email       types.String `tfsdk:"email"`
	Status      types.String `tfsdk:"status"`
	URL         types.String `tfsdk:"url"`
	Credentials types.Map    `tfsdk:"credentials"`
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_install"
}

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Installs a one-click app on a cloud server. WARNING: the server disk is rebuilt. Destroy only forgets the install (the server keeps running).",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"server_id":   schema.Int64Attribute{Required: true, PlanModifiers: replaceI},
			"app":         schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "App id from GET /cloud/apps."},
			"os":          schema.StringAttribute{Optional: true, PlanModifiers: replaceS},
			"domain":      schema.StringAttribute{Optional: true, PlanModifiers: replaceS},
			"email":       schema.StringAttribute{Optional: true, PlanModifiers: replaceS},
			"status":      schema.StringAttribute{Computed: true},
			"url":         schema.StringAttribute{Computed: true},
			"credentials": schema.MapAttribute{ElementType: types.StringType, Computed: true, Sensitive: true, PlanModifiers: nil},
		},
	}
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	b := map[string]any{"app": m.App.ValueString(), "confirm": true}
	for k, v := range map[string]types.String{"os": m.OS, "domain": m.Domain, "email": m.Email} {
		if !v.IsNull() && !v.IsUnknown() {
			b[k] = v.ValueString()
		}
	}
	res, err := r.pd.c.TypedWrite(ctx, http.MethodPost, fmt.Sprintf("cloud/servers/%d/app", sid(m.ServerID)), b)
	if err != nil {
		resp.Diagnostics.AddError("Starting the app install failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprint(res["install_id"]))
	wctx, cancel := context.WithTimeout(ctx, r.pd.pollTimeout+15*time.Minute)
	defer cancel()
	var in *client.AppInstall
	for {
		in, err = r.pd.c.AppStatus(wctx, sid(m.ServerID))
		if err == nil && in != nil && (in.Status == "hazir" || in.Status == "hata") {
			break
		}
		select {
		case <-wctx.Done():
			resp.Diagnostics.AddError("App install did not finish in time", "Check GET /cloud/servers/{id}/app")
			return
		case <-time.After(r.pd.pollInterval * 2):
		}
	}
	if in.Status == "hata" {
		resp.Diagnostics.AddError("App install failed", fmt.Sprint(in.Error))
		return
	}
	r.set(ctx, &m, in)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *appResource) set(ctx context.Context, m *appModel, in *client.AppInstall) {
	m.Status = types.StringValue(map[string]string{"hazir": "ready", "hata": "failed"}[in.Status])
	if m.Status.ValueString() == "" {
		m.Status = types.StringValue(in.Status)
	}
	m.URL = types.StringPointerValue(in.URL)
	if len(in.Credentials) > 0 {
		m.Credentials, _ = types.MapValueFrom(ctx, types.StringType, in.Credentials)
	} else if m.Credentials.IsUnknown() {
		m.Credentials = types.MapNull(types.StringType)
	}
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	in, err := r.pd.c.AppStatus(ctx, sid(m.ServerID))
	if client.IsNotFound(err) || (err == nil && in == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading app status failed", err.Error())
		return
	}
	creds := m.Credentials
	r.set(ctx, &m, in)
	if len(in.Credentials) == 0 {
		m.Credentials = creds // shown once by the API; keep what state already has
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_app_install arguments force replacement.")
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}
