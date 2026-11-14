package provider

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// Gyro VPC (private networks of Gyro Cloud Istanbul, tr-ist-1). Gyro Public API 2.29.0:
//   /cloud/vpcs, /cloud/vpcs/{id}, /cloud/vpcs/{id}/subnets, /cloud/vpcs/{id}/gateway, /cloud/vpcs/{id}/port-forwards,
//   /cloud/servers/{id}/nics (202 + task; state attaching|ready|detaching|error).

var (
	_ resource.ResourceWithImportState = (*vpcResource)(nil)
	_ resource.ResourceWithImportState = (*subnetResource)(nil)
	_ resource.ResourceWithImportState = (*serverNICResource)(nil)
	_ resource.ResourceWithImportState = (*portForwardResource)(nil)
)

// nicWaitTimeout: how long attach / detach of a network card is awaited.
var nicWaitTimeout = 5 * time.Minute

/* ------------------------------------------------------------------ API views */

// apiID decodes an id the API may send as a number or as a string.
type apiID string

func (x *apiID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*x = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		s, err := strconv.Unquote(string(b))
		if err != nil {
			return err
		}
		*x = apiID(s)
		return nil
	}
	*x = apiID(string(b))
	return nil
}

// idBody sends a numeric id as a JSON number, anything else as a string.
func idBody(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return s
}

type vpcPFView struct {
	ID         apiID  `json:"id"`
	Protocol   string `json:"protocol"`
	PublicPort int64  `json:"public_port"`
	TargetIP   string `json:"target_ip"`
	TargetPort int64  `json:"target_port"`
}

type vpcGatewayView struct {
	Enabled      bool        `json:"enabled"`
	NAT          bool        `json:"nat"`
	InterSubnet  string      `json:"inter_subnet"`
	PublicIP     string      `json:"public_ip"`
	Status       string      `json:"status"`
	PortForwards []vpcPFView `json:"port_forwards"`
	MonthlyEUR   float64     `json:"monthly_eur"`
	HourlyEUR    float64     `json:"hourly_eur"`
}

