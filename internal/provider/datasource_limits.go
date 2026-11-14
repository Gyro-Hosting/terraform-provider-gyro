package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

/* ------------------------------------------------------------------ gyro_account_limits (API 2.26.0) */

type accountLimitsDS struct{ pd *providerData }

func newAccountLimitsDataSource() datasource.DataSource { return &accountLimitsDS{} }

type accountLimitServiceModel struct {
	ServiceID types.Int64  `tfsdk:"service_id"`
	Name      types.String `tfsdk:"name"`
	Limit     types.Int64  `tfsdk:"limit"`
	Used      types.Int64  `tfsdk:"used"`
}

type accountLimitModel struct {
	Key           types.String               `tfsdk:"key"`
	Group         types.String               `tfsdk:"group"`
	Scope         types.String               `tfsdk:"scope"`
	Limit         types.Int64                `tfsdk:"limit"`
	Used          types.Int64                `tfsdk:"used"`
	Unit          types.String               `tfsdk:"unit"`
	Custom        types.Bool                 `tfsdk:"custom"`
	Increasable   types.Bool                 `tfsdk:"increasable"`
	HowToIncrease types.String               `tfsdk:"how_to_increase"`
	Description   types.String               `tfsdk:"description"`
	Services      []accountLimitServiceModel `tfsdk:"services"`
}

type accountLimitsModel struct {
	NewAccountApplies     types.Bool          `tfsdk:"new_account_applies"`
	NewAccountServers     types.Int64         `tfsdk:"new_account_servers"`
	NewAccountServersUsed types.Int64         `tfsdk:"new_account_servers_used"`
	NewAccountEnvs        types.Int64         `tfsdk:"new_account_container_environments"`
	NewAccountEndsAt      types.String        `tfsdk:"new_account_ends_at"`
	Limits                []accountLimitModel `tfsdk:"limits"`
}

func (d *accountLimitsDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_account_limits"
}

func i64(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

func (d *accountLimitsDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Every limit of the account with its current usage (GET /cloud/account/limits). Account limits are raised with a support ticket " +
			"(POST /api/v1/client/tickets or `gyro ticket create --department-id 3 ...`); plan limits also grow with a bigger plan or an add-on. " +
			"Use it in a precondition to fail early, e.g. before creating more servers than new_account_servers allows.",
		Attributes: map[string]schema.Attribute{
			"new_account_applies":                schema.BoolAttribute{Computed: true, Description: "True while the account is in its first days (prepaid cloud new-account limit applies)."},
			"new_account_servers":                schema.Int64Attribute{Computed: true, Description: "New-account limit: Gyro Cloud Istanbul servers."},
			"new_account_servers_used":           schema.Int64Attribute{Computed: true, Description: "Gyro Cloud Istanbul servers that count toward the new-account limit."},
			"new_account_container_environments": schema.Int64Attribute{Computed: true, Description: "New-account limit: container environments."},
			"new_account_ends_at":                schema.StringAttribute{Computed: true, Description: "When the new-account limit ends (Europe/Istanbul), empty when it does not apply."},
			"limits": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"key":             schema.StringAttribute{Computed: true, Description: "Limit name, e.g. compute.max_vms, api.max_servers, s3.max_buckets."},
				"group":           schema.StringAttribute{Computed: true},
				"scope":           schema.StringAttribute{Computed: true, Description: "account or service (then see services)."},
				"limit":           schema.Int64Attribute{Computed: true, Description: "Effective limit (null: per service / per plan, or no limit)."},
				"used":            schema.Int64Attribute{Computed: true},
				"unit":            schema.StringAttribute{Computed: true},
				"custom":          schema.BoolAttribute{Computed: true, Description: "Changed for this account."},
				"increasable":     schema.BoolAttribute{Computed: true},
				"how_to_increase": schema.StringAttribute{Computed: true, Description: "open_support_ticket, upgrade_plan_or_open_support_ticket, upgrade_plan, wait or fixed."},
				"description":     schema.StringAttribute{Computed: true},
				"services": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"service_id": schema.Int64Attribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
					"limit": schema.Int64Attribute{Computed: true}, "used": schema.Int64Attribute{Computed: true},
				}}},
			}}},
		},
	}
}

func (d *accountLimitsDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = configureDS(req, resp)
}

func (d *accountLimitsDS) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	l, err := d.pd.c.AccountLimits(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading account limits failed", err.Error())
		return
	}
	m := accountLimitsModel{NewAccountApplies: types.BoolValue(false), NewAccountServers: types.Int64Null(), NewAccountServersUsed: types.Int64Null(),
		NewAccountEnvs: types.Int64Null(), NewAccountEndsAt: types.StringValue(""), Limits: []accountLimitModel{}}
	if n := l.NewAccount; n != nil {
		m.NewAccountApplies = types.BoolValue(n.Applies)
		m.NewAccountServers = types.Int64Value(n.Servers.Limit)
		m.NewAccountServersUsed = types.Int64Value(n.Servers.Used)
		m.NewAccountEnvs = types.Int64Value(n.ContainerEnvironments.Limit)
		m.NewAccountEndsAt = types.StringValue(n.EndsAt)
	}
	for _, r := range l.Limits {
		x := accountLimitModel{types.StringValue(r.Key), types.StringValue(r.Group), types.StringValue(r.Scope), i64(r.Limit), i64(r.Used), types.StringValue(r.Unit),
			types.BoolValue(r.Custom), types.BoolValue(r.Increasable), types.StringValue(r.HowToIncrease), types.StringValue(r.Description), []accountLimitServiceModel{}}
		for _, s := range r.Services {
			x.Services = append(x.Services, accountLimitServiceModel{types.Int64Value(s.ServiceID), types.StringValue(s.Name), i64(s.Limit), i64(s.Used)})
		}
		m.Limits = append(m.Limits, x)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
