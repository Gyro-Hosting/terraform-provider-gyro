package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Managed DNS (Gyro DNS, ns1/ns2.gyrodns.net). API: /cloud/dns/{service}/zones/... (typed tool routes, GyroAPI >= 2.5.0).

var (
	_ resource.ResourceWithImportState = (*dnsZoneResource)(nil)
	_ resource.ResourceWithImportState = (*dnsRRsetResource)(nil)
)

type dnsZoneRow struct {
	Name     string  `json:"name"`
	State    string  `json:"state"`
	TXTName  string  `json:"txt_name"`
	TXTValue *string `json:"txt_value"`
}

type dnsRRset struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int64    `json:"ttl"`
	Records []string `json:"records"`
}

type dnsDS struct {
	DS []string `json:"ds"`
}

func dnsZones(ctx context.Context, c *client.Client, sid int64) ([]dnsZoneRow, []string, error) {
	var r struct {
		Items       []dnsZoneRow `json:"items"`
		Nameservers []string     `json:"nameservers"`
	}
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/dns/%d/zones", sid), nil, nil, "", &r)
	return r.Items, r.Nameservers, err
}

func dnsZonePath(sid int64, zone string) string {
	return fmt.Sprintf("cloud/dns/%d/zones/%s", sid, url.PathEscape(strings.TrimSuffix(strings.ToLower(zone), ".")))
}

/* ================================================================== gyro_dns_zone */

type dnsZoneResource struct{ pd *providerData }

func newDNSZoneResource() resource.Resource { return &dnsZoneResource{} }

type dnsZoneModel struct {
	ID          types.String `tfsdk:"id"`
	ServiceID   types.Int64  `tfsdk:"service_id"`
	Name        types.String `tfsdk:"name"`
	ZoneFile    types.String `tfsdk:"zone_file"`
	DNSSEC      types.Bool   `tfsdk:"dnssec"`
	Status      types.String `tfsdk:"status"`
	TXTName     types.String `tfsdk:"verification_txt_name"`
	TXTValue    types.String `tfsdk:"verification_txt_value"`
	Nameservers types.List   `tfsdk:"nameservers"`
	DS          types.List   `tfsdk:"ds_records"`
}

func (r *dnsZoneResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_zone"
}

func (r *dnsZoneResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	kl := []planmodifier.List{listplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A zone on Gyro DNS (ns1.gyrodns.net / ns2.gyrodns.net). The zone is published after ownership is verified: delegate the domain to " +
			"the name servers (immediate if already delegated) or create the TXT record `verification_txt_name` = `verification_txt_value` at your current DNS. " +
			"`status` is `active` or `pending`. Destroy removes the zone and its records.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Gyro DNS service id."},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Zone (domain) name."},
			"zone_file": schema.StringAttribute{Optional: true, Description: "Optional BIND zone file imported once when the zone is created (SOA and apex NS are ignored). " +
				"Later changes are not applied; manage records with gyro_dns_record."},
			"dnssec": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Sign the zone. Enter `ds_records` at your registrar afterwards; remove them there before disabling."},
			"status":                 schema.StringAttribute{Computed: true, Description: "active or pending (ownership not verified yet)."},
			"verification_txt_name":  schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"verification_txt_value": schema.StringAttribute{Computed: true, Description: "TXT value proving ownership (pending zones; empty once active)."},
			"nameservers":            schema.ListAttribute{ElementType: types.StringType, Computed: true, PlanModifiers: kl},
			"ds_records":             schema.ListAttribute{ElementType: types.StringType, Computed: true, Description: "DS records for the registrar (when dnssec = true)."},
		},
	}
}

func (r *dnsZoneResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *dnsZoneResource) refresh(ctx context.Context, m *dnsZoneModel) (bool, error) {
	name := strings.TrimSuffix(strings.ToLower(m.Name.ValueString()), ".")
	items, ns, err := dnsZones(ctx, r.pd.c, sid(m.ServiceID))
	if err != nil {
		return false, err
	}
	for _, z := range items {
		if z.Name != name {
			continue
		}
		m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), z.Name))
		m.Status = types.StringValue(z.State)
		m.TXTName = types.StringValue(z.TXTName)
		v := ""
		if z.TXTValue != nil {
			v = *z.TXTValue
		}
		m.TXTValue = types.StringValue(v)
		m.Nameservers, _ = types.ListValueFrom(ctx, types.StringType, ns)
		ds := []string{}
		m.DNSSEC = types.BoolValue(false)
		if z.State == "active" {
			var d struct {
				DNSSEC bool    `json:"dnssec"`
				DS     []dnsDS `json:"ds"`
			}
			if _, err := r.pd.c.Do(ctx, http.MethodGet, dnsZonePath(sid(m.ServiceID), z.Name)+"/dnssec", nil, nil, "", &d); err != nil {
				return false, err
			}
			m.DNSSEC = types.BoolValue(d.DNSSEC)
			for _, k := range d.DS {
				ds = append(ds, k.DS...)
			}
		}
		m.DS, _ = types.ListValueFrom(ctx, types.StringType, ds)
		return true, nil
	}
	return false, nil
}

