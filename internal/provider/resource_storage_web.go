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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Object storage static website (entry file, error page, SPA fallback) and bucket CORS rules.
// API: /cloud/storage/{service}/buckets/{bucket}/website|cors (typed tool routes, GyroAPI >= 2.6.0).

var (
	_ resource.ResourceWithImportState = (*bucketWebsiteResource)(nil)
	_ resource.ResourceWithImportState = (*bucketCorsResource)(nil)
)

func bucketPath(m types.Int64, b types.String, sub string) string {
	return fmt.Sprintf("cloud/storage/%d/buckets/%s/%s", sid(m), url.PathEscape(b.ValueString()), sub)
}

func importServiceBucket(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || p[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<bucket>, e.g. 123/assets")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

/* ================================================================== gyro_storage_bucket_website */

type bucketWebsiteResource struct{ pd *providerData }

func newBucketWebsiteResource() resource.Resource { return &bucketWebsiteResource{} }

type bucketWebsiteModel struct {
	ID            types.String `tfsdk:"id"`
	ServiceID     types.Int64  `tfsdk:"service_id"`
	Bucket        types.String `tfsdk:"bucket"`
	IndexDocument types.String `tfsdk:"index_document"`
	ErrorDocument types.String `tfsdk:"error_document"`
	SPA           types.Bool   `tfsdk:"spa"`
	URL           types.String `tfsdk:"url"`
}

type websiteState struct {
	Enabled       bool   `json:"enabled"`
	URL           string `json:"url"`
	IndexDocument string `json:"index_document"`
	ErrorDocument string `json:"error_document"`
	SPA           bool   `json:"spa"`
}

func (r *bucketWebsiteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_website"
}

func (r *bucketWebsiteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Publishes a bucket as a static website (https://<bucket>-<service code>.web.gyro-s3.com and connected custom domains). " +
			"Creating this resource accepts the abuse rules: every object in the bucket becomes publicly readable. Destroy disables the website (the bucket and its data stay).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Object storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Bucket name."},
			"index_document": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("index.html"),
				Description: "Entry file name served at the root and in every folder (file name only, no /). Default index.html."},
			"error_document": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				Description: "Object key of the 404 page (e.g. 404.html). Empty = plain 404. Not used while spa is true."},
			"spa": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Single-page app mode: every missing path returns the entry file with status 200 (client-side routing, page reloads). Real files are served as they are."},
			"url": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Website address."},
		},
	}
}

func (r *bucketWebsiteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *bucketWebsiteResource) get(ctx context.Context, m *bucketWebsiteModel) (*websiteState, error) {
	var out struct {
		Website websiteState `json:"website"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, bucketPath(m.ServiceID, m.Bucket, "website"), nil, nil, "", &out); err != nil {
		return nil, err
	}
	return &out.Website, nil
}

func (r *bucketWebsiteResource) apply(m *bucketWebsiteModel, w *websiteState) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	m.IndexDocument = types.StringValue(w.IndexDocument)
	m.ErrorDocument = types.StringValue(w.ErrorDocument)
	m.SPA = types.BoolValue(w.SPA)
	m.URL = types.StringValue(w.URL)
}

func (r *bucketWebsiteResource) put(ctx context.Context, m *bucketWebsiteModel) error {
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "website"), map[string]any{
		"index_document": m.IndexDocument.ValueString(), "error_document": m.ErrorDocument.ValueString(), "spa": m.SPA.ValueBool(), "accept": true,
	})
	return err
}

func (r *bucketWebsiteResource) save(ctx context.Context, m *bucketWebsiteModel, d interface{ AddError(string, string) }) bool {
	if err := r.put(ctx, m); err != nil {
		d.AddError("Saving website failed", err.Error())
		return false
	}
	w, err := r.get(ctx, m)
	if err != nil {
		d.AddError("Reading website failed", err.Error())
		return false
	}
	r.apply(m, w)
	return true
}

func (r *bucketWebsiteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketWebsiteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	w, err := r.get(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "tool_action_failed") || (err == nil && !w.Enabled) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading website failed", err.Error())
		return
	}
	r.apply(&m, w)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketWebsiteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m bucketWebsiteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketWebsiteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketWebsiteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, bucketPath(m.ServiceID, m.Bucket, "website"), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Disabling website failed", err.Error())
	}
}

func (r *bucketWebsiteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importServiceBucket(ctx, req, resp)
}

/* ================================================================== gyro_storage_bucket_cors */

type bucketCorsResource struct{ pd *providerData }

func newBucketCorsResource() resource.Resource { return &bucketCorsResource{} }

type corsRuleModel struct {
	ID             types.String `tfsdk:"id"`
	AllowedOrigins types.List   `tfsdk:"allowed_origins"`
	AllowedMethods types.List   `tfsdk:"allowed_methods"`
	AllowedHeaders types.List   `tfsdk:"allowed_headers"`
	ExposeHeaders  types.List   `tfsdk:"expose_headers"`
	MaxAgeSeconds  types.Int64  `tfsdk:"max_age_seconds"`
}

type bucketCorsModel struct {
	ID        types.String    `tfsdk:"id"`
	ServiceID types.Int64     `tfsdk:"service_id"`
	Bucket    types.String    `tfsdk:"bucket"`
	Rules     []corsRuleModel `tfsdk:"rules"`
}

type corsRuleJSON struct {
	ID             *string  `json:"id"`
	AllowedOrigins []string `json:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers"`
	ExposeHeaders  []string `json:"expose_headers"`
	MaxAgeSeconds  int64    `json:"max_age_seconds"`
}

func (r *bucketCorsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_bucket_cors"
}

func (r *bucketCorsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "CORS rules of a bucket (AWS S3 semantics). The resource owns all rules of the bucket: apply replaces them, destroy removes them. " +
			"Rules apply to the S3 endpoint and to the static website / custom domains. Limits: 100 rules, 50 origins/headers per rule, methods GET PUT POST DELETE HEAD (upper case), max_age_seconds 0-86400.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Object storage service id."},
			"bucket":     schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Bucket name."},
			"rules": schema.ListNestedAttribute{Required: true, Description: "Rules in order.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":              schema.StringAttribute{Optional: true, Description: "Rule label (optional, unique)."},
					"allowed_origins": schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "https://www.example.com, https://*.example.com, http://localhost:3000 or * (lower case)."},
					"allowed_methods": schema.ListAttribute{Required: true, ElementType: types.StringType, Description: "GET, PUT, POST, DELETE, HEAD."},
					"allowed_headers": schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "Request headers allowed in preflight, e.g. [\"*\"]."},
					"expose_headers":  schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "Response headers readable by the browser, e.g. [\"ETag\"]."},
					"max_age_seconds": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Preflight cache time (0 = not sent)."},
				}}},
		},
	}
}

