package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*dnsAcmeKeyResource)(nil)
	_ resource.ResourceWithImportState = (*dnsAcmeKeyResource)(nil)
)

type dnsAcmeKeyResource struct{ pd *providerData }

func newDNSAcmeKeyResource() resource.Resource { return &dnsAcmeKeyResource{} }

type dnsAcmeKeyModel struct {
	ID          types.String `tfsdk:"id"`
	ServiceID   types.Int64  `tfsdk:"service_id"`
	Name        types.String `tfsdk:"name"`
	Zones       types.Set    `tfsdk:"zones"`
	IPs         types.Set    `tfsdk:"ips"`
	Token       types.String `tfsdk:"token"`
	TokenPrefix types.String `tfsdk:"token_prefix"`
}

func (r *dnsAcmeKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_acme_key"
}

func (r *dnsAcmeKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	rs := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	rset := []planmodifier.Set{setplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "An ACME key of a Gyro DNS service: it can only add and remove _acme-challenge TXT records (Let's Encrypt DNS-01, " +
			"wildcard certificates), optionally only in `zones` and only from `ips`. Use the token with acme.sh, Certbot, Caddy, Traefik or " +
			"cert-manager. Every argument forces a new key. The token is known only right after creation (not after import). Requires API 2.13.0.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}, Description: "Gyro DNS service id."},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: rs, Description: "Label, 1-40 characters."},
			"zones": schema.SetAttribute{Optional: true, ElementType: types.StringType, PlanModifiers: rset,
				Description: "Active zones of the service the key may write in (empty = all zones)."},
			"ips": schema.SetAttribute{Optional: true, ElementType: types.StringType, PlanModifiers: rset,
				Description: "Client IP addresses allowed to use the key (empty = any)."},
			"token":        schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep, Description: "The key (secret)."},
			"token_prefix": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "First characters of the key (for display)."},
		},
	}
}

func (r *dnsAcmeKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
		return
	}
	r.pd = pd
}

func setStrings(ctx context.Context, s types.Set) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	s.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out
}

func (r *dnsAcmeKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dnsAcmeKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.pd.c.CreateAcmeKey(ctx, m.ServiceID.ValueInt64(), m.Name.ValueString(), setStrings(ctx, m.Zones), setStrings(ctx, m.IPs))
	if err != nil {
		resp.Diagnostics.AddError("Creating ACME key failed", err.Error())
		return
	}
	m.ID = types.StringValue(strconv.FormatInt(k.ID, 10))
	m.Token = types.StringValue(k.Token)
	m.TokenPrefix = types.StringValue(k.TokenPrefix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *dnsAcmeKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dnsAcmeKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	keys, err := r.pd.c.AcmeKeys(ctx, m.ServiceID.ValueInt64())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading ACME keys failed", err.Error())
		return
	}
	for _, k := range keys {
		if k.ID != id {
			continue
		}
		m.Name = types.StringValue(k.Name)
		m.TokenPrefix = types.StringValue(k.TokenPrefix)
		if len(k.Zones) > 0 || !m.Zones.IsNull() {
			m.Zones, _ = types.SetValueFrom(ctx, types.StringType, k.Zones)
		}
		if len(k.IPs) > 0 || !m.IPs.IsNull() {
			m.IPs, _ = types.SetValueFrom(ctx, types.StringType, k.IPs)
		}
		if m.Token.IsNull() || m.Token.IsUnknown() {
			m.Token = types.StringValue("")
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *dnsAcmeKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_dns_acme_key arguments force replacement.")
}

func (r *dnsAcmeKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dnsAcmeKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteAcmeKey(ctx, m.ServiceID.ValueInt64(), id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting ACME key failed", err.Error())
	}
}

// ImportState: "<service_id>/<key_id>" (the token cannot be recovered).
func (r *dnsAcmeKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	sid, err1 := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err1 != nil {
		resp.Diagnostics.AddError("Invalid import id", "use <service_id>/<key_id>")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &dnsAcmeKeyModel{ID: types.StringValue(p[1]), ServiceID: types.Int64Value(sid),
		Zones: types.SetNull(types.StringType), IPs: types.SetNull(types.StringType), Token: types.StringValue(""), Name: types.StringNull(), TokenPrefix: types.StringNull()})...)
}
