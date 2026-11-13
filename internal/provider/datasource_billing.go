package provider

// Prepaid billing data sources (Gyro Public API 2.17.4):
//   gyro_container_sizes   GET  /cloud/billing/container-sizes  (serverless container vCPU / memory pairs and prices)
//   gyro_prepaid_estimate  POST /cloud/billing/estimate         (hourly / daily / monthly cost of plans and serverless containers)

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

/* ------------------------------------------------------------------ gyro_container_sizes */

type containerSizesDS struct{ pd *providerData }

func newContainerSizesDataSource() datasource.DataSource { return &containerSizesDS{} }

type containerSizeItem struct {
	CPUm        types.Int64   `tfsdk:"cpu_m"`
	VCPU        types.Float64 `tfsdk:"vcpu"`
	MemoryMi    []int64       `tfsdk:"memory_mi"`
	HourlyMinEU types.Float64 `tfsdk:"hourly_eur_min"`
	HourlyMaxEU types.Float64 `tfsdk:"hourly_eur_max"`
}

type containerSizesModel struct {
	ID        types.String        `tfsdk:"id"`
	VCPUHour  types.Float64       `tfsdk:"vcpu_hour_eur"`
	GBHour    types.Float64       `tfsdk:"gb_hour_eur"`
	MaxVCPU   types.Int64         `tfsdk:"environment_max_vcpu"`
	MaxGB     types.Int64         `tfsdk:"environment_max_memory_gb"`
	EURRate   types.Float64       `tfsdk:"eur_rate"`
	LocalCurr types.String        `tfsdk:"local_currency"`
	Sizes     []containerSizeItem `tfsdk:"sizes"`
}

type containerSizesAPI struct {
	ProductID int64   `json:"product_id"`
	VCPUHour  float64 `json:"vcpu_hour_eur"`
	GBHour    float64 `json:"gb_hour_eur"`
	Limit     struct {
		VCPU     int64 `json:"vcpu"`
		MemoryGB int64 `json:"memory_gb"`
	} `json:"environment_limit"`
	EURRate   float64 `json:"eur_rate"`
	LocalCurr string  `json:"local_currency"`
	Sizes     []struct {
		CPUm      int64   `json:"cpu_m"`
		VCPU      float64 `json:"vcpu"`
		MemoryMi  []int64 `json:"memory_mi"`
		HourlyMin float64 `json:"hourly_eur_min"`
		HourlyMax float64 `json:"hourly_eur_max"`
	} `json:"sizes"`
}

func (d *containerSizesDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_sizes"
}

func (d *containerSizesDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Gyro Containers Serverless: the vCPU / memory pairs accepted for cpu_m / memory_mi of gyro_container_service in a serverless " +
			"environment, and the per-hour prices (EUR, VAT excluded; per running copy, no monthly cap). Requires the billing_read scope.",
		Attributes: map[string]schema.Attribute{
			"id":                        schema.StringAttribute{Computed: true},
			"vcpu_hour_eur":             schema.Float64Attribute{Computed: true, Description: "Price of one vCPU for one hour."},
			"gb_hour_eur":               schema.Float64Attribute{Computed: true, Description: "Price of one GB of memory for one hour."},
			"environment_max_vcpu":      schema.Int64Attribute{Computed: true, Description: "Default limit of vCPU running at once per environment."},
			"environment_max_memory_gb": schema.Int64Attribute{Computed: true, Description: "Default limit of memory (GB) running at once per environment."},
			"eur_rate":                  schema.Float64Attribute{Computed: true, Description: "EUR rate of your account currency."},
			"local_currency":            schema.StringAttribute{Computed: true},
			"sizes": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"cpu_m":          schema.Int64Attribute{Computed: true, Description: "CPU in millicores (250 = 0.25 vCPU)."},
				"vcpu":           schema.Float64Attribute{Computed: true},
				"memory_mi":      schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Allowed memory_mi values for this cpu_m."},
				"hourly_eur_min": schema.Float64Attribute{Computed: true, Description: "Hourly price with the smallest memory."},
				"hourly_eur_max": schema.Float64Attribute{Computed: true, Description: "Hourly price with the largest memory."},
			}}},
		},
	}
}

