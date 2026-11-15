package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_container_logs (v0.31.0, API 2.33.0, GYR-19): log package (retention days x monthly GB, prepaid hourly) and the log alert
// rules of a Gyro Containers environment (PUT /cloud/containers/{id}/logs/retention, GET|PUT .../log-rules, GET .../logs/usage).

var _ resource.ResourceWithImportState = (*containerLogsResource)(nil)

type containerLogsResource struct{ pd *providerData }

func newContainerLogsResource() resource.Resource { return &containerLogsResource{} }

type containerLogRuleModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Service       types.String `tfsdk:"service"`
	Pattern       types.String `tfsdk:"pattern"`
	Level         types.String `tfsdk:"level"`
	Threshold     types.Int64  `tfsdk:"threshold"`
	WindowMinutes types.Int64  `tfsdk:"window_minutes"`
	Enabled       types.Bool   `tfsdk:"enabled"`
}

type containerLogsModel struct {
	ID            types.String            `tfsdk:"id"`
	ServiceID     types.Int64             `tfsdk:"service_id"`
	RetentionDays types.Int64             `tfsdk:"retention_days"`
	GBMonth       types.Int64             `tfsdk:"gb_month"`
	MonthlyEUR    types.Float64           `tfsdk:"monthly_eur"`
	Rules         []containerLogRuleModel `tfsdk:"rules"`
}

func (r *containerLogsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_logs"
}

func (r *containerLogsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Log package and log alert rules of a Gyro Containers (ECS) environment. Free package: 7 days, 5 GB per month. Paid packages: " +
			"retention_days 30 | 90 | 365 with gb_month 10 | 50 | 100 | 300 (365 days: up to 100), billed hourly from the prepaid balance; no overage bill " +
			"(when the month's size is used up the oldest logs go first). Rules (at most 10) notify the environment's alert channels (gyro_container_notifications) " +
			"when a text / level shows up at least `threshold` times within `window_minutes`. Destroy = free package and no rules.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id":     schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id."},
			"retention_days": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(7), Description: "7 (free), 30, 90 or 365."},
			"gb_month":       schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(5), Description: "Monthly log volume of the package (5 with 7 days; 10, 50, 100, 300 for paid packages)."},
			"monthly_eur":    schema.Float64Attribute{Computed: true, Description: "Monthly price of the package (EUR, VAT excluded; 0 = free)."},
			"rules": schema.ListNestedAttribute{Optional: true, Description: "Log alert rules (at most 10).",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Rule id (kept while the rule stays at the same position)."},
					"name":           schema.StringAttribute{Required: true, Description: "Name (max 60)."},
					"service":        schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Only this service (empty = all)."},
					"pattern":        schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Text the line contains (case-insensitive). pattern or level is required."},
					"level":          schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "error | warn | info | debug (empty = any)."},
					"threshold":      schema.Int64Attribute{Required: true, Description: "Matches needed in the window (1-100000)."},
					"window_minutes": schema.Int64Attribute{Required: true, Description: "Window in minutes (1-60)."},
					"enabled":        schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
				}}},
		},
	}
}

func (r *containerLogsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

type logUsageView struct {
	Days    int64 `json:"days"`
	GBMonth int64 `json:"gb_month"`
	Package struct {
		MonthlyEUR float64 `json:"monthly_eur"`
	} `json:"package"`
}

type logRuleView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Service       string `json:"service"`
	Pattern       string `json:"pattern"`
	Level         string `json:"level"`
	Threshold     int64  `json:"threshold"`
	WindowMinutes int64  `json:"window_minutes"`
	Enabled       bool   `json:"enabled"`
}

func (r *containerLogsResource) read(ctx context.Context, m *containerLogsModel) error {
	s := sid(m.ServiceID)
	var u logUsageView
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d/logs/usage", s), nil, nil, "", &u); err != nil {
		return err
	}
	var rv struct {
		Rules []logRuleView `json:"rules"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d/log-rules", s), nil, nil, "", &rv); err != nil {
		return err
	}
	m.ID = types.StringValue(strconv.FormatInt(s, 10))
	m.RetentionDays, m.GBMonth, m.MonthlyEUR = types.Int64Value(u.Days), types.Int64Value(u.GBMonth), types.Float64Value(u.Package.MonthlyEUR)
	if len(rv.Rules) == 0 && m.Rules == nil {
		return nil
	}
	out := make([]containerLogRuleModel, 0, len(rv.Rules))
	for _, x := range rv.Rules {
		out = append(out, containerLogRuleModel{ID: types.StringValue(x.ID), Name: types.StringValue(x.Name), Service: types.StringValue(x.Service),
			Pattern: types.StringValue(x.Pattern), Level: types.StringValue(x.Level), Threshold: types.Int64Value(x.Threshold),
			WindowMinutes: types.Int64Value(x.WindowMinutes), Enabled: types.BoolValue(x.Enabled)})
	}
	m.Rules = out
	return nil
}

func (r *containerLogsResource) write(ctx context.Context, m *containerLogsModel, prev *containerLogsModel) error {
	s := sid(m.ServiceID)
	d, g := m.RetentionDays.ValueInt64(), m.GBMonth.ValueInt64()
	if prev == nil || prev.RetentionDays.ValueInt64() != d || prev.GBMonth.ValueInt64() != g {
		var cur logUsageView
		_, _ = r.pd.c.Do(ctx, http.MethodGet, fmt.Sprintf("cloud/containers/%d/logs/usage", s), nil, nil, "", &cur)
		if cur.Days != d || cur.GBMonth != g {
			body := map[string]any{"days": d, "gb": g, "confirm": true}
			if d == 7 {
				body = map[string]any{"free": true, "confirm": true}
			}
			if _, err := r.pd.c.TypedWrite(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/logs/retention", s), body); err != nil {
				return err
			}
		}
	}
	rules := []map[string]any{}
	for i, x := range m.Rules {
		id := ""
		if prev != nil && i < len(prev.Rules) {
			id = prev.Rules[i].ID.ValueString()
		}
		rules = append(rules, map[string]any{"id": id, "name": x.Name.ValueString(), "service": x.Service.ValueString(), "pattern": x.Pattern.ValueString(),
			"level": x.Level.ValueString(), "threshold": x.Threshold.ValueInt64(), "window_minutes": x.WindowMinutes.ValueInt64(), "enabled": x.Enabled.ValueBool()})
	}
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/log-rules", s), map[string]any{"rules": rules})
	return err
}

func (r *containerLogsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerLogsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &m, nil); err != nil {
		resp.Diagnostics.AddError("Setting container logs failed", err.Error())
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container logs failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerLogsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerLogsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container logs failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerLogsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st containerLogsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &m, &st); err != nil {
		resp.Diagnostics.AddError("Setting container logs failed", err.Error())
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container logs failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerLogsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerLogsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	s := sid(m.ServiceID)
	_, _ = r.pd.c.TypedWrite(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/log-rules", s), map[string]any{"rules": []any{}})
	if m.RetentionDays.ValueInt64() != 7 {
		_, _ = r.pd.c.TypedWrite(ctx, http.MethodPut, fmt.Sprintf("cloud/containers/%d/logs/retention", s), map[string]any{"free": true, "confirm": true})
	}
}

func (r *containerLogsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the container environment (service) id, e.g. 123")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
