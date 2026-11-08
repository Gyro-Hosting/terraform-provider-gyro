package provider

import (
	"context"
	"fmt"

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
		Description: "Orderable cloud server products.",
		Attributes: map[string]schema.Attribute{
			"location":       schema.StringAttribute{Optional: true, Description: "Filter by location code."},
			"server_type":    schema.StringAttribute{Optional: true, Description: "Filter by server type."},
			"available_only": schema.BoolAttribute{Optional: true, Description: "Only products in stock (default false)."},
			"products": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.Int64Attribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
				"server_type": schema.StringAttribute{Computed: true}, "location": schema.StringAttribute{Computed: true},
				"cores": schema.Int64Attribute{Computed: true}, "memory_gb": schema.Float64Attribute{Computed: true},
				"disk_gb": schema.Int64Attribute{Computed: true}, "cpu_type": schema.StringAttribute{Computed: true},
				"available": schema.BoolAttribute{Computed: true}, "monthly_price": schema.Float64Attribute{Computed: true},
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
	list, err := d.pd.c.Products(ctx, m.Location.ValueString(), m.ServerType.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Listing products failed", err.Error())
		return
	}
	m.Products = []productModel{}
	for _, p := range list {
		if m.AvailableOnly.ValueBool() && !p.Available {
			continue
		}
		pm := productModel{ID: types.Int64Value(p.ID), Name: types.StringValue(p.Name), ServerType: types.StringValue(p.ServerType),
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
		Description: "Locations that have orderable products.",
		Attributes: map[string]schema.Attribute{
			"locations": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"code": schema.StringAttribute{Computed: true}, "city": schema.StringAttribute{Computed: true},
				"country": schema.StringAttribute{Computed: true}, "zone": schema.StringAttribute{Computed: true},
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
		m.Locations = append(m.Locations, locationModel{types.StringValue(l.Code), types.StringValue(l.City), types.StringValue(l.Country), types.StringValue(l.Zone)})
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