type vpcSubnetView struct {
	ID        apiID  `json:"id"`
	VPCID     apiID  `json:"vpc_id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	GatewayIP string `json:"gateway_ip"`
	Used      int64  `json:"used"`
	Capacity  int64  `json:"capacity"`
	State     string `json:"state"`
}

type vpcView struct {
	ID        apiID           `json:"id"`
	Name      string          `json:"name"`
	CIDR      string          `json:"cidr"`
	Region    string          `json:"region"`
	ProjectID apiID           `json:"project_id"`
	State     string          `json:"state"`
	Subnets   []vpcSubnetView `json:"subnets"`
	Servers   []struct {
		ServerID apiID  `json:"server_id"`
		Name     string `json:"name"`
		NICID    apiID  `json:"nic_id"`
	} `json:"servers"`
	Gateway vpcGatewayView `json:"gateway"`
}

type nicView struct {
	ID         apiID  `json:"id"`
	VPCID      apiID  `json:"vpc_id"`
	SubnetID   apiID  `json:"subnet_id"`
	SubnetName string `json:"subnet_name"`
	SubnetCIDR string `json:"subnet_cidr"`
	IP         string `json:"ip"`
	GatewayIP  string `json:"gateway_ip"`
	MAC        string `json:"mac"`
	State      string `json:"state"`
	Error      string `json:"error"`
}

func vpcPath(id string, sub ...string) string {
	p := "cloud/vpcs/" + url.PathEscape(id)
	for _, s := range sub {
		p += "/" + s
	}
	return p
}

func pidOf(s apiID) int64 { n, _ := strconv.ParseInt(string(s), 10, 64); return n }

// defaultSubnet: the subnet the API opened with the VPC ("default"), else the first one.
func defaultSubnet(v *vpcView) string {
	for _, s := range v.Subnets {
		if s.Name == "default" {
			return string(s.ID)
		}
	}
	if len(v.Subnets) > 0 {
		return string(v.Subnets[0].ID)
	}
	return ""
}

// vpcErr adds what to do for the VPC error codes a user can fix.
func vpcErr(err error) string {
	hint := ""
	switch {
	case client.HasCode(err, "vpc_in_use"):
		hint = "The VPC still has server network cards (gyro_server_nic) or port forwards (gyro_port_forward) - remove them first, then destroy the VPC."
	case client.HasCode(err, "subnet_in_use"):
		hint = "Servers are still attached to this subnet - remove their gyro_server_nic first."
	case client.HasCode(err, "last_subnet"):
		hint = "A VPC keeps at least one subnet; the last one goes away together with the VPC."
	case client.HasCode(err, "gateway_disabled"):
		hint = "Port forwards need the VPC gateway: set gateway_enabled = true on the gyro_vpc."
	case client.HasCode(err, "gateway_in_use"):
		hint = "The gateway still has port forwards - remove the gyro_port_forward resources first."
	case client.HasCode(err, "no_egress"):
		hint = "A server of this VPC has its public IP turned off and reaches the internet only through the gateway (NAT). Turn its public IP back on first (gyro server public-ip on) or detach it."
	case client.HasCode(err, "other_vpc"):
		hint = "A server can be in one VPC at a time (several subnets of that VPC are fine)."
	case client.HasCode(err, "not_supported"):
		hint = "VPCs are available only for Gyro Cloud Istanbul (tr-ist-1) servers."
	}
	if hint == "" {
		return err.Error()
	}
	return err.Error() + "\n" + hint
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// canonicalCIDR refuses host bits (10.10.0.1/16): the API stores the network address, so plans would never settle.
type canonicalCIDR struct{}

func (canonicalCIDR) Description(context.Context) string {
	return "IPv4 network in CIDR notation (network address)"
}
func (canonicalCIDR) MarkdownDescription(context.Context) string {
	return "IPv4 network in CIDR notation (network address)"
}
func (canonicalCIDR) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	ip, n, err := net.ParseCIDR(s)
	if err != nil || ip.To4() == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", fmt.Sprintf("%q is not an IPv4 network such as 10.10.0.0/16.", s))
		return
	}
	if !ip.Equal(n.IP) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid CIDR", fmt.Sprintf("%q has host bits set; use the network address %s.", s, n.String()))
	}
}

type ipv4Only struct{}

func (ipv4Only) Description(context.Context) string         { return "IPv4 address" }
func (ipv4Only) MarkdownDescription(context.Context) string { return "IPv4 address" }
func (ipv4Only) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if ip := net.ParseIP(req.ConfigValue.ValueString()); ip == nil || ip.To4() == nil || strings.Contains(req.ConfigValue.ValueString(), ":") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP address", fmt.Sprintf("%q is not an IPv4 address.", req.ConfigValue.ValueString()))
	}
}

var (
	replaceIfSetS = []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()}
	keepI         = []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
)

func splitImport(id, want string, resp *resource.ImportStateResponse) (string, string, bool) {
	p := strings.SplitN(id, "/", 2)
	if len(p) != 2 || strings.TrimSpace(p[0]) == "" || strings.TrimSpace(p[1]) == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use "+want)
		return "", "", false
	}
	return p[0], p[1], true
}

/* ================================================================== gyro_vpc */

type vpcResource struct{ pd *providerData }

func newVPCResource() resource.Resource { return &vpcResource{} }

type vpcModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	CIDR            types.String `tfsdk:"cidr"`
	ProjectID       types.Int64  `tfsdk:"project_id"`
	GatewayEnabled  types.Bool   `tfsdk:"gateway_enabled"`
	GatewayNAT      types.Bool   `tfsdk:"gateway_nat"`
	InterSubnet     types.String `tfsdk:"inter_subnet"`
	Region          types.String `tfsdk:"region"`
	GatewayPublicIP types.String `tfsdk:"gateway_public_ip"`
	DefaultSubnetID types.String `tfsdk:"default_subnet_id"`
}

func (r *vpcResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (r *vpcResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A private network (VPC) for Gyro Cloud Istanbul (tr-ist-1) servers. The VPC, its subnets and private traffic are free. " +
			"Creating a VPC also creates its first subnet (\"default\", a /24 inside cidr) - its id is default_subnet_id; add more with gyro_subnet " +
			"(up to 4 per VPC by default). Servers join with gyro_server_nic. Default limit: 3 VPCs per account (raisable with a support ticket). " +
			"Destroy needs the VPC to be empty: remove its gyro_server_nic and gyro_port_forward resources first (Terraform does this when they reference the VPC).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "VPC id."},
			"name": schema.StringAttribute{Required: true, Description: "VPC name (unique per account). Changing it renames the VPC in place.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 48)}},
			"cidr": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Validators: []validator.String{canonicalCIDR{}},
				Description: "Private IPv4 range of the VPC, /16 to /24 inside 10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16 (e.g. 10.10.0.0/16). " +
					"10.42/16, 10.43/16, 10.200/16, 10.250/16 and 10.255/16 are reserved. Changing it replaces the VPC."},
			"project_id": schema.Int64Attribute{Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplaceIfConfigured()},
				Description:   "Project of the VPC (gyro_project id, 0 = default project). Changing it replaces the VPC."},
			"gateway_enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Turn on the VPC gateway (NAT): a public IPv4 (gateway_public_ip) through which servers without a public IP reach the internet, " +
					"plus port forwarding (gyro_port_forward). The gateway has hourly prepaid billing from the account balance while enabled: " +
					"4.99 EUR/month cap (672 hours), at least 1 hour each time it is turned on. Outgoing connections through the gateway are logged as required by law."},
			"gateway_nat": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Source NAT for internet access through the gateway (default true). false = the gateway only forwards ports."},
			"inter_subnet": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("allow"),
				Validators:  []validator.String{stringvalidator.OneOf("allow", "deny")},
				Description: "allow (default) = subnets of this VPC reach each other; deny = traffic between subnets is blocked."},
			"region":            schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Always tr-ist-1."},
			"gateway_public_ip": schema.StringAttribute{Computed: true, Description: "Public IPv4 of the gateway while it is enabled (empty when off)."},
			"default_subnet_id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Id of the subnet created with the VPC (\"default\")."},
		},
	}
}

func (r *vpcResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *vpcResource) fill(m *vpcModel, v *vpcView) {
	m.ID = types.StringValue(string(v.ID))
	m.Name = types.StringValue(v.Name)
	m.CIDR = types.StringValue(v.CIDR)
	m.ProjectID = types.Int64Value(pidOf(v.ProjectID))
	m.GatewayEnabled = types.BoolValue(v.Gateway.Enabled)
	m.GatewayNAT = types.BoolValue(v.Gateway.NAT)
	is := v.Gateway.InterSubnet
	if is == "" {
		is = "allow"
	}
	m.InterSubnet = types.StringValue(is)
	m.Region = types.StringValue(v.Region)
	m.GatewayPublicIP = types.StringValue(v.Gateway.PublicIP)
	if !v.Gateway.Enabled {
		m.GatewayPublicIP = types.StringValue("")
	}
	keepDefault := false
	if !m.DefaultSubnetID.IsNull() && !m.DefaultSubnetID.IsUnknown() {
		for _, s := range v.Subnets {
			if string(s.ID) == m.DefaultSubnetID.ValueString() {
				keepDefault = true
			}
		}
	}
	if !keepDefault {
		m.DefaultSubnetID = types.StringValue(defaultSubnet(v))
	}
}

func (r *vpcResource) get(ctx context.Context, id string) (*vpcView, error) {
	var v vpcView
	if _, err := r.pd.c.Do(ctx, http.MethodGet, vpcPath(id), nil, nil, "", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *vpcResource) putGateway(ctx context.Context, id string, enabled, nat bool, inter string) error {
	body := map[string]any{"enabled": enabled, "nat": nat, "inter_subnet": inter}
	_, err := r.pd.c.Do(ctx, http.MethodPut, vpcPath(id, "gateway"), nil, body, "", nil)
	return err
}

func (r *vpcResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m vpcModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": m.Name.ValueString(), "cidr": m.CIDR.ValueString()}
	if !m.ProjectID.IsNull() && !m.ProjectID.IsUnknown() {
		body["project_id"] = m.ProjectID.ValueInt64()
	}
	var v vpcView
	if _, err := r.pd.c.Do(ctx, http.MethodPost, "cloud/vpcs", nil, body, "", &v); err != nil {
		resp.Diagnostics.AddError("Creating the VPC failed", vpcErr(err))
		return
	}
	id := string(v.ID)
	if id == "" {
		resp.Diagnostics.AddError("Creating the VPC failed", "the API did not return the VPC id")
		return
	}
	// Keep the id in state at once: a failing gateway step must not orphan the VPC.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	en, nat, inter := m.GatewayEnabled.ValueBool(), m.GatewayNAT.ValueBool(), m.InterSubnet.ValueString()
	gwIS := v.Gateway.InterSubnet
	if gwIS == "" {
		gwIS = "allow"
	}
	if en != v.Gateway.Enabled || nat != v.Gateway.NAT || inter != gwIS {
		if err := r.putGateway(ctx, id, en, nat, inter); err != nil {
			resp.Diagnostics.AddError("The VPC was created but setting its gateway failed", vpcErr(err)+"\nRun terraform apply again to retry.")
			if g, gerr := r.get(ctx, id); gerr == nil {
				r.fill(&m, g)
				resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			}
			return
		}
		g, err := r.get(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Reading the VPC failed", err.Error())
			return
		}
		v = *g
	}
	r.fill(&m, &v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *vpcResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m vpcModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.get(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the VPC failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *vpcResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st vpcModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := st.ID.ValueString()
	m.ID, m.DefaultSubnetID = st.ID, st.DefaultSubnetID
	if m.Name.ValueString() != st.Name.ValueString() {
		if _, err := r.pd.c.Do(ctx, http.MethodPatch, vpcPath(id), nil, map[string]any{"name": m.Name.ValueString()}, "", nil); err != nil {
			resp.Diagnostics.AddError("Renaming the VPC failed", vpcErr(err))
			return
		}
	}
	if !m.GatewayEnabled.Equal(st.GatewayEnabled) || !m.GatewayNAT.Equal(st.GatewayNAT) || !m.InterSubnet.Equal(st.InterSubnet) {
		if err := r.putGateway(ctx, id, m.GatewayEnabled.ValueBool(), m.GatewayNAT.ValueBool(), m.InterSubnet.ValueString()); err != nil {
			resp.Diagnostics.AddError("Changing the VPC gateway failed", vpcErr(err))
			return
		}
	}
	v, err := r.get(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading the VPC failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *vpcResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m vpcModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := m.ID.ValueString()
	v, err := r.get(ctx, id)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the VPC failed", err.Error())
		return
	}
	// Refuse before touching the gateway, so a VPC that is still in use keeps working.
	if len(v.Servers) > 0 || len(v.Gateway.PortForwards) > 0 {
		resp.Diagnostics.AddError("The VPC is still in use",
			fmt.Sprintf("%d server network card(s) and %d port forward(s) are still in VPC %s. Remove the server network cards (gyro_server_nic) and port forwards (gyro_port_forward) first, then destroy the VPC.",
				len(v.Servers), len(v.Gateway.PortForwards), id))
		return
	}
	if v.Gateway.Enabled {
		if err := r.putGateway(ctx, id, false, v.Gateway.NAT, func() string {
			if v.Gateway.InterSubnet == "" {
				return "allow"
			}
			return v.Gateway.InterSubnet
		}()); err != nil && !client.IsNotFound(err) {
			resp.Diagnostics.AddError("Turning off the VPC gateway failed", vpcErr(err))
			return
		}
	}
	_, err = r.pd.c.Do(ctx, http.MethodDelete, vpcPath(id), url.Values{"confirm": []string{"true"}}, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) {
		if client.HasCode(err, "vpc_in_use") {
			resp.Diagnostics.AddError("The VPC is still in use", vpcErr(err))
			return
		}
		resp.Diagnostics.AddError("Deleting the VPC failed", vpcErr(err))
	}
}

func (r *vpcResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

/* ================================================================== gyro_subnet */

type subnetResource struct{ pd *providerData }

func newSubnetResource() resource.Resource { return &subnetResource{} }

type subnetModel struct {
	ID        types.String `tfsdk:"id"`
	VPCID     types.String `tfsdk:"vpc_id"`
	Name      types.String `tfsdk:"name"`
	CIDR      types.String `tfsdk:"cidr"`
	Prefix    types.Int64  `tfsdk:"prefix"`
	SubnetID  types.String `tfsdk:"subnet_id"`
	GatewayIP types.String `tfsdk:"gateway_ip"`
	Capacity  types.Int64  `tfsdk:"capacity"`
}

func (r *subnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (r *subnetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A subnet of a gyro_vpc (free). /20 to /28 inside the VPC range; .1 is the gateway, .0-.4 and the last address are reserved. " +
			"Default limit: 4 subnets per VPC (the one created with the VPC included; up to 8 with a support ticket). Every attribute is ForceNew. " +
			"Destroy needs the subnet to be empty (no gyro_server_nic in it).",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<vpc_id>/<subnet_id>"},
			"vpc_id": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "gyro_vpc id."},
			"name": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Subnet name, unique in the VPC.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 48)}},
			"cidr": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceIfSetS, Validators: []validator.String{canonicalCIDR{}},
				Description: "Subnet range inside the VPC, e.g. 10.10.1.0/24. Leave it out to let the API pick a free range of size prefix."},
			"prefix": schema.Int64Attribute{Optional: true, PlanModifiers: replaceI,
				Validators:  []validator.Int64{int64validator.Between(20, 28), int64validator.ConflictsWith(path.MatchRoot("cidr"))},
				Description: "Size of the automatically picked range when cidr is not set (20-28, API default 24)."},
			"subnet_id":  schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Subnet id (use it in gyro_server_nic.subnet_id)."},
			"gateway_ip": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Gateway address of the subnet (.1)."},
			"capacity":   schema.Int64Attribute{Computed: true, PlanModifiers: keepI, Description: "Usable addresses for servers."},
		},
	}
}

func (r *subnetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *subnetResource) fill(m *subnetModel, s *vpcSubnetView) {
	m.ID = types.StringValue(m.VPCID.ValueString() + "/" + string(s.ID))
	m.SubnetID = types.StringValue(string(s.ID))
	m.Name = types.StringValue(s.Name)
	m.CIDR = types.StringValue(s.CIDR)
	m.GatewayIP = types.StringValue(s.GatewayIP)
	m.Capacity = types.Int64Value(s.Capacity)
}

func (r *subnetResource) find(ctx context.Context, vpcID, subnetID string) (*vpcSubnetView, error) {
	var out struct {
		Subnets []vpcSubnetView `json:"subnets"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, vpcPath(vpcID, "subnets"), nil, nil, "", &out); err != nil {
		return nil, err
	}
	for i := range out.Subnets {
		if string(out.Subnets[i].ID) == subnetID {
			return &out.Subnets[i], nil
		}
	}
	return nil, nil
}

