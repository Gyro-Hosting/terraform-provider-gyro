package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Reverse DNS (PTR) of one server address. API: GET/PUT /cloud/servers/{id}/rdns (GyroAPI >= 2.12.0 for tr-ist-1 create + status).

var _ resource.ResourceWithImportState = (*serverRdnsResource)(nil)

type serverRdnsResource struct{ pd *providerData }

func newServerRdnsResource() resource.Resource { return &serverRdnsResource{} }

type serverRdnsModel struct {
	ID       types.String `tfsdk:"id"`
	ServerID types.Int64  `tfsdk:"server_id"`
	IP       types.String `tfsdk:"ip"`
	Hostname types.String `tfsdk:"hostname"`
	Status   types.String `tfsdk:"status"`
}

type rdnsEntry struct {
	IP       string  `json:"ip"`
	Hostname *string `json:"hostname"`
	Status   string  `json:"status"`
}

func (r *serverRdnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_rdns"
}

func (r *serverRdnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reverse DNS (PTR) record of one address of a server. In region tr-ist-1 a PTR is a request that goes live after approval " +
			"(status pending -> active); a new address without a record gets one created. Destroy resets the address to the default.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"server_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Server id."},
			"ip":        schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "An address of this server."},
			"hostname":  schema.StringAttribute{Required: true, Description: "Fully qualified host name for the PTR record, e.g. mail.example.com."},
			"status":    schema.StringAttribute{Computed: true, Description: "tr-ist-1: none | pending | active | rejected (empty in other regions)."},
		},
	}
}

func (r *serverRdnsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *serverRdnsResource) path(m *serverRdnsModel) string {
	return fmt.Sprintf("cloud/servers/%d/rdns", sid(m.ServerID))
}

func (r *serverRdnsResource) find(list []rdnsEntry, ip string) *rdnsEntry {
	for i := range list {
		if list[i].IP == ip {
			return &list[i]
		}
	}
	return nil
}

func (r *serverRdnsResource) apply(m *serverRdnsModel, e *rdnsEntry) {
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServerID), m.IP.ValueString()))
	if e.Hostname != nil {
		m.Hostname = types.StringValue(*e.Hostname)
	}
	m.Status = types.StringValue(e.Status)
}

func (r *serverRdnsResource) put(ctx context.Context, m *serverRdnsModel, host string, d interface{ AddError(string, string) }) bool {
	var out []rdnsEntry
	if _, err := r.pd.c.Do(ctx, http.MethodPut, r.path(m), nil, map[string]any{"ip": m.IP.ValueString(), "hostname": host}, "", &out); err != nil {
		d.AddError("Setting reverse DNS failed", err.Error())
		return false
	}
	if host == "" {
		return true
	}
	e := r.find(out, m.IP.ValueString())
	if e == nil {
		d.AddError("Setting reverse DNS failed", "the address is not listed on the server after the change")
		return false
	}
	r.apply(m, e)
	return true
}

func (r *serverRdnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverRdnsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.put(ctx, &m, strings.ToLower(strings.TrimSuffix(m.Hostname.ValueString(), ".")), &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverRdnsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverRdnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var out []rdnsEntry
	_, err := r.pd.c.Do(ctx, http.MethodGet, r.path(&m), nil, nil, "", &out)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading reverse DNS failed", err.Error())
		return
	}
	e := r.find(out, m.IP.ValueString())
	if e == nil || e.Hostname == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	r.apply(&m, e)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverRdnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverRdnsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !r.put(ctx, &m, strings.ToLower(strings.TrimSuffix(m.Hostname.ValueString(), ".")), &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverRdnsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverRdnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	var out []rdnsEntry
	_, err := r.pd.c.Do(ctx, http.MethodPut, r.path(&m), nil, map[string]any{"ip": m.IP.ValueString(), "hostname": ""}, "", &out)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Resetting reverse DNS failed", err.Error())
	}
}

func (r *serverRdnsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || p[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <server_id>/<ip>, e.g. 123/45.87.174.10")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ip"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
