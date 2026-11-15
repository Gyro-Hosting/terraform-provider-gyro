package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
)

// gyro_container_schedule (v0.34.0, API 2.36.0, GYR-12): scheduled job of a Gyro Containers environment
// (cloud/containers/{id}/schedules): cron / every N minutes / once, a service command or an HTTP call.

var _ resource.ResourceWithImportState = (*containerScheduleResource)(nil)
var _ resource.ResourceWithValidateConfig = (*containerScheduleResource)(nil)

type containerScheduleResource struct{ pd *providerData }

func newContainerScheduleResource() resource.Resource { return &containerScheduleResource{} }

type containerScheduleModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.Int64  `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Cron          types.String `tfsdk:"cron"`
	RateMinutes   types.Int64  `tfsdk:"rate_minutes"`
	At            types.String `tfsdk:"at"`
	Timezone      types.String `tfsdk:"timezone"`
	Service       types.String `tfsdk:"service"`
	Command       types.List   `tfsdk:"command"`
	TimeoutSecs   types.Int64  `tfsdk:"timeout_seconds"`
	CPUM          types.Int64  `tfsdk:"cpu_m"`
	MemoryMi      types.Int64  `tfsdk:"memory_mi"`
	URL           types.String `tfsdk:"url"`
	Method        types.String `tfsdk:"method"`
	Headers       types.Map    `tfsdk:"headers"`
	Body          types.String `tfsdk:"body"`
	HTTPTimeout   types.Int64  `tfsdk:"http_timeout_seconds"`
	Retries       types.Int64  `tfsdk:"retries"`
	Paused        types.Bool   `tfsdk:"paused"`
	Target        types.String `tfsdk:"target"`
	State         types.String `tfsdk:"state"`
	NextRun       types.String `tfsdk:"next_run"`
}

func (r *containerScheduleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_schedule"
}

func (r *containerScheduleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepS := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	keepI := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Scheduled job of a Gyro Containers (ECS) environment: a service command (run as a one-off job with the image of the running version, " +
			"its variables and secrets) or an HTTP call, on a cron expression (time zone aware), every N minutes or once. A run never starts while the previous run " +
			"of the same schedule is still going; failed runs can be retried (0-3). Notifications: schedule_failed / schedule_missed (gyro_container_notifications). " +
			"Shortest interval 1 minute (Serverless 5). Free on node plans; on Serverless each run is billed like a task.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "Schedule id."},
			"environment_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id."},
			"name": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Description: "2-40 characters: lowercase letters, digits and '-', starting with a letter."},
			"description":  schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("")},
			"cron":         schema.StringAttribute{Optional: true, Description: "Cron expression (5 fields: minute hour day month weekday, e.g. \"0 3 * * *\"; @daily, @hourly). One of cron, rate_minutes, at."},
			"rate_minutes": schema.Int64Attribute{Optional: true, Description: "Run every N minutes (1-10080; Serverless at least 5)."},
			"at":           schema.StringAttribute{Optional: true, Description: "Run once at this time: RFC 3339, or local date-time (2026-12-01T03:00) in `timezone`. Must be in the future when set."},
			"timezone":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("Europe/Istanbul"), Description: "IANA time zone (default Europe/Istanbul)."},
			"service":      schema.StringAttribute{Optional: true, Description: "Service whose image, variables and secrets the command uses (service target)."},
			"command": schema.ListAttribute{ElementType: types.StringType, Optional: true,
				Description: "Command and arguments (replaces the image ENTRYPOINT; no shell unless you write [\"sh\",\"-c\",\"...\"]). Omit = the image's own command."},
			"timeout_seconds": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepI, Description: "Time limit of a run (10-21600, default 1800; service target)."},
			"cpu_m":           schema.Int64Attribute{Optional: true, Description: "vCPU in millicores for a run (default: the service's)."},
			"memory_mi":       schema.Int64Attribute{Optional: true, Description: "Memory in MiB for a run (default: the service's)."},
			"url":             schema.StringAttribute{Optional: true, Description: "URL to call (http:// or https://; HTTP target). Private / platform addresses are refused; redirects are not followed."},
			"method":          schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepS, Description: "HTTP method (GET, POST, PUT, PATCH, DELETE, HEAD; default GET)."},
			"headers": schema.MapAttribute{ElementType: types.StringType, Optional: true, Sensitive: true,
				Description: "HTTP headers (at most 20). Kept secret by the API (never read back): Terraform keeps the configured values."},
			"body":                 schema.StringAttribute{Optional: true, Sensitive: true, Description: "HTTP body (at most 64 KB, not for GET / HEAD). Never read back."},
			"http_timeout_seconds": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepI, Description: "Time limit of the HTTP call (1-120, default 30)."},
			"retries":              schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Retries after a failed run (0-3; 30 s × attempt apart)."},
			"paused":               schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Paused schedules do not run."},
			"target":               schema.StringAttribute{Computed: true, PlanModifiers: keepS, Description: "service or http."},
			"state":                schema.StringAttribute{Computed: true, Description: "active, paused or completed (a one-time schedule that ran)."},
			"next_run":             schema.StringAttribute{Computed: true, Description: "Next run (UTC, RFC 3339); empty when paused / completed."},
		},
	}
}

func (r *containerScheduleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func (r *containerScheduleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m containerScheduleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n := 0
	for _, set := range []bool{!m.Cron.IsNull(), !m.RateMinutes.IsNull(), !m.At.IsNull()} {
		if set {
			n++
		}
	}
	if n != 1 && !m.Cron.IsUnknown() && !m.RateMinutes.IsUnknown() && !m.At.IsUnknown() {
		resp.Diagnostics.AddError("Invalid schedule", "Set exactly one of cron, rate_minutes, at.")
	}
	if !m.Service.IsNull() && !m.URL.IsNull() {
		resp.Diagnostics.AddError("Invalid target", "Set either service (a command) or url (an HTTP call), not both.")
	}
	if m.Service.IsNull() && m.URL.IsNull() && !m.Service.IsUnknown() && !m.URL.IsUnknown() {
		resp.Diagnostics.AddError("Invalid target", "Set service (with an optional command) or url.")
	}
	if !m.URL.IsNull() && !m.Command.IsNull() {
		resp.Diagnostics.AddError("Invalid target", "command is for a service target; an HTTP call has no command.")
	}
}

func schedPath(env int64, id string) string {
	p := fmt.Sprintf("cloud/containers/%d/schedules", env)
	if id != "" {
		p += "/" + id
	}
	return p
}

type schedView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"`
	Cron        string            `json:"cron"`
	RateMinutes int64             `json:"rate_minutes"`
	At          string            `json:"at"`
	Timezone    string            `json:"timezone"`
	Target      string            `json:"target"`
	Service     string            `json:"service"`
	Command     []string          `json:"command"`
	Timeout     int64             `json:"timeout_seconds"`
	CPU         int64             `json:"cpu_m"`
	Memory      int64             `json:"memory_mi"`
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	HTTPTimeout int64             `json:"http_timeout_seconds"`
	Retries     int64             `json:"retries"`
	Paused      bool              `json:"paused"`
	State       string            `json:"state"`
	NextRun     *string           `json:"next_run"`
}