func (r *dnsZoneResource) setDNSSEC(ctx context.Context, m *dnsZoneModel, on bool) error {
	p := dnsZonePath(sid(m.ServiceID), m.Name.ValueString()) + "/dnssec"
	if on {
		_, err := r.pd.c.TypedWrite(ctx, http.MethodPost, p, map[string]any{})
		return err
	}
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, p, map[string]any{"confirm": true})
	return err
}

func (r *dnsZoneResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dnsZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := map[string]any{"name": m.Name.ValueString()}
	if !m.ZoneFile.IsNull() && m.ZoneFile.ValueString() != "" {
		b["bind"] = m.ZoneFile.ValueString()
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, fmt.Sprintf("cloud/dns/%d/zones", sid(m.ServiceID)), b); err != nil {
		resp.Diagnostics.AddError("Creating DNS zone failed", err.Error())
		return
	}
	want := m.DNSSEC.ValueBool()
	if ok, err := r.refresh(ctx, &m); err != nil || !ok {
		resp.Diagnostics.AddError("DNS zone not found after create", fmt.Sprint(err))
		return
	}
	if want && m.Status.ValueString() == "active" {
		if err := r.setDNSSEC(ctx, &m, true); err != nil {
			resp.Diagnostics.AddError("Enabling DNSSEC failed", err.Error())
			return
		}
		_, _ = r.refresh(ctx, &m)
	} else if want {
		resp.Diagnostics.AddWarning("DNSSEC not enabled yet", "The zone is pending ownership verification; run apply again once it is active.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsZoneResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dnsZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	ok, err := r.refresh(ctx, &m)
	if client.IsNotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading DNS zone failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsZoneResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st dnsZoneModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := m.DNSSEC.ValueBool()
	if want != st.DNSSEC.ValueBool() {
		if st.Status.ValueString() != "active" {
			resp.Diagnostics.AddError("Zone is not active", "DNSSEC can be changed after ownership verification.")
			return
		}
		if err := r.setDNSSEC(ctx, &m, want); err != nil {
			resp.Diagnostics.AddError("Changing DNSSEC failed", err.Error())
			return
		}
	}
	if _, err := r.refresh(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading DNS zone failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsZoneResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dnsZoneModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, dnsZonePath(sid(m.ServiceID), m.Name.ValueString()), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) && !client.HasCode(err, "tool_action_failed") {
		resp.Diagnostics.AddError("Deleting DNS zone failed", err.Error())
	}
}

// Import ID: <service_id>/<zone>
func (r *dnsZoneResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || p[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<zone>, e.g. 123/example.com")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), strings.ToLower(p[1]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

/* ================================================================== gyro_dns_record (one record set: name + type) */

type dnsRRsetResource struct{ pd *providerData }

func newDNSRRsetResource() resource.Resource { return &dnsRRsetResource{} }

type dnsRRsetModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	Zone      types.String `tfsdk:"zone"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	TTL       types.Int64  `tfsdk:"ttl"`
	Records   types.Set    `tfsdk:"records"`
	FQDN      types.String `tfsdk:"fqdn"`
}

func (r *dnsRRsetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record"
}

func (r *dnsRRsetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A record set (all values of one name + type) in a Gyro DNS zone. The zone must be active (see gyro_dns_zone.status).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI},
			"zone":       schema.StringAttribute{Required: true, PlanModifiers: replaceS},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Relative to the zone: \"@\" = apex, \"www\", \"*.app\"."},
			"type":       schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "A, AAAA, CNAME, MX, TXT, SRV, CAA, ALIAS, NS (not at the apex) or PTR."},
			"ttl":        schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(3600), Description: "60-86400 seconds."},
			"records": schema.SetAttribute{ElementType: types.StringType, Required: true,
				Description: "Values in zone file syntax, e.g. [\"10 mx1.example.com.\"] for MX. TXT quotes are optional; names without a trailing dot are relative to the zone."},
			"fqdn": schema.StringAttribute{Computed: true, PlanModifiers: keep},
		},
	}
}

func (r *dnsRRsetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func dnsFQDN(name, zone string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	z := strings.TrimSuffix(strings.ToLower(zone), ".")
	switch {
	case n == "" || n == "@":
		return z + "."
	case strings.HasSuffix(n, "."):
		return n
	case n == z || strings.HasSuffix(n, "."+z):
		return n + "."
	}
	return n + "." + z + "."
}

// dnsNorm makes a value comparable with the server's canonical form (absolute names, quoted TXT).
func dnsNorm(typ, v, zone string) string {
	v = strings.TrimSpace(v)
	z := strings.TrimSuffix(strings.ToLower(zone), ".")
	abs := func(n string) string {
		if n == "@" {
			return z
		}
		if strings.HasSuffix(n, ".") {
			return strings.TrimSuffix(strings.ToLower(n), ".")
		}
		return strings.ToLower(n) + "." + z
	}
	f := strings.Fields(v)
	switch strings.ToUpper(typ) {
	case "TXT":
		s := strings.ReplaceAll(v, "\" \"", "")
		return strings.Trim(s, "\"")
	case "CNAME", "ALIAS", "NS", "PTR":
		if len(f) == 1 {
			return abs(f[0])
		}
	case "MX":
		if len(f) == 2 {
			return f[0] + " " + abs(f[1])
		}
	case "SRV":
		if len(f) == 4 {
			return f[0] + " " + f[1] + " " + f[2] + " " + abs(f[3])
		}
	case "AAAA", "A":
		return strings.ToLower(v)
	}
	return v
}

func (r *dnsRRsetResource) find(ctx context.Context, m *dnsRRsetModel) (*dnsRRset, error) {
	var d struct {
		RRsets []dnsRRset `json:"rrsets"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, dnsZonePath(sid(m.ServiceID), m.Zone.ValueString())+"/records", nil, nil, "", &d); err != nil {
		return nil, err
	}
	fq := dnsFQDN(m.Name.ValueString(), m.Zone.ValueString())
	for _, x := range d.RRsets {
		if x.Name == fq && strings.EqualFold(x.Type, m.Type.ValueString()) {
			return &x, nil
		}
	}
	return nil, nil
}

// apply keeps the configured spelling when the server returns the same values in canonical form.
func (r *dnsRRsetResource) apply(ctx context.Context, m *dnsRRsetModel, x *dnsRRset) {
	var cur []string
	_ = m.Records.ElementsAs(ctx, &cur, false)
	zone := m.Zone.ValueString()
	norm := func(vals []string) []string {
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			out = append(out, dnsNorm(m.Type.ValueString(), v, zone))
		}
		sort.Strings(out)
		return out
	}
	a, b := norm(cur), norm(x.Records)
	same := len(a) == len(b)
	for i := 0; same && i < len(a); i++ {
		same = a[i] == b[i]
	}
	if !same {
		vals := make([]string, 0, len(x.Records))
		for _, v := range x.Records {
			// TXT: a single quoted string without inner quotes is stored unquoted (the way it is usually written in configs)
			if strings.EqualFold(m.Type.ValueString(), "TXT") && len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' && !strings.ContainsAny(v[1:len(v)-1], "\"\\") {
				v = v[1 : len(v)-1]
			}
			vals = append(vals, v)
		}
		m.Records, _ = types.SetValueFrom(ctx, types.StringType, vals)
	}
	m.TTL = types.Int64Value(x.TTL)
	m.FQDN = types.StringValue(x.Name)
	m.ID = types.StringValue(fmt.Sprintf("%d/%s/%s/%s", sid(m.ServiceID), strings.TrimSuffix(strings.ToLower(zone), "."), m.Name.ValueString(), strings.ToUpper(m.Type.ValueString())))
}