func (r *bucketCorsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func corsStrs(ctx context.Context, l types.List) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	_ = l.ElementsAs(ctx, &out, false)
	return out
}

// listOrNull keeps "not set" as null so an omitted optional list does not drift against the API's [].
func listOrNull(v []string, prev types.List) types.List {
	if len(v) == 0 && (prev.IsNull() || prev.IsUnknown()) {
		return types.ListNull(types.StringType)
	}
	els := make([]string, len(v))
	copy(els, v)
	l, _ := types.ListValueFrom(context.Background(), types.StringType, els)
	return l
}

func (r *bucketCorsResource) get(ctx context.Context, m *bucketCorsModel) ([]corsRuleJSON, error) {
	var out struct {
		Rules []corsRuleJSON `json:"rules"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, bucketPath(m.ServiceID, m.Bucket, "cors"), nil, nil, "", &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

func (r *bucketCorsResource) apply(m *bucketCorsModel, rules []corsRuleJSON) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Bucket.ValueString()))
	prev := m.Rules
	out := make([]corsRuleModel, len(rules))
	for i, x := range rules {
		var p corsRuleModel
		if i < len(prev) {
			p = prev[i]
		} else {
			p = corsRuleModel{AllowedHeaders: types.ListNull(types.StringType), ExposeHeaders: types.ListNull(types.StringType)}
		}
		o := corsRuleModel{ID: types.StringNull(), MaxAgeSeconds: types.Int64Value(x.MaxAgeSeconds)}
		if x.ID != nil && *x.ID != "" {
			o.ID = types.StringValue(*x.ID)
		}
		o.AllowedOrigins = listOrNull(x.AllowedOrigins, types.ListUnknown(types.StringType))
		o.AllowedMethods = listOrNull(x.AllowedMethods, types.ListUnknown(types.StringType))
		o.AllowedHeaders = listOrNull(x.AllowedHeaders, p.AllowedHeaders)
		o.ExposeHeaders = listOrNull(x.ExposeHeaders, p.ExposeHeaders)
		out[i] = o
	}
	m.Rules = out
}

func (r *bucketCorsResource) put(ctx context.Context, m *bucketCorsModel) error {
	rules := make([]map[string]any, 0, len(m.Rules))
	for _, x := range m.Rules {
		o := map[string]any{"allowed_origins": corsStrs(ctx, x.AllowedOrigins), "allowed_methods": corsStrs(ctx, x.AllowedMethods),
			"allowed_headers": corsStrs(ctx, x.AllowedHeaders), "expose_headers": corsStrs(ctx, x.ExposeHeaders), "max_age_seconds": x.MaxAgeSeconds.ValueInt64()}
		if !x.ID.IsNull() && !x.ID.IsUnknown() && x.ID.ValueString() != "" {
			o["id"] = x.ID.ValueString()
		}
		rules = append(rules, o)
	}
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, bucketPath(m.ServiceID, m.Bucket, "cors"), map[string]any{"rules": rules})
	return err
}

func (r *bucketCorsResource) save(ctx context.Context, m *bucketCorsModel, d interface{ AddError(string, string) }) bool {
	if len(m.Rules) == 0 {
		d.AddError("No CORS rules", "rules must contain at least one rule (destroy the resource to remove CORS).")
		return false
	}
	if err := r.put(ctx, m); err != nil {
		d.AddError("Saving CORS rules failed", err.Error())
		return false
	}
	rules, err := r.get(ctx, m)
	if err != nil {
		d.AddError("Reading CORS rules failed", err.Error())
		return false
	}
	r.apply(m, rules)
	return true
}

func (r *bucketCorsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m bucketCorsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketCorsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m bucketCorsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	rules, err := r.get(ctx, &m)
	if client.IsNotFound(err) || client.HasCode(err, "tool_action_failed") || (err == nil && len(rules) == 0) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading CORS rules failed", err.Error())
		return
	}
	r.apply(&m, rules)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketCorsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m bucketCorsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &m, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *bucketCorsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m bucketCorsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, bucketPath(m.ServiceID, m.Bucket, "cors"), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Removing CORS rules failed", err.Error())
	}
}

func (r *bucketCorsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importServiceBucket(ctx, req, resp)
}