func (r *subnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": m.Name.ValueString()}
	if !m.CIDR.IsNull() && !m.CIDR.IsUnknown() {
		body["cidr"] = m.CIDR.ValueString()
	} else if !m.Prefix.IsNull() && !m.Prefix.IsUnknown() {
		body["prefix"] = m.Prefix.ValueInt64()
	}
	var s vpcSubnetView
	if _, err := r.pd.c.Do(ctx, http.MethodPost, vpcPath(m.VPCID.ValueString(), "subnets"), nil, body, "", &s); err != nil {
		resp.Diagnostics.AddError("Creating the subnet failed", vpcErr(err))
		return
	}
	if s.ID == "" {
		resp.Diagnostics.AddError("Creating the subnet failed", "the API did not return the subnet id")
		return
	}
	r.fill(&m, &s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *subnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m subnetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.find(ctx, m.VPCID.ValueString(), m.SubnetID.ValueString())
	if client.IsNotFound(err) || (err == nil && s == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the subnet failed", err.Error())
		return
	}
	r.fill(&m, s)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: every attribute forces replacement; nothing to send.
func (r *subnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *subnetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m subnetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, vpcPath(m.VPCID.ValueString(), "subnets", url.PathEscape(m.SubnetID.ValueString())), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting the subnet failed", vpcErr(err))
	}
}

func (r *subnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	v, s, ok := splitImport(req.ID, "<vpc_id>/<subnet_id>, e.g. 12/34", resp)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vpc_id"), v)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("subnet_id"), s)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

/* ================================================================== gyro_server_nic */

type serverNICResource struct{ pd *providerData }

func newServerNICResource() resource.Resource { return &serverNICResource{} }

type serverNICModel struct {
	ID        types.String `tfsdk:"id"`
	ServerID  types.Int64  `tfsdk:"server_id"`
	SubnetID  types.String `tfsdk:"subnet_id"`
	IP        types.String `tfsdk:"ip"`
	NICID     types.String `tfsdk:"nic_id"`
	VPCID     types.String `tfsdk:"vpc_id"`
	MAC       types.String `tfsdk:"mac"`
	GatewayIP types.String `tfsdk:"gateway_ip"`
	State     types.String `tfsdk:"state"`
}

func (r *serverNICResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_nic"
}

func (r *serverNICResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A private network card: attaches a Gyro Cloud Istanbul (tr-ist-1) server to a subnet of a gyro_vpc (free). The card is configured " +
			"inside the server automatically; no reboot. Up to 3 cards per server; a server can be in one VPC at a time (several subnets of it are fine). " +
			"Create waits until the card is ready (up to 5 minutes), destroy waits until it is gone. Every attribute is ForceNew.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<server_id>/<nic_id>"},
			"server_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "tr-ist-1 server id."},
			"subnet_id": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Subnet id (gyro_subnet.subnet_id or gyro_vpc.default_subnet_id)."},
			"ip": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceIfSetS, Validators: []validator.String{ipv4Only{}},
				Description: "Private IPv4 of the server in the subnet. Leave it out to get the next free address."},
			"nic_id":     schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Network card id."},
			"vpc_id":     schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "VPC of the subnet."},
			"mac":        schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "MAC address of the card."},
			"gateway_ip": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Gateway address of the subnet."},
			"state":      schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "attaching, ready, detaching or error."},
		},
	}
}