func (m *containerScheduleModel) body(ctx context.Context, st *containerScheduleModel) map[string]any {
	b := map[string]any{"description": m.Description.ValueString(), "timezone": m.Timezone.ValueString(), "retries": m.Retries.ValueInt64(), "paused": m.Paused.ValueBool()}
	timing := st == nil || !m.Cron.Equal(st.Cron) || !m.RateMinutes.Equal(st.RateMinutes) || !m.At.Equal(st.At) || !m.Timezone.Equal(st.Timezone)
	if timing {
		switch {
		case !m.Cron.IsNull():
			b["cron"] = m.Cron.ValueString()
		case !m.RateMinutes.IsNull():
			b["rate_minutes"] = m.RateMinutes.ValueInt64()
		case !m.At.IsNull():
			b["at"] = m.At.ValueString()
		}
	} else {
		delete(b, "timezone")
	}
	if !m.URL.IsNull() {
		b["target"], b["url"] = "http", m.URL.ValueString()
		if !m.Method.IsNull() && !m.Method.IsUnknown() {
			b["method"] = strings.ToUpper(m.Method.ValueString())
		}
		h := map[string]string{}
		if !m.Headers.IsNull() && !m.Headers.IsUnknown() {
			m.Headers.ElementsAs(ctx, &h, false)
		}
		b["headers"] = h // always sent: Terraform owns the full set (the API never returns the values)
		b["body"] = m.Body.ValueString()
		if !m.HTTPTimeout.IsNull() && !m.HTTPTimeout.IsUnknown() {
			b["http_timeout_seconds"] = m.HTTPTimeout.ValueInt64()
		}
	} else {
		b["target"], b["service"] = "service", m.Service.ValueString()
		cmd := []string{}
		if !m.Command.IsNull() && !m.Command.IsUnknown() {
			m.Command.ElementsAs(ctx, &cmd, false)
		}
		b["command"] = cmd
		if !m.TimeoutSecs.IsNull() && !m.TimeoutSecs.IsUnknown() {
			b["timeout_seconds"] = m.TimeoutSecs.ValueInt64()
		}
		if !m.CPUM.IsNull() {
			b["cpu_m"] = m.CPUM.ValueInt64()
		} else if st != nil && !st.CPUM.IsNull() {
			b["cpu_m"] = 0 // back to the service's size
		}
		if !m.MemoryMi.IsNull() {
			b["memory_mi"] = m.MemoryMi.ValueInt64()
		} else if st != nil && !st.MemoryMi.IsNull() {
			b["memory_mi"] = 0
		}
	}
	return b
}

