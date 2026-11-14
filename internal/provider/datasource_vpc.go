package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

/* ------------------------------------------------------------------ gyro_vpc data source (API 2.29.0) */

type vpcDS struct{ pd *providerData }

func newVPCDataSource() datasource.DataSource { return &vpcDS{} }

type vpcDSModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	CIDR            types.String `tfsdk:"cidr"`
	Region          types.String `tfsdk:"region"`
	ProjectID       types.Int64  `tfsdk:"project_id"`
	State           types.String `tfsdk:"state"`
	GatewayEnabled  types.Bool   `tfsdk:"gateway_enabled"`
	GatewayNAT      types.Bool   `tfsdk:"gateway_nat"`
	InterSubnet     types.String `tfsdk:"inter_subnet"`
	GatewayPublicIP types.String `tfsdk:"gateway_public_ip"`
	GatewayStatus   types.String `tfsdk:"gateway_status"`
	DefaultSubnetID types.String `tfsdk:"default_subnet_id"`
	Subnets         types.List   `tfsdk:"subnets"`
}

var vpcDSSubnetType = map[string]attr.Type{
	"id": types.StringType, "name": types.StringType, "cidr": types.StringType, "gateway_ip": types.StringType,
	"used": types.Int64Type, "capacity": types.Int64Type, "state": types.StringType,
}

func (d *vpcDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (d *vpcDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up one private network (VPC) of Gyro Cloud Istanbul (tr-ist-1) by id or by name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Optional: true, Computed: true, Description: "VPC id. Set id or name.",
				Validators: []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}},
			"name":              schema.StringAttribute{Optional: true, Computed: true, Description: "VPC name. Set id or name."},
			"cidr":              schema.StringAttribute{Computed: true},
			"region":            schema.StringAttribute{Computed: true},
			"project_id":        schema.Int64Attribute{Computed: true},
			"state":             schema.StringAttribute{Computed: true},
			"gateway_enabled":   schema.BoolAttribute{Computed: true, Description: "Whether the VPC gateway (NAT, hourly prepaid billing) is on."},
			"gateway_nat":       schema.BoolAttribute{Computed: true},
			"inter_subnet":      schema.StringAttribute{Computed: true, Description: "allow or deny."},
			"gateway_public_ip": schema.StringAttribute{Computed: true},
			"gateway_status":    schema.StringAttribute{Computed: true, Description: "off, active or syncing."},
			"default_subnet_id": schema.StringAttribute{Computed: true, Description: "Id of the subnet created with the VPC."},
			"subnets": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":         schema.StringAttribute{Computed: true},
				"name":       schema.StringAttribute{Computed: true},
				"cidr":       schema.StringAttribute{Computed: true},
				"gateway_ip": schema.StringAttribute{Computed: true},
				"used":       schema.Int64Attribute{Computed: true},
				"capacity":   schema.Int64Attribute{Computed: true},
				"state":      schema.StringAttribute{Computed: true},
			}}},
		},
	}
}

func (d *vpcDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *vpcDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m vpcDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var v vpcView
	if !m.ID.IsNull() && m.ID.ValueString() != "" {
		if _, err := d.pd.c.Do(ctx, http.MethodGet, vpcPath(m.ID.ValueString()), nil, nil, "", &v); err != nil {
			resp.Diagnostics.AddError("Reading the VPC failed", err.Error())
			return
		}
	} else {
		var out struct {
			VPCs []vpcView `json:"vpcs"`
		}
		if _, err := d.pd.c.Do(ctx, http.MethodGet, "cloud/vpcs", nil, nil, "", &out); err != nil {
			resp.Diagnostics.AddError("Listing VPCs failed", err.Error())
			return
		}
		var hit []vpcView
		for _, x := range out.VPCs {
			if x.Name == m.Name.ValueString() {
				hit = append(hit, x)
			}
		}
		if len(hit) != 1 {
			resp.Diagnostics.AddError("VPC not found", fmt.Sprintf("%d VPCs are named %q (need exactly one).", len(hit), m.Name.ValueString()))
			return
		}
		v = hit[0]
	}
	m.ID, m.Name, m.CIDR, m.Region = types.StringValue(string(v.ID)), types.StringValue(v.Name), types.StringValue(v.CIDR), types.StringValue(v.Region)
	m.ProjectID, m.State = types.Int64Value(pidOf(v.ProjectID)), types.StringValue(v.State)
	m.GatewayEnabled, m.GatewayNAT = types.BoolValue(v.Gateway.Enabled), types.BoolValue(v.Gateway.NAT)
	is := v.Gateway.InterSubnet
	if is == "" {
		is = "allow"
	}
	m.InterSubnet, m.GatewayPublicIP, m.GatewayStatus = types.StringValue(is), types.StringValue(v.Gateway.PublicIP), types.StringValue(v.Gateway.Status)
	m.DefaultSubnetID = types.StringValue(defaultSubnet(&v))
	objs := []attr.Value{}
	for _, s := range v.Subnets {
		o, diags := types.ObjectValue(vpcDSSubnetType, map[string]attr.Value{
			"id": types.StringValue(string(s.ID)), "name": types.StringValue(s.Name), "cidr": types.StringValue(s.CIDR),
			"gateway_ip": types.StringValue(s.GatewayIP), "used": types.Int64Value(s.Used), "capacity": types.Int64Value(s.Capacity),
			"state": types.StringValue(s.State),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, o)
	}
	l, diags := types.ListValue(types.ObjectType{AttrTypes: vpcDSSubnetType}, objs)
	resp.Diagnostics.Append(diags...)
	m.Subnets = l
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
