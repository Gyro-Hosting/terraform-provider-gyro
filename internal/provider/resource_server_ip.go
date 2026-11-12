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
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// One additional IPv4 address of a tr-ist-1 server. API (GyroAPI >= 2.16.0):
// GET/POST /cloud/servers/{id}/ips, DELETE /cloud/servers/{id}/ips/{ip}?confirm=true.

var _ resource.ResourceWithImportState = (*serverIPResource)(nil)

type serverIPResource struct{ pd *providerData }

func newServerIPResource() resource.Resource { return &serverIPResource{} }

type serverIPModel struct {
	ID       types.String  `tfsdk:"id"`
	ServerID types.Int64   `tfsdk:"server_id"`
	IP       types.String  `tfsdk:"ip"`
	AddedAt  types.String  `tfsdk:"added_at"`
	Applied  types.Bool    `tfsdk:"applied_on_server"`
	RDNS     types.String  `tfsdk:"reverse_dns"`
	MinHours types.Int64   `tfsdk:"minimum_hours"`
	HourEUR  types.Float64 `tfsdk:"hourly_eur"`
	MonthEUR types.Float64 `tfsdk:"monthly_eur"`
}

type serverIPList struct {
	PrimaryIP string `json:"primary_ip"`
	ExtraIPs  []struct {
		IP      string  `json:"ip"`
		AddedAt *string `json:"added_at"`
		RDNS    *string `json:"reverse_dns"`
	} `json:"extra_ips"`
	Pricing struct {
		MonthlyEUR float64 `json:"per_ip_monthly_eur"`
		HourlyEUR  float64 `json:"per_ip_hourly_eur"`
		MinHours   int64   `json:"minimum_hours"`
	} `json:"pricing"`
	Applied bool     `json:"applied_on_server"`
	Added   []string `json:"added"`
}

func (r *serverIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_ip"
}

func (r *serverIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "One additional IPv4 address of a Gyro Cloud Istanbul (tr-ist-1) server. The address is taken from the pool and configured " +
			"on the server automatically (/32 on the primary interface). Billed hourly from the prepaid balance (default 2 EUR per address-month) " +
			"until destroyed, at least minimum_hours (default 6) per address; up to 5 per server by default. Destroy releases the address (getting the same address back is not guaranteed). " +
			"Reverse DNS: gyro_server_rdns.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<server_id>/<ip>"},
			"server_id":         schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "tr-ist-1 server id."},
			"ip":                schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "The assigned IPv4 address."},
			"added_at":          schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"applied_on_server": schema.BoolAttribute{Computed: true, Description: "false while the server's guest agent has not confirmed the address yet."},
			"reverse_dns":       schema.StringAttribute{Computed: true, Description: "Current PTR of the address (automatic default <a-b-c-d>.ip.gyrohosting.com unless set with gyro_server_rdns). API 2.17.2+."},
			"minimum_hours":     schema.Int64Attribute{Computed: true, Description: "Every added address is billed at least this many hours (default 6), even if destroyed earlier. API 2.17.1+."},
			"hourly_eur":        schema.Float64Attribute{Computed: true, Description: "Current price per address-hour in EUR (VAT excluded)."},
			"monthly_eur":       schema.Float64Attribute{Computed: true, Description: "Current price per address-month in EUR (VAT excluded)."},
		},
	}
}

func (r *serverIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverIPResource) path(m *serverIPModel) string {
	return fmt.Sprintf("cloud/servers/%d/ips", sid(m.ServerID))
}

func (r *serverIPResource) fill(m *serverIPModel, l *serverIPList) bool {
	for _, e := range l.ExtraIPs {
		if e.IP == m.IP.ValueString() {
			m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServerID), e.IP))
			if e.AddedAt != nil {
				m.AddedAt = types.StringValue(*e.AddedAt)
			} else {
				m.AddedAt = types.StringValue("")
			}
			m.Applied = types.BoolValue(l.Applied)
			m.RDNS = types.StringValue("")
			if e.RDNS != nil {
				m.RDNS = types.StringValue(*e.RDNS)
			}
			m.MinHours = types.Int64Value(l.Pricing.MinHours)
			m.HourEUR = types.Float64Value(l.Pricing.HourlyEUR)
			m.MonthEUR = types.Float64Value(l.Pricing.MonthlyEUR)
			return true
		}
	}
	return false
}

func (r *serverIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out serverIPList
	if _, err := r.pd.c.Do(ctx, http.MethodPost, r.path(&m), nil, map[string]any{"count": 1}, "", &out); err != nil {
		resp.Diagnostics.AddError("Adding the IPv4 address failed", err.Error())
		return
	}
	if len(out.Added) != 1 {
		resp.Diagnostics.AddError("Adding the IPv4 address failed", "the API did not return the new address")
		return
	}
	m.IP = types.StringValue(out.Added[0])
	if !r.fill(&m, &out) {
		m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServerID), out.Added[0]))
		m.AddedAt = types.StringValue("")
		m.Applied = types.BoolValue(false)
		m.RDNS = types.StringValue("")
		m.MinHours = types.Int64Value(out.Pricing.MinHours)
		m.HourEUR = types.Float64Value(out.Pricing.HourlyEUR)
		m.MonthEUR = types.Float64Value(out.Pricing.MonthlyEUR)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var out serverIPList
	_, err := r.pd.c.Do(ctx, http.MethodGet, r.path(&m), nil, nil, "", &out)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading IPv4 addresses failed", err.Error())
		return
	}
	if !r.fill(&m, &out) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update: nothing is updatable in place (server_id forces a new address).
func (r *serverIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	q := url.Values{"confirm": []string{"true"}}
	_, err := r.pd.c.Do(ctx, http.MethodDelete, r.path(&m)+"/"+url.PathEscape(m.IP.ValueString()), q, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Releasing the IPv4 address failed", err.Error())
	}
}

func (r *serverIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || p[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <server_id>/<ip>, e.g. 123/31.58.248.200")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ip"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