func (r *serverNICResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverNICResource) path(serverID int64) string {
	return fmt.Sprintf("cloud/servers/%d/nics", serverID)
}

// find returns the card or nil when the server has no such card. A 404 (server gone) is returned as error.
func (r *serverNICResource) find(ctx context.Context, serverID int64, nicID string) (*nicView, error) {
	var out struct {
		NICs []nicView `json:"nics"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, r.path(serverID), nil, nil, "", &out); err != nil {
		return nil, err
	}
	for i := range out.NICs {
		if string(out.NICs[i].ID) == nicID {
			return &out.NICs[i], nil
		}
	}
	return nil, nil
}

func (r *serverNICResource) fill(m *serverNICModel, n *nicView) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", m.ServerID.ValueInt64(), string(n.ID)))
	m.NICID = types.StringValue(string(n.ID))
	m.SubnetID = types.StringValue(string(n.SubnetID))
	m.IP = types.StringValue(n.IP)
	m.VPCID = types.StringValue(string(n.VPCID))
	m.MAC = types.StringValue(n.MAC)
	m.GatewayIP = types.StringValue(n.GatewayIP)
	m.State = types.StringValue(n.State)
}

func (r *serverNICResource) interval() time.Duration {
	if r.pd.pollInterval > 0 {
		return r.pd.pollInterval
	}
	return 5 * time.Second
}

func (r *serverNICResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverNICModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	srv := m.ServerID.ValueInt64()
	body := map[string]any{"subnet_id": idBody(m.SubnetID.ValueString())}
	if !m.IP.IsNull() && !m.IP.IsUnknown() {
		body["ip"] = m.IP.ValueString()
	}
	var out struct {
		NIC    nicView `json:"nic"`
		TaskID apiID   `json:"task_id"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodPost, r.path(srv), nil, body, "", &out); err != nil {
		resp.Diagnostics.AddError("Attaching the network card failed", vpcErr(err))
		return
	}
	nicID := string(out.NIC.ID)
	if nicID == "" {
		resp.Diagnostics.AddError("Attaching the network card failed", "the API did not return the card id")
		return
	}
	cur := out.NIC
	deadline := time.Now().Add(nicWaitTimeout)
	for cur.State != "ready" {
		if cur.State == "error" {
			msg := cur.Error
			if msg == "" {
				msg = "the card could not be configured"
			}
			r.cleanup(ctx, srv, nicID)
			resp.Diagnostics.AddError("Attaching the network card failed", fmt.Sprintf("card %s of server %d ended in state error: %s. The card was removed; run terraform apply again to retry.", nicID, srv, msg))
			return
		}
		if time.Now().After(deadline) {
			m.State = types.StringValue(cur.State)
			r.fill(&m, &cur)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...) // keep it in state: the card exists and may still finish
			resp.Diagnostics.AddError("Attaching the network card timed out",
				fmt.Sprintf("card %s of server %d is still %q after %s. Check `gyro server nic list %d`; run terraform apply again (or refresh) once it is ready.", nicID, srv, cur.State, nicWaitTimeout, srv))
			return
		}
		if !sleepCtx(ctx, r.interval()) {
			resp.Diagnostics.AddError("Attaching the network card was interrupted", ctx.Err().Error())
			return
		}
		n, err := r.find(ctx, srv, nicID)
		if err != nil {
			resp.Diagnostics.AddError("Reading the network card failed", err.Error())
			return
		}
		if n == nil {
			resp.Diagnostics.AddError("Attaching the network card failed", fmt.Sprintf("card %s disappeared from server %d while attaching.", nicID, srv))
			return
		}
		cur = *n
	}
	r.fill(&m, &cur)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// cleanup removes a card that failed to attach (best effort).
