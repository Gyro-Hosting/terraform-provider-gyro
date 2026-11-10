package provider

import (
	"context"
	"fmt"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func configureDS(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
	}
	return pd
}

/* ------------------------------------------------------------------ gyro_products */

type productsDS struct{ pd *providerData }

func newProductsDataSource() datasource.DataSource { return &productsDS{} }

type productModel struct {
	ID           types.Int64   `tfsdk:"id"`
	Name         types.String  `tfsdk:"name"`
	Plan         types.String  `tfsdk:"plan"`
	Region       types.String  `tfsdk:"region"`
	ProductLine  types.String  `tfsdk:"product_line"`
	ServerType   types.String  `tfsdk:"server_type"`
	Location     types.String  `tfsdk:"location"`
	Cores        types.Int64   `tfsdk:"cores"`
	MemoryGB     types.Float64 `tfsdk:"memory_gb"`
	DiskGB       types.Int64   `tfsdk:"disk_gb"`
	CPUType      types.String  `tfsdk:"cpu_type"`
	Available    types.Bool    `tfsdk:"available"`
	MonthlyPrice types.Float64 `tfsdk:"monthly_price"`
	Currency     types.String  `tfsdk:"currency"`
}

type productsModel struct {
	Region        types.String   `tfsdk:"region"`
	Plan          types.String   `tfsdk:"plan"`
	Location      types.String   `tfsdk:"location"`
	ServerType    types.String   `tfsdk:"server_type"`
	AvailableOnly types.Bool     `tfsdk:"available_only"`
	Products      []productModel `tfsdk:"products"`
}

func (d *productsDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_products"
}

func (d *productsDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Orderable server products. Order one with gyro_server using its id (product_id) or its plan + region.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{Optional: true, Description: "Only products in this region, e.g. eu-de-1 (see gyro_locations)."},
			"plan":   schema.StringAttribute{Optional: true, Description: "Only products with this plan name, e.g. standart-2-4."},
			"location": schema.StringAttribute{Optional: true, Description: "Deprecated: legacy location filter. Use region.",
				DeprecationMessage: "location is deprecated; use region instead."},
			"server_type": schema.StringAttribute{Optional: true, Description: "Deprecated: legacy product code filter. Use plan.",
				DeprecationMessage: "server_type is deprecated; use plan instead."},
			"available_only": schema.BoolAttribute{Optional: true, Description: "Only products in stock (default false)."},
			"products": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":           schema.Int64Attribute{Computed: true, Description: "Product id (gyro_server.product_id)."},
				"name":         schema.StringAttribute{Computed: true, Description: "Display name, e.g. Gyro Bulut Standart 2-4."},
				"plan":         schema.StringAttribute{Computed: true, Description: "Plan name (gyro_server.plan), e.g. standart-2-4."},
				"region":       schema.StringAttribute{Computed: true, Description: "Region (gyro_server.region), e.g. eu-de-1."},
				"product_line": schema.StringAttribute{Computed: true, Description: "cloud_server (global cloud) or vds (Istanbul VDS)."},
				"server_type":  schema.StringAttribute{Computed: true, Description: "Deprecated: legacy product code. Use plan."},
				"location":     schema.StringAttribute{Computed: true, Description: "Deprecated: legacy location code. Use region."},
				"cores":        schema.Int64Attribute{Computed: true}, "memory_gb": schema.Float64Attribute{Computed: true},
				"disk_gb": schema.Int64Attribute{Computed: true}, "cpu_type": schema.StringAttribute{Computed: true, Description: "shared or dedicated."},
				"available": schema.BoolAttribute{Computed: true}, "monthly_price": schema.Float64Attribute{Computed: true, Description: "Monthly price (VAT excluded)."},
				"currency": schema.StringAttribute{Computed: true},
			}}},
		},
	}
}

