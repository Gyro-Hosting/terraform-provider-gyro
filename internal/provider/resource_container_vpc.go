package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_container_vpc_attachment (v0.32.0, Gyro Public API 2.34.0, VPC V2): a node container environment in one subnet of a
// customer VPC (tr-ist-1). GET|PUT|PATCH|DELETE /cloud/containers/{id}/vpc. Contract: gyro-vpc/V2-SOZLESME.md.

var _ resource.ResourceWithImportState = (*containerVPCResource)(nil)

// containerVPCWaitTimeout: how long attach (state ready) / detach (state off) is awaited.
var containerVPCWaitTimeout = 10 * time.Minute

type containerVPCResource struct{ pd *providerData }

func newContainerVPCResource() resource.Resource { return &containerVPCResource{} }

type containerVPCExposeModel struct {
	Service types.String `tfsdk:"service"`
	Port    types.Int64  `tfsdk:"port"`
}

type containerVPCModel struct {
	ID            types.String              `tfsdk:"id"`
	EnvironmentID types.Int64               `tfsdk:"environment_id"`
	VPCID         types.String              `tfsdk:"vpc_id"`
	SubnetID      types.String              `tfsdk:"subnet_id"`
	Expose        []containerVPCExposeModel `tfsdk:"expose"`
	SourceRange   types.String              `tfsdk:"source_range"`
	EndpointIP    types.String              `tfsdk:"endpoint_ip"`
	NodeIPs       types.List                `tfsdk:"node_ips"`
	State         types.String              `tfsdk:"state"`
	VPCCIDR       types.String              `tfsdk:"vpc_cidr"`
	SubnetCIDR    types.String              `tfsdk:"subnet_cidr"`
}

type containerVPCView struct {
	Attached    bool     `json:"attached"`
	Supported   *bool    `json:"supported"`
	State       string   `json:"state"`
	VPCID       apiID    `json:"vpc_id"`
	SubnetID    apiID    `json:"subnet_id"`
	VPCCIDR     string   `json:"vpc_cidr"`
	SubnetCIDR  string   `json:"subnet_cidr"`
	SourceRange string   `json:"source_range"`
	EndpointIP  string   `json:"endpoint_ip"`
	NodeIPs     []string `json:"node_ips"`
	Error       string   `json:"error"`
	Expose      []struct {
		Service string `json:"service"`
		Port    int64  `json:"port"`
	} `json:"expose"`
}

func (r *containerVPCResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_vpc_attachment"
}

func (r *containerVPCResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepS := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Connects a node Gyro Containers (ECS) environment (Mini, Small, Starter, Standard, Pro) to one subnet of a gyro_vpc in tr-ist-1. " +
			"The environment gets a fixed VPC source range (/28) inside the subnet: connections from the containers to the VPC servers come from it and it does not " +
			"change when nodes are replaced, scaled or renewed. The first address is the VPC endpoint: VPC servers reach the published services (expose) at " +
			"endpoint_ip:port; it is never reachable from the internet. Free. The subnet must be /26 or larger; an environment joins one subnet at a time. " +
			"Serverless environments are not supported yet. Create waits for state ready, destroy waits until the attachment is gone. Import: <environment_id>.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Same as environment_id."},
			"environment_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id (gyro_container_environment.id)."},
			"vpc_id":         schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "gyro_vpc id."},
			"subnet_id":      schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "gyro_subnet subnet_id (or gyro_vpc default_subnet_id), /26 or larger."},
			"source_range":   schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "Fixed /28: connections from the containers to the VPC come from here (allow it in your server firewalls)."},
			"endpoint_ip":    schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "First address of source_range; VPC servers reach the published services at endpoint_ip:port."},
			"node_ips":       schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Addresses of the ready nodes inside source_range (can change; the range does not)."},
			"state":          schema.StringAttribute{Computed: true, Description: "off, attaching, ready, detaching, error."},
			"vpc_cidr":       schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "Range of the VPC."},
			"subnet_cidr":    schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "Range of the subnet."},
		},
		Blocks: map[string]schema.Block{
			"expose": schema.ListNestedBlock{Validators: []validator.List{listvalidator.SizeAtMost(20)},
				Description: "A service published at the VPC endpoint (repeat the block; updated in place; at most 20; each port once). Ports 22, 2200, 6443, 9100, 9750, 10250-10256, 30000-32767, 51820-51821 are reserved.",
				NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
					"service": schema.StringAttribute{Required: true, Description: "Service name in the environment (gyro_container_service.name)."},
					"port": schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.Between(1, 65535)},
						Description: "Port at the endpoint; connects to the service's internal address <service>.<namespace>.svc.cluster.local:80."},
				}}},
		},
	}
}