func (r *serverNICResource) cleanup(ctx context.Context, srv int64, nicID string) {
	_, _ = r.pd.c.Do(ctx, http.MethodDelete, r.path(srv)+"/"+url.PathEscape(nicID), nil, nil, "", nil)
}

func (r *serverNICResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverNICModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n, err := r.find(ctx, m.ServerID.ValueInt64(), m.NICID.ValueString())
	if client.IsNotFound(err) || (err == nil && n == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the network card failed", err.Error())
		return
	}
	r.fill(&m, n)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverNICResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverNICModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverNICResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverNICModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	srv, nicID := m.ServerID.ValueInt64(), m.NICID.ValueString()
	_, err := r.pd.c.Do(ctx, http.MethodDelete, r.path(srv)+"/"+url.PathEscape(nicID), nil, nil, "", nil)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Detaching the network card failed", vpcErr(err))
		return
	}
	deadline := time.Now().Add(nicWaitTimeout)
	for {
		n, err := r.find(ctx, srv, nicID)
		if client.IsNotFound(err) || (err == nil && n == nil) {
			return
		}
		if err != nil {
			resp.Diagnostics.AddError("Reading the network card failed", err.Error())
			return
		}
		if n.State == "error" {
			resp.Diagnostics.AddError("Detaching the network card failed", fmt.Sprintf("card %s of server %d ended in state error: %s", nicID, srv, n.Error))
			return
		}
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Detaching the network card timed out", fmt.Sprintf("card %s of server %d is still %q after %s.", nicID, srv, n.State, nicWaitTimeout))
			return
		}
		if !sleepCtx(ctx, r.interval()) {
			resp.Diagnostics.AddError("Detaching the network card was interrupted", ctx.Err().Error())
			return
		}
	}
}

