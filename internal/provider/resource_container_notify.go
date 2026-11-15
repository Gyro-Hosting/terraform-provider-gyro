package provider

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// gyro_container_notifications (v0.16.0, API 2.17.0): alert channels of a Gyro Containers environment
// (GET/PUT /cloud/containers/{id}/notifications, POST .../notifications/webhook-secret).

var _ resource.ResourceWithImportState = (*containerNotifyResource)(nil)

// containerEvents: the events known when this provider was built (documentation and the destroy fallback only).
// The API is the source of truth: read() takes every event it returns, write() validates against that list and leaves
// "events" out when it is not configured, so events added later (job_failed, log_rule, ...) are never switched off (#3461).
var containerEvents = []string{"down", "crashloop", "replicas_low", "scale_max", "capacity", "egress", "job_failed", "log_rule", "log_quota", "schedule_failed", "schedule_missed"}

type containerNotifyResource struct{ pd *providerData }

func newContainerNotifyResource() resource.Resource { return &containerNotifyResource{} }

type containerNotifyModel struct {
	ID            types.String `tfsdk:"id"`
	ServiceID     types.Int64  `tfsdk:"service_id"`
	Email         types.Bool   `tfsdk:"email"`
	SMS           types.Bool   `tfsdk:"sms"`
	Telegram      types.String `tfsdk:"telegram"`
	WebhookURL    types.String `tfsdk:"webhook_url"`
	WebhookSecret types.String `tfsdk:"webhook_secret"`
	Events        types.Set    `tfsdk:"events"`
	Cooldown      types.Int64  `tfsdk:"cooldown"`
	Resolved      types.Bool   `tfsdk:"resolved"`
}

func (r *containerNotifyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_notifications"
}

func (r *containerNotifyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Alert channels of a Gyro Containers (ECS) environment: e-mail, SMS, Telegram (chat id from the platform bot), signed webhook. " +
			"Events: down, crashloop, replicas_low (per-service threshold: gyro_container_service.notification.min_ready), scale_max, capacity, egress (egress IPs changed), " +
			"job_failed (one-off job), log_rule / log_quota (log alerts), schedule_failed / schedule_missed (scheduled jobs). Without `events` the selection on the platform is kept. " +
			"An event is announced after ~2 minutes, at most once per `cooldown` minutes per service and event, with a resolved message. Destroy = defaults (e-mail on, others off).",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"service_id":  schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id."},
			"email":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Account e-mail."},
			"sms":         schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Account phone (SMS)."},
			"telegram":    schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Telegram chat id (send /start to the platform bot)."},
			"webhook_url": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "https URL of a public server (JSON POST, X-Gyro-Signature)."},
			"webhook_secret": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep,
				Description: "Signing secret: X-Gyro-Signature = sha256=HMAC(secret, X-Gyro-Timestamp + \".\" + body)."},
			"events": schema.SetAttribute{ElementType: types.StringType, Optional: true, Computed: true, PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				Description: "Enabled events (the FULL set; events not listed are turned off). Omit to keep the current selection on the platform (all on by default). " +
					"Known events: down, crashloop, replicas_low, scale_max, capacity, egress, job_failed, log_rule, log_quota, schedule_failed, schedule_missed; " +
					"the list is checked against the events the API offers."},
			"cooldown": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(30), Description: "Minutes between two announcements of the same service + event (5-1440)."},
			"resolved": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Send a resolved message."},
		},
	}
}