func (d *productsDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *productsDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m productsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.pd.c.Products(ctx, client.ProductFilter{Region: m.Region.ValueString(), Plan: m.Plan.ValueString(),
		Location: m.Location.ValueString(), ServerType: m.ServerType.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Listing products failed", err.Error())
		return
	}
	m.Products = []productModel{}
	for _, p := range list {
		if m.AvailableOnly.ValueBool() && !p.Available {
			continue
		}
		pm := productModel{ID: types.Int64Value(p.ID), Name: types.StringValue(p.Name), Plan: types.StringValue(p.Plan),
			Region: types.StringValue(p.Region), ProductLine: types.StringValue(p.ProductLine), ServerType: types.StringValue(p.ServerType),
			Location: types.StringValue(p.Location), Cores: types.Int64PointerValue(p.Cores), MemoryGB: types.Float64PointerValue(p.MemoryGB),
			DiskGB: types.Int64PointerValue(p.DiskGB), CPUType: types.StringPointerValue(p.CPUType), Available: types.BoolValue(p.Available),
			MonthlyPrice: types.Float64Null(), Currency: types.StringNull()}
		for _, pr := range p.Prices {
			if pr.Cycle == "monthly" {
				pm.MonthlyPrice = types.Float64Value(pr.Amount)
				pm.Currency = types.StringValue(pr.Currency)
			}
		}
		m.Products = append(m.Products, pm)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

/* ------------------------------------------------------------------ gyro_locations */

type locationsDS struct{ pd *providerData }

func newLocationsDataSource() datasource.DataSource { return &locationsDS{} }

type locationModel struct {
	Region  types.String `tfsdk:"region"`
	Code    types.String `tfsdk:"code"`
	City    types.String `tfsdk:"city"`
	Country types.String `tfsdk:"country"`
	Zone    types.String `tfsdk:"zone"`
}

type locationsModel struct {
	Locations []locationModel `tfsdk:"locations"`
}

func (d *locationsDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_locations"
}

func (d *locationsDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Regions that have orderable products.",
		Attributes: map[string]schema.Attribute{
			"locations": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"region": schema.StringAttribute{Computed: true, Description: "Region name (gyro_server.region), e.g. eu-de-1, tr-ist-1."},
				"code":   schema.StringAttribute{Computed: true, Description: "Deprecated: legacy location code. Use region."},
				"city":   schema.StringAttribute{Computed: true}, "country": schema.StringAttribute{Computed: true, Description: "ISO country code."},
				"zone": schema.StringAttribute{Computed: true},
			}}},
		},
	}
}

func (d *locationsDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *locationsDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.pd.c.Locations(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing locations failed", err.Error())
		return
	}
	m := locationsModel{Locations: []locationModel{}}
	for _, l := range list {
		m.Locations = append(m.Locations, locationModel{types.StringValue(l.Region), types.StringValue(l.Code), types.StringValue(l.City), types.StringValue(l.Country), types.StringValue(l.Zone)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

/* ------------------------------------------------------------------ gyro_images */

type imagesDS struct{ pd *providerData }

func newImagesDataSource() datasource.DataSource { return &imagesDS{} }

type imageModel struct {
	Slug     types.String `tfsdk:"slug"`
	Name     types.String `tfsdk:"name"`
	OSFlavor types.String `tfsdk:"os_flavor"`
}

type imagesModel struct {
	OSFlavor types.String `tfsdk:"os_flavor"`
	Images   []imageModel `tfsdk:"images"`
}

func (d *imagesDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_images"
}

func (d *imagesDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Operating system images that can be used for gyro_server.image.",
		Attributes: map[string]schema.Attribute{
			"os_flavor": schema.StringAttribute{Optional: true, Description: "Filter, e.g. debian or ubuntu."},
			"images": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"slug": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true}, "os_flavor": schema.StringAttribute{Computed: true},
			}}},
		},
	}
}

func (d *imagesDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *imagesDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m imagesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := d.pd.c.Images(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing images failed", err.Error())
		return
	}
	m.Images = []imageModel{}
	for _, i := range list {
		if f := m.OSFlavor.ValueString(); f != "" && f != i.OSFlavor {
			continue
		}
		m.Images = append(m.Images, imageModel{types.StringValue(i.Slug), types.StringValue(i.Name), types.StringValue(i.OSFlavor)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

/* ------------------------------------------------------------------ gyro_services */

type servicesDS struct{ pd *providerData }

func newServicesDataSource() datasource.DataSource { return &servicesDS{} }

type serviceModel struct {
	ID          types.Int64  `tfsdk:"id"`
	ProductLine types.String `tfsdk:"product_line"`
	Name        types.String `tfsdk:"name"`
	Status      types.String `tfsdk:"status"`
	Host        types.String `tfsdk:"host"`
	IP          types.String `tfsdk:"ip"`
	CustomerID  types.Int64  `tfsdk:"customer_id"`
}

type servicesModel struct {
	ProductLine types.String   `tfsdk:"product_line"`
	Services    []serviceModel `tfsdk:"services"`
}

func (d *servicesDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_services"
}

func (d *servicesDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Services of every product line (cloud_server, vds, dedicated_server, mail, monitor, storage, license, ...).",
		Attributes: map[string]schema.Attribute{
			"product_line": schema.StringAttribute{Optional: true},
			"services": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.Int64Attribute{Computed: true}, "product_line": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
				"status": schema.StringAttribute{Computed: true}, "host": schema.StringAttribute{Computed: true}, "ip": schema.StringAttribute{Computed: true},
				"customer_id": schema.Int64Attribute{Computed: true},
			}}},
		},
	}
}

func (d *servicesDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *servicesDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m servicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	list, err := d.pd.c.Services(ctx, m.ProductLine.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Listing services failed", err.Error())
		return
	}
	m.Services = []serviceModel{}
	for _, s := range list {
		m.Services = append(m.Services, serviceModel{types.Int64Value(s.ID), types.StringValue(s.ProductLine), types.StringValue(s.Name), types.StringValue(s.Status),
			types.StringPointerValue(s.Host), types.StringPointerValue(s.IP), types.Int64PointerValue(s.CustomerID)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