func (r *serverNICResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	s, n, ok := splitImport(req.ID, "<server_id>/<nic_id>, e.g. 123/45", resp)
	if !ok {
		return
	}
	sv, err := strconv.ParseInt(s, 10, 64)
	if err != nil || sv <= 0 {
		resp.Diagnostics.AddError("Invalid import id", "Use <server_id>/<nic_id>, e.g. 123/45")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), sv)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("nic_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

/* ================================================================== gyro_port_forward */

type portForwardResource struct{ pd *providerData }

func newPortForwardResource() resource.Resource { return &portForwardResource{} }

type portForwardModel struct {
	ID         types.String `tfsdk:"id"`
	VPCID      types.String `tfsdk:"vpc_id"`
	Protocol   types.String `tfsdk:"protocol"`
	PublicPort types.Int64  `tfsdk:"public_port"`
	TargetIP   types.String `tfsdk:"target_ip"`
	TargetPort types.Int64  `tfsdk:"target_port"`
	ForwardID  types.String `tfsdk:"forward_id"`
}

func (r *portForwardResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_forward"
}

func (r *portForwardResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Forwards a port of the VPC gateway's public IP (gyro_vpc.gateway_public_ip) to a private address in the VPC. " +
			"Needs gateway_enabled = true on the gyro_vpc. Up to 20 per VPC by default. Every attribute is ForceNew.",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<vpc_id>/<forward_id>"},
			"vpc_id": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "gyro_vpc id."},
			"protocol": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Validators: []validator.String{stringvalidator.OneOf("tcp", "udp")},
				Description: "tcp or udp."},
			"public_port": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Validators: []validator.Int64{int64validator.Between(1, 65535)},
				Description: "Port on the gateway's public IP."},
			"target_ip": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Validators: []validator.String{ipv4Only{}},
				Description: "Private IPv4 of a server in the VPC (e.g. gyro_server_nic.ip)."},
			"target_port": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Validators: []validator.Int64{int64validator.Between(1, 65535)},
				Description: "Port on the target server."},
			"forward_id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Port forward id."},
		},
	}
}