func (r *containerNotifyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func ntfPath(sid int64) string { return fmt.Sprintf("cloud/containers/%d/notifications", sid) }

type ntfView struct {
	Settings struct {
		Email      int64            `json:"email"`
		SMS        int64            `json:"sms"`
		Telegram   string           `json:"telegram"`
		WebhookURL string           `json:"webhook_url"`
		Events     map[string]int64 `json:"events"`
		Cooldown   int64            `json:"cooldown"`
		Resolved   int64            `json:"resolved"`
		SecretSet  bool             `json:"webhook_secret_set"`
	} `json:"settings"`
}

func (r *containerNotifyResource) read(ctx context.Context, m *containerNotifyModel) error {
	var v ntfView
	if _, err := r.pd.c.Do(ctx, http.MethodGet, ntfPath(sid(m.ServiceID)), nil, nil, "", &v); err != nil {
		return err
	}
	st := v.Settings
	m.ID = types.StringValue(strconv.FormatInt(sid(m.ServiceID), 10))
	m.Email, m.SMS, m.Resolved = types.BoolValue(st.Email == 1), types.BoolValue(st.SMS == 1), types.BoolValue(st.Resolved == 1)
	m.Telegram, m.WebhookURL, m.Cooldown = types.StringValue(st.Telegram), types.StringValue(st.WebhookURL), types.Int64Value(st.Cooldown)
	ev := []string{}
	for e, on := range st.Events { // every event the API knows, also the ones added after this provider version
		if on == 1 {
			ev = append(ev, e)
		}
	}
	sort.Strings(ev)
	m.Events, _ = types.SetValueFrom(ctx, types.StringType, ev)
	if st.WebhookURL == "" {
		m.WebhookSecret = types.StringValue("")
	} else if m.WebhookSecret.IsNull() || m.WebhookSecret.IsUnknown() || m.WebhookSecret.ValueString() == "" {
		var s struct {
			Secret string `json:"secret"`
		}
		if _, err := r.pd.c.Do(ctx, http.MethodPost, ntfPath(sid(m.ServiceID))+"/webhook-secret", nil, map[string]any{}, "", &s); err == nil {
			m.WebhookSecret = types.StringValue(s.Secret)
		} else {
			m.WebhookSecret = types.StringValue("")
		}
	}
	return nil
}

// write sends the settings. "events" only when configured (the full set), validated against the events the API offers;
// otherwise it is left out and the platform keeps its selection (#3461: a cooldown change turned job_failed etc. off).
func (r *containerNotifyResource) write(ctx context.Context, m *containerNotifyModel, configured bool) error {
	body := map[string]any{"email": m.Email.ValueBool(), "sms": m.SMS.ValueBool(), "telegram": m.Telegram.ValueString(),
		"webhook_url": m.WebhookURL.ValueString(), "cooldown": m.Cooldown.ValueInt64(), "resolved": m.Resolved.ValueBool()}
	if configured && !m.Events.IsNull() && !m.Events.IsUnknown() {
		ev := ctrSet(ctx, m.Events)
		known, err := r.events(ctx, sid(m.ServiceID))
		if err != nil {
			return err
		}
		for _, e := range ev {
			if !known[e] {
				names := make([]string, 0, len(known))
				for k := range known {
					names = append(names, k)
				}
				sort.Strings(names)
				return fmt.Errorf("unknown event %q; the platform offers: %s", e, strings.Join(names, ", "))
			}
		}
		body["events"] = strings.Join(ev, ",")
	}
	_, err := r.pd.c.TypedWrite(ctx, http.MethodPut, ntfPath(sid(m.ServiceID)), body)
	return err
}

// events: event name -> known, as the API returns them (settings.events).
func (r *containerNotifyResource) events(ctx context.Context, service int64) (map[string]bool, error) {
	var v ntfView
	if _, err := r.pd.c.Do(ctx, http.MethodGet, ntfPath(service), nil, nil, "", &v); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for e := range v.Settings.Events {
		out[e] = true
	}
	if len(out) == 0 {
		for _, e := range containerEvents {
			out[e] = true
		}
	}
	return out, nil
}

func (r *containerNotifyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m, cfg containerNotifyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &m, !cfg.Events.IsNull()); err != nil {
		resp.Diagnostics.AddError("Setting container notifications failed", err.Error())
		return
	}
	m.WebhookSecret = types.StringNull()
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container notifications failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerNotifyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerNotifyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container notifications failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerNotifyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st, cfg containerNotifyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &m, !cfg.Events.IsNull()); err != nil {
		resp.Diagnostics.AddError("Setting container notifications failed", err.Error())
		return
	}
	m.WebhookSecret = st.WebhookSecret
	if st.WebhookURL.ValueString() == "" {
		m.WebhookSecret = types.StringNull()
	}
	if err := r.read(ctx, &m); err != nil {
		resp.Diagnostics.AddError("Reading container notifications failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerNotifyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerNotifyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	all := containerEvents // destroy = defaults: every event the platform offers on
	if known, err := r.events(ctx, sid(m.ServiceID)); err == nil {
		all = all[:0:0]
		for e := range known {
			all = append(all, e)
		}
		sort.Strings(all)
	}
	_, _ = r.pd.c.TypedWrite(ctx, http.MethodPut, ntfPath(sid(m.ServiceID)), map[string]any{"email": true, "sms": false, "telegram": "", "webhook_url": "",
		"events": strings.Join(all, ","), "cooldown": 30, "resolved": true})
}

func (r *containerNotifyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", "Use the container environment (service) id, e.g. 123")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