// apply copies the API view into the model; secret / write-only fields (headers, body) stay as configured.
func (m *containerScheduleModel) apply(ctx context.Context, v schedView, imported bool) {
	m.ID, m.Name, m.Target, m.State = types.StringValue(v.ID), types.StringValue(v.Name), types.StringValue(v.Target), types.StringValue(v.State)
	m.Description, m.Timezone = types.StringValue(v.Description), types.StringValue(v.Timezone)
	m.Retries, m.Paused = types.Int64Value(v.Retries), types.BoolValue(v.Paused)
	m.NextRun = types.StringValue("")
	if v.NextRun != nil {
		m.NextRun = types.StringValue(*v.NextRun)
	}
	switch v.Kind {
	case "cron":
		m.Cron, m.RateMinutes, m.At = types.StringValue(v.Cron), types.Int64Null(), types.StringNull()
	case "rate":
		m.Cron, m.RateMinutes, m.At = types.StringNull(), types.Int64Value(v.RateMinutes), types.StringNull()
	case "once":
		if imported || m.At.IsNull() {
			m.At = types.StringValue(v.At)
		} // a local date-time in the config stays as written (the API answers in UTC)
		m.Cron, m.RateMinutes = types.StringNull(), types.Int64Null()
	}
	if v.Target == "http" {
		m.URL, m.Method, m.HTTPTimeout = types.StringValue(v.URL), types.StringValue(v.Method), types.Int64Value(v.HTTPTimeout)
		m.Service, m.Command, m.CPUM, m.MemoryMi = types.StringNull(), types.ListNull(types.StringType), types.Int64Null(), types.Int64Null()
		m.TimeoutSecs = types.Int64Value(0)
		if imported {
			names := []string{}
			for k := range v.Headers {
				names = append(names, k)
			}
			sort.Strings(names)
			if len(names) > 0 { // values cannot be read back
				hm := map[string]string{}
				for _, k := range names {
					hm[k] = ""
				}
				m.Headers, _ = types.MapValueFrom(ctx, types.StringType, hm)
			}
		}
		return
	}
	m.Service, m.TimeoutSecs = types.StringValue(v.Service), types.Int64Value(v.Timeout)
	m.URL, m.Method, m.HTTPTimeout, m.Headers, m.Body = types.StringNull(), types.StringValue(""), types.Int64Value(0), types.MapNull(types.StringType), types.StringNull()
	if len(v.Command) > 0 || (!m.Command.IsNull() && !imported) {
		m.Command, _ = types.ListValueFrom(ctx, types.StringType, v.Command)
	} else {
		m.Command = types.ListNull(types.StringType)
	}
	if v.CPU > 0 {
		m.CPUM = types.Int64Value(v.CPU)
	} else {
		m.CPUM = types.Int64Null()
	}
	if v.Memory > 0 {
		m.MemoryMi = types.Int64Value(v.Memory)
	} else {
		m.MemoryMi = types.Int64Null()
	}
}

func (r *containerScheduleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	b := m.body(ctx, nil)
	b["name"] = m.Name.ValueString()
	var out struct {
		Schedule schedView `json:"schedule"`
	}
	if _, err := r.pd.c.Do(ctx, "POST", schedPath(m.EnvironmentID.ValueInt64(), ""), nil, b, "", &out); err != nil {
		resp.Diagnostics.AddError("Creating the schedule failed", err.Error())
		return
	}
	m.apply(ctx, out.Schedule, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerScheduleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var v schedView
	_, err := r.pd.c.Do(ctx, "GET", schedPath(m.EnvironmentID.ValueInt64(), m.ID.ValueString()), nil, nil, "", &v)
	if client.IsNotFound(err) || (err != nil && (strings.Contains(err.Error(), "schedule_not_found") || strings.Contains(err.Error(), "Zamanlama bulunamadı") || strings.Contains(err.Error(), "Schedule not found"))) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the schedule failed", err.Error())
		return
	}
	m.apply(ctx, v, m.Target.IsNull() || m.Target.ValueString() == "")
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerScheduleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st containerScheduleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Schedule schedView `json:"schedule"`
	}
	if _, err := r.pd.c.Do(ctx, "PATCH", schedPath(st.EnvironmentID.ValueInt64(), st.ID.ValueString()), nil, m.body(ctx, &st), "", &out); err != nil {
		resp.Diagnostics.AddError("Updating the schedule failed", err.Error())
		return
	}
	m.apply(ctx, out.Schedule, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerScheduleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerScheduleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.pd.c.Do(ctx, "DELETE", schedPath(m.EnvironmentID.ValueInt64(), m.ID.ValueString()), nil, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) && !strings.Contains(err.Error(), "schedule_not_found") {
		resp.Diagnostics.AddError("Deleting the schedule failed", err.Error())
	}
}

// ImportState: "<environment_id>/<schedule id or name>".
func (r *containerScheduleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	env, id, ok := strings.Cut(req.ID, "/")
	n, err := strconv.ParseInt(env, 10, 64)
	if !ok || err != nil || id == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <environment_id>/<schedule id or name>, e.g. 17000/nightly-report")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