func (r *portForwardResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *portForwardResource) fill(m *portForwardModel, p *vpcPFView) {
	m.ID = types.StringValue(m.VPCID.ValueString() + "/" + string(p.ID))
	m.ForwardID = types.StringValue(string(p.ID))
	m.Protocol = types.StringValue(p.Protocol)
	m.PublicPort = types.Int64Value(p.PublicPort)
	m.TargetIP = types.StringValue(p.TargetIP)
	m.TargetPort = types.Int64Value(p.TargetPort)
}

func (r *portForwardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m portForwardModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"protocol": m.Protocol.ValueString(), "public_port": m.PublicPort.ValueInt64(),
		"target_ip": m.TargetIP.ValueString(), "target_port": m.TargetPort.ValueInt64()}
	var p vpcPFView
	if _, err := r.pd.c.Do(ctx, http.MethodPost, vpcPath(m.VPCID.ValueString(), "port-forwards"), nil, body, "", &p); err != nil {
		resp.Diagnostics.AddError("Creating the port forward failed", vpcErr(err))
		return
	}
	if p.ID == "" {
		resp.Diagnostics.AddError("Creating the port forward failed", "the API did not return the port forward id")
		return
	}
	r.fill(&m, &p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *portForwardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m portForwardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		PortForwards []vpcPFView `json:"port_forwards"`
	}
	_, err := r.pd.c.Do(ctx, http.MethodGet, vpcPath(m.VPCID.ValueString(), "port-forwards"), nil, nil, "", &out)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading port forwards failed", err.Error())
		return
	}
	for i := range out.PortForwards {
		if string(out.PortForwards[i].ID) == m.ForwardID.ValueString() {
			r.fill(&m, &out.PortForwards[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *portForwardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m portForwardModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *portForwardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m portForwardModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, vpcPath(m.VPCID.ValueString(), "port-forwards", url.PathEscape(m.ForwardID.ValueString())), nil, nil, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting the port forward failed", vpcErr(err))
	}
}

func (r *portForwardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	v, p, ok := splitImport(req.ID, "<vpc_id>/<forward_id>, e.g. 12/7", resp)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vpc_id"), v)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("forward_id"), p)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