func (r *dnsRRsetResource) put(ctx context.Context, m *dnsRRsetModel) error {
	var vals []string
	_ = m.Records.ElementsAs(ctx, &vals, false)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, dnsZonePath(sid(m.ServiceID), m.Zone.ValueString())+"/records", map[string]any{
		"name": m.Name.ValueString(), "type": strings.ToUpper(m.Type.ValueString()), "ttl": m.TTL.ValueInt64(), "records": vals})
	return err
}

func (r *dnsRRsetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dnsRRsetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if x, err := r.find(ctx, &m); err == nil && x != nil {
		resp.Diagnostics.AddError("Record set already exists", "Import it: terraform import <address> "+fmt.Sprintf("%d/%s/%s/%s", sid(m.ServiceID), m.Zone.ValueString(), m.Name.ValueString(), strings.ToUpper(m.Type.ValueString())))
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Creating DNS record failed", err.Error())
		return
	}
	x, err := r.find(ctx, &m)
	if err != nil || x == nil {
		resp.Diagnostics.AddError("DNS record not found after create", fmt.Sprint(err))
		return
	}
	r.apply(ctx, &m, x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRRsetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dnsRRsetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	x, err := r.find(ctx, &m)
	if client.IsNotFound(err) || (err == nil && x == nil) || client.HasCode(err, "tool_action_failed") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading DNS record failed", err.Error())
		return
	}
	r.apply(ctx, &m, x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRRsetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m dnsRRsetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Updating DNS record failed", err.Error())
		return
	}
	x, err := r.find(ctx, &m)
	if err != nil || x == nil {
		resp.Diagnostics.AddError("DNS record not found after update", fmt.Sprint(err))
		return
	}
	r.apply(ctx, &m, x)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsRRsetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dnsRRsetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, dnsZonePath(sid(m.ServiceID), m.Zone.ValueString())+"/records",
		map[string]any{"name": m.Name.ValueString(), "type": strings.ToUpper(m.Type.ValueString()), "confirm": true})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting DNS record failed", err.Error())
	}
}

// Import ID: <service_id>/<zone>/<name>/<type>, e.g. 123/example.com/www/A (name @ = apex)
func (r *dnsRRsetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.Split(req.ID, "/")
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 4 || err != nil || p[1] == "" || p[2] == "" || p[3] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<zone>/<name>/<type>, e.g. 123/example.com/www/A")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("zone"), strings.ToLower(p[1]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), p[2])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), strings.ToUpper(p[3]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("records"), types.SetValueMust(types.StringType, nil))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