func (r *containerVPCResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func cvPath(env int64) string { return fmt.Sprintf("cloud/containers/%d/vpc", env) }

func (r *containerVPCResource) get(ctx context.Context, env int64) (*containerVPCView, error) {
	var v containerVPCView
	if _, err := r.pd.c.Do(ctx, http.MethodGet, cvPath(env), nil, nil, "", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func cvExposeBody(l []containerVPCExposeModel) []map[string]any {
	out := []map[string]any{}
	for _, x := range l {
		out = append(out, map[string]any{"service": x.Service.ValueString(), "port": x.Port.ValueInt64()})
	}
	return out
}

func (r *containerVPCResource) fill(m *containerVPCModel, v *containerVPCView) {
	env := m.EnvironmentID.ValueInt64()
	m.ID = types.StringValue(strconv.FormatInt(env, 10))
	if v.VPCID != "" {
		m.VPCID = types.StringValue(string(v.VPCID))
	}
	if v.SubnetID != "" {
		m.SubnetID = types.StringValue(string(v.SubnetID))
	}
	m.SourceRange, m.EndpointIP = types.StringValue(v.SourceRange), types.StringValue(v.EndpointIP)
	m.State, m.VPCCIDR, m.SubnetCIDR = types.StringValue(v.State), types.StringValue(v.VPCCIDR), types.StringValue(v.SubnetCIDR)
	ips := make([]attr.Value, 0, len(v.NodeIPs))
	for _, ip := range v.NodeIPs {
		ips = append(ips, types.StringValue(ip))
	}
	m.NodeIPs = types.ListValueMust(types.StringType, ips)
	ex := make([]containerVPCExposeModel, 0, len(v.Expose))
	for _, x := range v.Expose {
		ex = append(ex, containerVPCExposeModel{Service: types.StringValue(x.Service), Port: types.Int64Value(x.Port)})
	}
	m.Expose = ex
}

// wait polls until the attachment is ready (off=false) or gone (off=true).
func (r *containerVPCResource) wait(ctx context.Context, env int64, off bool) (*containerVPCView, error) {
	deadline := time.Now().Add(containerVPCWaitTimeout)
	for {
		v, err := r.get(ctx, env)
		if off && client.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if off && (v.State == "off" || (v.State == "" && !v.Attached)) {
			return v, nil
		}
		if !off && v.State == "ready" {
			return v, nil
		}
		if !off && v.State == "error" {
			msg := v.Error
			if msg == "" {
				msg = "the nodes could not join the subnet"
			}
			return v, fmt.Errorf("VPC attachment failed: %s", msg)
		}
		if time.Now().After(deadline) {
			return v, fmt.Errorf("timed out after %s waiting for the VPC attachment (state %q)", containerVPCWaitTimeout, v.State)
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(r.pd.pollInterval):
		}
	}
}

func cvErr(err error) string {
	msg := err.Error()
	if client.HasCode(err, "not_supported") || strings.Contains(strings.ToLower(msg), "serverless") {
		msg += "\nOnly node environments (Mini, Small, Starter, Standard, Pro) can join a VPC for now."
	}
	return msg
}

func (r *containerVPCResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerVPCModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := m.EnvironmentID.ValueInt64()
	body := map[string]any{"vpc_id": m.VPCID.ValueString(), "subnet_id": m.SubnetID.ValueString()}
	if len(m.Expose) > 0 {
		body["expose"] = cvExposeBody(m.Expose)
	}
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPut, cvPath(env), body); err != nil {
		resp.Diagnostics.AddError("Attaching the container environment to the VPC failed", cvErr(err))
		return
	}
	v, err := r.wait(ctx, env, false)
	if err != nil {
		// do not leave a half-made attachment behind
		_, _ = r.pd.c.TypedWrite(ctx, http.MethodDelete, cvPath(env), map[string]any{"confirm": true})
		resp.Diagnostics.AddError("Attaching the container environment to the VPC failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerVPCResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerVPCModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.get(ctx, m.EnvironmentID.ValueInt64())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the VPC attachment failed", err.Error())
		return
	}
	if !v.Attached || v.State == "off" || v.State == "detaching" {
		resp.State.RemoveResource(ctx)
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerVPCResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m containerVPCModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := m.EnvironmentID.ValueInt64()
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, cvPath(env), map[string]any{"expose": cvExposeBody(m.Expose)}); err != nil {
		resp.Diagnostics.AddError("Changing the published services failed", cvErr(err))
		return
	}
	v, err := r.get(ctx, env)
	if err != nil {
		resp.Diagnostics.AddError("Reading the VPC attachment failed", err.Error())
		return
	}
	r.fill(&m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerVPCResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerVPCModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := m.EnvironmentID.ValueInt64()
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, cvPath(env), map[string]any{"confirm": true})
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Detaching the container environment from the VPC failed", cvErr(err))
		return
	}
	if _, err := r.wait(ctx, env, true); err != nil {
		resp.Diagnostics.AddError("Detaching the container environment from the VPC failed", err.Error())
	}
}

func (r *containerVPCResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	env, err := strconv.ParseInt(strings.TrimSpace(req.ID), 10, 64)
	if err != nil || env <= 0 {
		resp.Diagnostics.AddError("Invalid import id", "expected the container environment id, e.g. 17000")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), env)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strconv.FormatInt(env, 10))...)
}

/* ------------------------------------------------------------------ source_cidrs of gyro_port_forward */

// sourceCIDR accepts a public IPv4 network written as its network address (203.0.113.10/32, 198.51.100.0/24).
type sourceCIDR struct{}

func (sourceCIDR) Description(context.Context) string { return "public IPv4 network in CIDR form" }
func (sourceCIDR) MarkdownDescription(context.Context) string {
	return "public IPv4 network in CIDR form"
}
func (sourceCIDR) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	ip, n, err := net.ParseCIDR(s)
	if err != nil || ip.To4() == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid source", fmt.Sprintf("%q: write an IPv4 network in CIDR form; a single address is /32 (203.0.113.10/32).", s))
		return
	}
	if !ip.Equal(n.IP) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid source", fmt.Sprintf("%q: use the network address: %s", s, n.String()))
		return
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid source", fmt.Sprintf("%q: only public IPv4 networks can be allowed.", s))
	}
}
