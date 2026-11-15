package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_server_public_ip (v0.33.0, Gyro Public API 2.29.0+): turn the public IPv4 of a Gyro Cloud Istanbul (tr-ist-1) server off / on.
// GET|DELETE|POST /cloud/servers/{id}/public-ip (DELETE body {confirm_ip, allow_no_egress}; 202 + task).

var _ resource.ResourceWithImportState = (*serverPublicIPResource)(nil)

type serverPublicIPResource struct{ pd *providerData }

func newServerPublicIPResource() resource.Resource { return &serverPublicIPResource{} }

type serverPublicIPModel struct {
	ID            types.String `tfsdk:"id"`
	ServerID      types.Int64  `tfsdk:"server_id"`
	Enabled       types.Bool   `tfsdk:"enabled"`
	AllowNoEgress types.Bool   `tfsdk:"allow_no_egress"`
	IP            types.String `tfsdk:"ip"`
	PrivateReady  types.Bool   `tfsdk:"private_ready"`
}

type publicIPView struct {
	ServerID     int64  `json:"server_id"`
	IP           string `json:"ip"`
	Enabled      bool   `json:"enabled"`
	ExtraIPs     int64  `json:"extra_ips"`
	PrivateReady bool   `json:"private_ready"`
}

func (r *serverPublicIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_public_ip"
}

func (r *serverPublicIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Public IPv4 of a Gyro Cloud Istanbul (tr-ist-1) server: enabled = false makes the server private-only (reachable from its VPC, a VPC gateway " +
			"port forward or the web console). Needs a ready private network card (gyro_server_nic) and no additional IPv4. If the VPC gateway NAT is off the server " +
			"loses internet access; that is refused unless allow_no_egress = true. The address stays reserved: turning it on brings the same IP. " +
			"Destroying this resource turns the public IP back on. Import: <server_id>.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Same as server_id."},
			"server_id":       schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "tr-ist-1 server id."},
			"enabled":         schema.BoolAttribute{Required: true, Description: "false = public IPv4 off (private-only server), true = on. Changed in place."},
			"allow_no_egress": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Allow turning it off although the server then has no internet access (VPC gateway NAT off)."},
			"ip":              schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "The server's public IPv4 (kept while off)."},
			"private_ready":   schema.BoolAttribute{Computed: true, Description: "The server has a ready private network card."},
		},
	}
}

func (r *serverPublicIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func pipPath(sid int64) string { return fmt.Sprintf("cloud/servers/%d/public-ip", sid) }

func (r *serverPublicIPResource) get(ctx context.Context, sid int64) (*publicIPView, error) {
	var v publicIPView
	_, err := r.pd.c.Do(ctx, http.MethodGet, pipPath(sid), nil, nil, "", &v)
	return &v, err
}

func pipErr(err error) string {
	msg := err.Error()
	switch {
	case client.HasCode(err, "no_private_network"):
		msg += "\nAttach the server to a VPC subnet first (gyro_server_nic) and wait until the card is ready."
	case client.HasCode(err, "no_egress"):
		msg += "\nThe VPC gateway NAT is off: the server would lose internet access. Turn the gateway NAT on (gyro_vpc.gateway_enabled / gateway_nat) or set allow_no_egress = true."
	case client.HasCode(err, "extra_ips"):
		msg += "\nRemove the server's additional IPv4 addresses (gyro_server_ip) first."
	case client.HasCode(err, "not_supported"):
		msg += "\nOnly Gyro Cloud Istanbul (tr-ist-1) servers can turn their public IP off."
	}
	return msg
}

// set turns the public IP to `want` (if needed) and waits until the API reports it.
func (r *serverPublicIPResource) set(ctx context.Context, sid int64, want, allowNoEgress bool) (*publicIPView, error) {
	cur, err := r.get(ctx, sid)
	if err != nil {
		return nil, err
	}
	if cur.Enabled != want {
		if want {
			_, err = r.pd.c.Do(ctx, http.MethodPost, pipPath(sid), nil, map[string]any{}, "", nil)
		} else {
			_, err = r.pd.c.Do(ctx, http.MethodDelete, pipPath(sid), nil, map[string]any{"confirm_ip": cur.IP, "allow_no_egress": allowNoEgress}, "", nil)
		}
		if err != nil && !client.HasCode(err, "already_on") && !client.HasCode(err, "already_off") {
			return nil, fmt.Errorf("%s", pipErr(err))
		}
	}
	deadline := time.Now().Add(r.pd.pollTimeout)
	for {
		v, err := r.get(ctx, sid)
		if err == nil && v.Enabled == want {
			// the API flips `enabled` at once; the server applies it a little later (GET .../nics applied = true)
			var n struct {
				Applied       *bool `json:"applied"`
				PublicEnabled *bool `json:"public_enabled"`
			}
			if _, e2 := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/servers/%d/nics", sid), nil, nil, "", &n); e2 != nil || n.Applied == nil || (*n.Applied && (n.PublicEnabled == nil || *n.PublicEnabled == want)) {
				return v, nil
			}
		}
		if time.Now().After(deadline) {
			return v, fmt.Errorf("public IP of server %d did not turn %s within %s", sid, map[bool]string{true: "on", false: "off"}[want], r.pd.pollTimeout)
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(r.pd.pollInterval):
		}
	}
}

func (r *serverPublicIPResource) fill(m *serverPublicIPModel, v *publicIPView) {
	m.ID = types.StringValue(strconv.FormatInt(m.ServerID.ValueInt64(), 10))
	m.Enabled, m.IP, m.PrivateReady = types.BoolValue(v.Enabled), types.StringValue(v.IP), types.BoolValue(v.PrivateReady)
}

func (r *serverPublicIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serverPublicIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.set(ctx, m.ServerID.ValueInt64(), m.Enabled.ValueBool(), m.AllowNoEgress.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Changing the public IP failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverPublicIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serverPublicIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.get(ctx, m.ServerID.ValueInt64())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the public IP failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *serverPublicIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m serverPublicIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.set(ctx, m.ServerID.ValueInt64(), m.Enabled.ValueBool(), m.AllowNoEgress.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Changing the public IP failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Delete turns the public IP back on (the server's normal state).
func (r *serverPublicIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m serverPublicIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.set(ctx, m.ServerID.ValueInt64(), true, false); err != nil && !client.IsNotFound(err) && !strings.Contains(err.Error(), "404") {
		resp.Diagnostics.AddError("Turning the public IP back on failed", err.Error())
	}
}

func (r *serverPublicIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	sid, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
	if err != nil || sid <= 0 {
		resp.Diagnostics.AddError("Invalid import id", "expected the server id, e.g. 12345")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), sid)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.FormatInt(sid, 10))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_no_egress"), false)...)
}