func (d *containerSizesDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *containerSizesDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var out containerSizesAPI
	if _, err := d.pd.c.Do(ctx, http.MethodGet, "cloud/billing/container-sizes", nil, nil, "", &out); err != nil {
		resp.Diagnostics.AddError("Reading container sizes failed", err.Error())
		return
	}
	m := containerSizesModel{ID: types.StringValue("container-sizes"), VCPUHour: types.Float64Value(out.VCPUHour), GBHour: types.Float64Value(out.GBHour),
		MaxVCPU: types.Int64Value(out.Limit.VCPU), MaxGB: types.Int64Value(out.Limit.MemoryGB), EURRate: types.Float64Value(out.EURRate),
		LocalCurr: types.StringValue(out.LocalCurr), Sizes: []containerSizeItem{}}
	for _, s := range out.Sizes {
		mem := s.MemoryMi
		if mem == nil {
			mem = []int64{}
		}
		m.Sizes = append(m.Sizes, containerSizeItem{CPUm: types.Int64Value(s.CPUm), VCPU: types.Float64Value(s.VCPU), MemoryMi: mem,
			HourlyMinEU: types.Float64Value(s.HourlyMin), HourlyMaxEU: types.Float64Value(s.HourlyMax)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

/* ------------------------------------------------------------------ gyro_prepaid_estimate */

type prepaidEstimateDS struct{ pd *providerData }

func newPrepaidEstimateDataSource() datasource.DataSource { return &prepaidEstimateDS{} }

type estimateItemModel struct {
	Plan      types.String  `tfsdk:"plan"`
	ProductID types.Int64   `tfsdk:"product_id"`
	CPUm      types.Int64   `tfsdk:"cpu_m"`
	MemoryMi  types.Int64   `tfsdk:"memory_mi"`
	Count     types.Int64   `tfsdk:"count"`
	Hours     types.Int64   `tfsdk:"hours"`
	Type      types.String  `tfsdk:"type"`
	HourlyEUR types.Float64 `tfsdk:"hourly_eur"`
	Monthly   types.Float64 `tfsdk:"monthly"`
	MonthEUR  types.Float64 `tfsdk:"monthly_eur"`
	Capped    types.Bool    `tfsdk:"capped"`
	Total     types.Float64 `tfsdk:"total"`
}

type prepaidEstimateModel struct {
	ID         types.String        `tfsdk:"id"`
	Items      []estimateItemModel `tfsdk:"items"`
	Currency   types.String        `tfsdk:"currency"`
	Hourly     types.Float64       `tfsdk:"hourly"`
	Daily      types.Float64       `tfsdk:"daily"`
	Monthly    types.Float64       `tfsdk:"monthly"`
	Balance    types.Float64       `tfsdk:"balance"`
	HoursCover types.Float64       `tfsdk:"hours_covered"`
	OrderReq   types.Float64       `tfsdk:"order_requires"`
	CanOrder   types.Bool          `tfsdk:"can_order"`
}

type estimateAPI struct {
	Items []struct {
		ProductID  int64   `json:"product_id"`
		Plan       *string `json:"plan"`
		Type       string  `json:"type"`
		HourlyEUR  float64 `json:"hourly_eur"`
		MonthlyCap money   `json:"monthly_cap"`
		MonthEUR   float64 `json:"monthly_cap_eur"`
		Capped     *bool   `json:"capped"`
		Total      *money  `json:"total"`
	} `json:"items"`
	Hourly       money    `json:"hourly"`
	Daily        money    `json:"daily"`
	MonthlyCap   money    `json:"monthly_cap"`
	Balance      money    `json:"balance"`
	HoursCovered *float64 `json:"hours_covered"`
	OrderReq     money    `json:"order_requires"`
	CanOrder     bool     `json:"can_order"`
}

func (d *prepaidEstimateDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prepaid_estimate"
}

func (d *prepaidEstimateDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Cost of prepaid (hourly, from the account balance) Gyro Cloud Istanbul servers, Gyro Containers plans and Gyro Containers Serverless " +
			"containers, and how long the current balance would last. Amounts are in your account currency, VAT excluded. Nothing is ordered. " +
			"Requires the billing_read scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true},
			"items": schema.ListNestedAttribute{Required: true, Description: "Up to 50 items. A plan item sets plan (or product_id); a serverless container item sets cpu_m + memory_mi (pairs: data.gyro_container_sizes).",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"plan":        schema.StringAttribute{Optional: true, Description: "Prepaid plan name, e.g. standart-2-4 or ecs-baslangic."},
					"product_id":  schema.Int64Attribute{Optional: true},
					"cpu_m":       schema.Int64Attribute{Optional: true, Description: "Serverless container: CPU per copy in millicores (250, 500, 1000, 2000, 4000)."},
					"memory_mi":   schema.Int64Attribute{Optional: true, Description: "Serverless container: memory per copy in MiB."},
					"count":       schema.Int64Attribute{Optional: true, Description: "Copies / servers (default 1, max 100)."},
					"hours":       schema.Int64Attribute{Optional: true, Description: "Serverless container: running hours for total (default 730 = a full month; no monthly cap)."},
					"type":        schema.StringAttribute{Computed: true, Description: "server, container or serverless."},
					"hourly_eur":  schema.Float64Attribute{Computed: true},
					"monthly":     schema.Float64Attribute{Computed: true, Description: "Monthly price (capped plans) or a full month (730 hours) of running time (serverless; no monthly cap)."},
					"monthly_eur": schema.Float64Attribute{Computed: true},
					"capped":      schema.BoolAttribute{Computed: true, Description: "true: the item never costs more than its monthly price in a calendar month."},
					"total":       schema.Float64Attribute{Computed: true, Description: "Serverless: hours x hourly; plans: same as monthly."},
				}}},
			"currency":       schema.StringAttribute{Computed: true},
			"hourly":         schema.Float64Attribute{Computed: true},
			"daily":          schema.Float64Attribute{Computed: true},
			"monthly":        schema.Float64Attribute{Computed: true},
			"balance":        schema.Float64Attribute{Computed: true},
			"hours_covered":  schema.Float64Attribute{Computed: true, Description: "How many hours the balance would carry these items plus what is already running."},
			"order_requires": schema.Float64Attribute{Computed: true, Description: "Balance needed to order these items (first 24 hours)."},
			"can_order":      schema.BoolAttribute{Computed: true},
		},
	}
}

