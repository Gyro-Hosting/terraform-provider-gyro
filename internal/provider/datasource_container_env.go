package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

/* ------------------------------------------------------------------ gyro_container_environment (API 2.27.0) */

type containerEnvDS struct{ pd *providerData }

func newContainerEnvironmentDataSource() datasource.DataSource { return &containerEnvDS{} }

type containerEnvView struct {
	Code       string `json:"code"`
	Namespace  string `json:"namespace"`
	Mode       string `json:"mode"`
	HostSuffix string `json:"host_suffix"`
	Suspended  bool   `json:"suspended"`
	ClusterDNS struct {
		ServiceDomain string `json:"service_domain"`
	} `json:"cluster_dns"`
	EgressIPs  []string `json:"egress_ips"`
	EgressNote string   `json:"egress_note"`
	Egress     struct {
		BuildIPs []string `json:"build_ips"`
		Hash     string   `json:"hash"`
	} `json:"egress"`
	IngressIPs []string `json:"ingress_ips"`
}

type containerEnvModel struct {
	ServiceID      types.Int64  `tfsdk:"service_id"`
	Code           types.String `tfsdk:"code"`
	Namespace      types.String `tfsdk:"namespace"`
	Mode           types.String `tfsdk:"mode"`
	ServiceDomain  types.String `tfsdk:"service_domain"`
	HostSuffix     types.String `tfsdk:"host_suffix"`
	Suspended      types.Bool   `tfsdk:"suspended"`
	EgressIPs      types.List   `tfsdk:"egress_ips"`
	EgressHash     types.String `tfsdk:"egress_hash"`
	EgressNote     types.String `tfsdk:"egress_note"`
	BuildEgressIPs types.List   `tfsdk:"build_egress_ips"`
	IngressIPs     types.List   `tfsdk:"ingress_ips"`
}

func (d *containerEnvDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_environment"
}

func (d *containerEnvDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Gyro Containers (ECS) environment (GET /cloud/containers/{id}): Kubernetes namespace, in-cluster service domain and the current " +
			"egress IPv4 addresses (the addresses your containers reach the internet from - put them on the IP allow list of an external service). " +
			"Egress IPs change when the environment's nodes change (plan change, node replacement, pool scaling); the owner is notified.",
		Attributes: map[string]schema.Attribute{
			"service_id":       schema.Int64Attribute{Required: true, Description: "Container environment (service) id."},
			"code":             schema.StringAttribute{Computed: true, Description: "Environment code (used in <service>-<code>.gyroapp.net)."},
			"namespace":        schema.StringAttribute{Computed: true, Description: "Kubernetes namespace of the environment, e.g. t-canli-12345."},
			"mode":             schema.StringAttribute{Computed: true, Description: "dedicated (own node pool) or serverless (shared sandboxed pool)."},
			"service_domain":   schema.StringAttribute{Computed: true, Description: "In-cluster DNS suffix: services are reached at <service>.<service_domain>:80 (or <service>:80) from the same environment."},
			"host_suffix":      schema.StringAttribute{Computed: true, Description: "Public host suffix, -<code>.gyroapp.net."},
			"suspended":        schema.BoolAttribute{Computed: true},
			"egress_ips":       schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Current egress IPv4 addresses (dedicated: own nodes + the shared pool used during updates; serverless: shared pool nodes). No IPv6."},
			"egress_hash":      schema.StringAttribute{Computed: true, Description: "Changes whenever egress_ips change."},
			"egress_note":      schema.StringAttribute{Computed: true},
			"build_egress_ips": schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Gyro Pipeline build machines (git clone / archive download)."},
			"ingress_ips":      schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Inbound address for apex A records of custom domains."},
		},
	}
}

func (d *containerEnvDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func ceStrList(ctx context.Context, s []string) types.List {
	if s == nil {
		s = []string{}
	}
	l, _ := types.ListValueFrom(ctx, types.StringType, s)
	return l
}

func (d *containerEnvDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m containerEnvModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var v containerEnvView
	if _, err := d.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d", m.ServiceID.ValueInt64()), nil, nil, "", &v); err != nil {
		resp.Diagnostics.AddError("Reading the container environment failed", err.Error())
		return
	}
	m.Code, m.Namespace, m.Mode = types.StringValue(v.Code), types.StringValue(v.Namespace), types.StringValue(v.Mode)
	m.ServiceDomain, m.HostSuffix, m.Suspended = types.StringValue(v.ClusterDNS.ServiceDomain), types.StringValue(v.HostSuffix), types.BoolValue(v.Suspended)
	m.EgressIPs, m.EgressHash, m.EgressNote = ceStrList(ctx, v.EgressIPs), types.StringValue(v.Egress.Hash), types.StringValue(v.EgressNote)
	m.BuildEgressIPs, m.IngressIPs = ceStrList(ctx, v.Egress.BuildIPs), ceStrList(ctx, v.IngressIPs)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