func (d *prepaidEstimateDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *prepaidEstimateDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m prepaidEstimateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	items := []map[string]any{}
	for _, it := range m.Items {
		x := map[string]any{}
		if v := it.Plan.ValueString(); v != "" {
			x["plan"] = v
		}
		if !it.ProductID.IsNull() && !it.ProductID.IsUnknown() {
			x["product_id"] = it.ProductID.ValueInt64()
		}
		if !it.CPUm.IsNull() && !it.CPUm.IsUnknown() {
			x["type"] = "serverless"
			x["cpu_m"] = it.CPUm.ValueInt64()
		}
		if !it.MemoryMi.IsNull() && !it.MemoryMi.IsUnknown() {
			x["type"] = "serverless"
			x["memory_mi"] = it.MemoryMi.ValueInt64()
		}
		if !it.Count.IsNull() && !it.Count.IsUnknown() {
			x["count"] = it.Count.ValueInt64()
		}
		if !it.Hours.IsNull() && !it.Hours.IsUnknown() {
			x["hours"] = it.Hours.ValueInt64()
		}
		items = append(items, x)
	}
	var out estimateAPI
	if _, err := d.pd.c.Do(ctx, http.MethodPost, "cloud/billing/estimate", nil, map[string]any{"items": items}, "", &out); err != nil {
		resp.Diagnostics.AddError("Estimating the prepaid cost failed", err.Error())
		return
	}
	if len(out.Items) != len(m.Items) {
		resp.Diagnostics.AddError("Estimating the prepaid cost failed", "the API returned a different number of items")
		return
	}
	for i, o := range out.Items {
		capped := o.Type != "serverless"
		if o.Capped != nil {
			capped = *o.Capped
		}
		total := o.MonthlyCap.Amount
		if o.Total != nil {
			total = o.Total.Amount
		}
		m.Items[i].Type = types.StringValue(o.Type)
		m.Items[i].HourlyEUR = types.Float64Value(o.HourlyEUR)
		m.Items[i].Monthly = types.Float64Value(o.MonthlyCap.Amount)
		m.Items[i].MonthEUR = types.Float64Value(o.MonthEUR)
		m.Items[i].Capped = types.BoolValue(capped)
		m.Items[i].Total = types.Float64Value(total)
	}
	m.ID = types.StringValue("prepaid-estimate")
	m.Currency = types.StringValue(out.Hourly.Currency)
	m.Hourly = types.Float64Value(out.Hourly.Amount)
	m.Daily = types.Float64Value(out.Daily.Amount)
	m.Monthly = types.Float64Value(out.MonthlyCap.Amount)
	m.Balance = types.Float64Value(out.Balance.Amount)
	if out.HoursCovered != nil {
		m.HoursCover = types.Float64Value(*out.HoursCovered)
	} else {
		m.HoursCover = types.Float64Null()
	}
	m.OrderReq = types.Float64Value(out.OrderReq.Amount)
	m.CanOrder = types.BoolValue(out.CanOrder)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
