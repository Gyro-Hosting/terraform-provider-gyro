package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Gyro Containers (Istanbul, tr-ist-1). API: /cloud/containers/{service}/services/{name}... (typed tool routes, GyroKapsayici).

var _ resource.ResourceWithImportState = (*containerServiceResource)(nil)

type containerSpec struct {
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	Port       int64             `json:"port"`
	Replicas   int64             `json:"replicas"`
	CPU        int64             `json:"cpu_m"`
	Memory     int64             `json:"memory_mi"`
	Command    []string          `json:"command"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	HealthPath string            `json:"health_path"`
	Public     bool              `json:"public"`
	RunAsUser  int64             `json:"run_as_user"`
	// v0.16.0 (API 2.17.0)
	Autoscaling *struct {
		Enabled       bool  `json:"enabled"`
		Min           int64 `json:"min"`
		Max           int64 `json:"max"`
		CPUPercent    int64 `json:"cpu_percent"`
		MemoryPercent int64 `json:"memory_percent"`
	} `json:"autoscaling"`
	Alerts *struct {
		MinReady int64 `json:"min_ready"`
	} `json:"alerts"`
}

// containerAutoscaling: present = autoscaling on (replicas then follow the load between min and max).
type containerAutoscaling struct {
	Min           types.Int64 `tfsdk:"min"`
	Max           types.Int64 `tfsdk:"max"`
	CPUPercent    types.Int64 `tfsdk:"cpu_percent"`
	MemoryPercent types.Int64 `tfsdk:"memory_percent"`
}

type containerNotification struct {
	MinReady types.Int64 `tfsdk:"min_ready"`
}

type containerDomain struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type containerView struct {
	Name       string            `json:"name"`
	Spec       containerSpec     `json:"spec"`
	Revision   int64             `json:"revision"`
	State      string            `json:"state"`
	URL        string            `json:"url"`
	Host       string            `json:"host"`
	Domains    []containerDomain `json:"domains"`
	SecretKeys []string          `json:"secret_keys"`
}

func containerPath(sid int64, name string) string {
	p := fmt.Sprintf("cloud/containers/%d/services", sid)
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	return p
}

type containerServiceResource struct{ pd *providerData }

func newContainerServiceResource() resource.Resource { return &containerServiceResource{} }

type containerServiceModel struct {
	ID           types.String           `tfsdk:"id"`
	ServiceID    types.Int64            `tfsdk:"service_id"`
	Name         types.String           `tfsdk:"name"`
	Image        types.String           `tfsdk:"image"`
	Port         types.Int64            `tfsdk:"port"`
	Replicas     types.Int64            `tfsdk:"replicas"`
	CPU          types.Int64            `tfsdk:"cpu_m"`
	Memory       types.Int64            `tfsdk:"memory_mi"`
	Public       types.Bool             `tfsdk:"public"`
	HealthPath   types.String           `tfsdk:"health_path"`
	Command      types.List             `tfsdk:"command"`
	Args         types.List             `tfsdk:"args"`
	Env          types.Map              `tfsdk:"env"`
	Secrets      types.Map              `tfsdk:"secrets"`
	Domains      types.Set              `tfsdk:"domains"`
	RunAsUser    types.Int64            `tfsdk:"run_as_user"`
	Autoscaling  *containerAutoscaling  `tfsdk:"autoscaling"`
	Notification *containerNotification `tfsdk:"notification"`
	URL          types.String           `tfsdk:"url"`
	Host         types.String           `tfsdk:"host"`
	Revision     types.Int64            `tfsdk:"revision"`
	State        types.String           `tfsdk:"state"`
}

func (r *containerServiceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_service"
}

func (r *containerServiceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A service in a Gyro Containers (ECS) environment (Istanbul, tr-ist-1): a Docker image running on the node pool dedicated to you. " +
			"Every change except `replicas` creates a new revision that rolls out without downtime. Secret values are stored encrypted and never read back " +
			"(Terraform keeps what you configured). Domains activate once their DNS points to the service `host` (CNAME).",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<name>"},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: replaceI, Description: "Container environment (service) id."},
			"name":       schema.StringAttribute{Required: true, PlanModifiers: replaceS, Description: "Service name: lowercase letters, digits and '-', max 40."},
			"image":      schema.StringAttribute{Required: true, Description: "Image reference, e.g. ghcr.io/acme/web:1.4."},
			"port":       schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Container port (0 = no network port). Use >= 1024 (non-root)."},
			"replicas":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Description: "Number of instances (0 = stopped)."},
			"cpu_m":      schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(250), Description: "CPU per instance in millicores."},
			"memory_mi":  schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(256), Description: "Memory per instance in MiB."},
			"public":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Public HTTPS address <name>-<code>.gyroapp.net."},
			"health_path": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				Description: "HTTP health check path (empty = TCP check)."},
			"command":     schema.ListAttribute{ElementType: types.StringType, Optional: true, Description: "Override the image entrypoint."},
			"args":        schema.ListAttribute{ElementType: types.StringType, Optional: true, Description: "Arguments for the command."},
			"env":         schema.MapAttribute{ElementType: types.StringType, Optional: true, Description: "Plain environment variables. Names: letters, digits and _, not starting with a digit (invalid names fail at plan time). A value must not start with an invisible copy-paste character (U+200B, U+FEFF)."},
			"secrets":     schema.MapAttribute{ElementType: types.StringType, Optional: true, Sensitive: true, Description: "Secret environment variables (write only; a key removed outside Terraform shows as a diff)."},
			"domains":     schema.SetAttribute{ElementType: types.StringType, Optional: true, Description: "Your domains (CNAME to `host`); requires public = true."},
			"run_as_user": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "User id in the container (0 = platform default 10001)."},
			"autoscaling": schema.SingleNestedAttribute{Optional: true,
				Description: "Autoscaling: when set, the replica count follows the average CPU (and optionally memory) use of the reserved size between min and max " +
					"(up fast, down one per minute after 3 calm minutes); `replicas` is then ignored. Max cannot exceed the plan replica limit. Not a revision.",
				Attributes: map[string]schema.Attribute{
					"min":            schema.Int64Attribute{Required: true, Description: "Minimum replicas (>= 1)."},
					"max":            schema.Int64Attribute{Required: true, Description: "Maximum replicas (<= plan limit)."},
					"cpu_percent":    schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(70), Description: "Target average CPU use in % of cpu_m (10-95)."},
					"memory_percent": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Target average memory use in % of memory_mi (0 = off, else 10-95)."},
				}},
			"notification": schema.SingleNestedAttribute{Optional: true,
				Description: "Per-service alert threshold; channels are set with gyro_container_notifications.",
				Attributes: map[string]schema.Attribute{
					"min_ready": schema.Int64Attribute{Required: true, Description: "Alert when fewer replicas are ready (0 = off)."},
				}},
			"url":      schema.StringAttribute{Computed: true, Description: "Public URL (public services)."},
			"host":     schema.StringAttribute{Computed: true, Description: "Public host name; CNAME target for your domains."},
			"revision": schema.Int64Attribute{Computed: true, Description: "Current revision number."},
			"state":    schema.StringAttribute{Computed: true, Description: "running, deploying, failing, pending, stopped or suspended (at the last read)."},
		},
	}
}

func (r *containerServiceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = pdOf(req, resp)
}

func ctrList(ctx context.Context, l types.List) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	l.ElementsAs(ctx, &out, false)
	return out
}

func ctrMap(ctx context.Context, m types.Map) map[string]string {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out
	}
	m.ElementsAs(ctx, &out, false)
	return out
}

func ctrSet(ctx context.Context, s types.Set) []string {
	out := []string{}
	if s.IsNull() || s.IsUnknown() {
		return out
	}
	s.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out
}

func (m *containerServiceModel) spec(ctx context.Context) map[string]any {
	return map[string]any{"image": m.Image.ValueString(), "port": m.Port.ValueInt64(), "replicas": m.Replicas.ValueInt64(),
		"cpu_m": m.CPU.ValueInt64(), "memory_mi": m.Memory.ValueInt64(), "public": m.Public.ValueBool(), "health_path": m.HealthPath.ValueString(),
		"command": ctrList(ctx, m.Command), "args": ctrList(ctx, m.Args), "env": ctrMap(ctx, m.Env), "run_as_user": m.RunAsUser.ValueInt64(),
		"autoscaling": m.autoscaling(), "alerts": map[string]any{"min_ready": m.minReady()}}
}

func (m *containerServiceModel) autoscaling() map[string]any {
	if m.Autoscaling == nil {
		return map[string]any{"enabled": false}
	}
	a := m.Autoscaling
	return map[string]any{"enabled": true, "min": a.Min.ValueInt64(), "max": a.Max.ValueInt64(), "cpu_percent": a.CPUPercent.ValueInt64(), "memory_percent": a.MemoryPercent.ValueInt64()}
}

func (m *containerServiceModel) minReady() int64 {
	if m.Notification == nil {
		return 0
	}
	return m.Notification.MinReady.ValueInt64()
}

func (r *containerServiceResource) get(ctx context.Context, sid int64, name string) (*containerView, error) {
	var v containerView
	_, err := r.pd.c.Do(ctx, http.MethodGet, containerPath(sid, name), nil, nil, "", &v)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// apply copies the remote view into the model. Optional collections stay null when empty remotely and unset locally.
func (r *containerServiceResource) apply(ctx context.Context, m *containerServiceModel, v *containerView) {
	sp := v.Spec
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), v.Name))
	m.Name = types.StringValue(v.Name)
	m.Image = types.StringValue(sp.Image)
	m.Port, m.CPU, m.Memory = types.Int64Value(sp.Port), types.Int64Value(sp.CPU), types.Int64Value(sp.Memory)
	if sp.Autoscaling != nil && sp.Autoscaling.Enabled {
		// the autoscaler owns the replica count: keep the configured value (no drift)
		if m.Replicas.IsNull() || m.Replicas.IsUnknown() {
			m.Replicas = types.Int64Value(1)
		}
		mp := sp.Autoscaling.MemoryPercent
		m.Autoscaling = &containerAutoscaling{Min: types.Int64Value(sp.Autoscaling.Min), Max: types.Int64Value(sp.Autoscaling.Max),
			CPUPercent: types.Int64Value(sp.Autoscaling.CPUPercent), MemoryPercent: types.Int64Value(mp)}
	} else {
		m.Replicas = types.Int64Value(sp.Replicas)
		m.Autoscaling = nil
	}
	if sp.Alerts != nil && sp.Alerts.MinReady > 0 {
		m.Notification = &containerNotification{MinReady: types.Int64Value(sp.Alerts.MinReady)}
	} else if m.Notification != nil {
		m.Notification = &containerNotification{MinReady: types.Int64Value(0)}
	}
	m.Public, m.HealthPath, m.RunAsUser = types.BoolValue(sp.Public), types.StringValue(sp.HealthPath), types.Int64Value(sp.RunAsUser)
	if len(sp.Command) > 0 || !m.Command.IsNull() {
		m.Command, _ = types.ListValueFrom(ctx, types.StringType, nonNil(sp.Command))
	}
	if len(sp.Args) > 0 || !m.Args.IsNull() {
		m.Args, _ = types.ListValueFrom(ctx, types.StringType, nonNil(sp.Args))
	}
	if len(sp.Env) > 0 || !m.Env.IsNull() {
		env := sp.Env
		if env == nil {
			env = map[string]string{}
		}
		m.Env, _ = types.MapValueFrom(ctx, types.StringType, env)
	}
	// secrets: keep configured values for keys that still exist remotely
	if !m.Secrets.IsNull() {
		have := map[string]bool{}
		for _, k := range v.SecretKeys {
			have[k] = true
		}
		kept := map[string]string{}
		for k, val := range ctrMap(ctx, m.Secrets) {
			if have[k] {
				kept[k] = val
			}
		}
		m.Secrets, _ = types.MapValueFrom(ctx, types.StringType, kept)
	}
	ds := []string{}
	for _, d := range v.Domains {
		ds = append(ds, d.Name)
	}
	if len(ds) > 0 || !m.Domains.IsNull() {
		sort.Strings(ds)
		m.Domains, _ = types.SetValueFrom(ctx, types.StringType, ds)
	}
	m.URL, m.Host = types.StringValue(v.URL), types.StringValue(v.Host)
	m.Revision, m.State = types.Int64Value(v.Revision), types.StringValue(v.State)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *containerServiceResource) syncSecrets(ctx context.Context, m *containerServiceModel, old map[string]string) error {
	want := ctrMap(ctx, m.Secrets)
	set := map[string]any{}
	for k, v := range want {
		if ov, ok := old[k]; !ok || ov != v {
			set[k] = v
		}
	}
	del := []string{}
	for k := range old {
		if _, ok := want[k]; !ok {
			del = append(del, k)
		}
	}
	p := containerPath(sid(m.ServiceID), m.Name.ValueString()) + "/secrets"
	if len(set) > 0 {
		if _, err := r.pd.c.TypedWrite(ctx, http.MethodPut, p, map[string]any{"set": set}); err != nil {
			return err
		}
	}
	if len(del) > 0 {
		sort.Strings(del)
		q := url.Values{"keys": {strings.Join(del, ",")}}
		if _, err := r.pd.c.Do(ctx, http.MethodDelete, p, q, map[string]any{"confirm": true}, "", nil); err != nil && !client.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *containerServiceResource) syncDomains(ctx context.Context, m *containerServiceModel, old []string) error {
	want := ctrSet(ctx, m.Domains)
	in := func(l []string, x string) bool {
		for _, y := range l {
			if y == x {
				return true
			}
		}
		return false
	}
	base := containerPath(sid(m.ServiceID), m.Name.ValueString()) + "/domains"
	for _, d := range old {
		if !in(want, d) {
			if _, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, base+"/"+url.PathEscape(d), map[string]any{"confirm": true}); err != nil && !client.IsNotFound(err) {
				return err
			}
		}
	}
	for _, d := range want {
		if !in(old, d) {
			if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, base, map[string]any{"domain": d}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *containerServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := m.spec(ctx)
	body["name"] = m.Name.ValueString()
	if _, err := r.pd.c.TypedWrite(ctx, http.MethodPost, containerPath(sid(m.ServiceID), ""), body); err != nil {
		resp.Diagnostics.AddError("Creating container service failed", err.Error())
		return
	}
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", sid(m.ServiceID), m.Name.ValueString()))
	if err := r.syncSecrets(ctx, &m, map[string]string{}); err != nil {
		resp.Diagnostics.AddError("Setting secrets failed", err.Error())
	}
	if err := r.syncDomains(ctx, &m, nil); err != nil {
		resp.Diagnostics.AddError("Adding domains failed", err.Error())
	}
	v, err := r.get(ctx, sid(m.ServiceID), m.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading container service failed", err.Error())
		return
	}
	r.apply(ctx, &m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.get(ctx, sid(m.ServiceID), m.Name.ValueString())
	if client.IsNotFound(err) || (err != nil && !r.exists(ctx, sid(m.ServiceID), m.Name.ValueString())) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading container service failed", err.Error())
		return
	}
	r.apply(ctx, &m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// exists: a missing service answers 422 (tool error) on the get route; the list is authoritative.
// Errors listing are treated as "exists" so a transient failure never drops state.
func (r *containerServiceResource) exists(ctx context.Context, sid int64, name string) bool {
	var l struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	if _, err := r.pd.c.Do(ctx, http.MethodGet, containerPath(sid, ""), nil, nil, "", &l); err != nil {
		return !client.IsNotFound(err)
	}
	for _, s := range l.Services {
		if s.Name == name {
			return true
		}
	}
	return false
}

func (r *containerServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st containerServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	newSpec, oldSpec := m.spec(ctx), st.spec(ctx)
	if fmt.Sprint(newSpec) != fmt.Sprint(oldSpec) {
		if _, err := r.pd.c.TypedWrite(ctx, http.MethodPatch, containerPath(sid(m.ServiceID), m.Name.ValueString()), map[string]any{"spec": newSpec, "reason": "terraform"}); err != nil {
			resp.Diagnostics.AddError("Updating container service failed", err.Error())
			return
		}
	}
	if err := r.syncSecrets(ctx, &m, ctrMap(ctx, st.Secrets)); err != nil {
		resp.Diagnostics.AddError("Updating secrets failed", err.Error())
		return
	}
	if err := r.syncDomains(ctx, &m, ctrSet(ctx, st.Domains)); err != nil {
		resp.Diagnostics.AddError("Updating domains failed", err.Error())
		return
	}
	v, err := r.get(ctx, sid(m.ServiceID), m.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading container service failed", err.Error())
		return
	}
	r.apply(ctx, &m, v)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	_, err := r.pd.c.TypedWrite(ctx, http.MethodDelete, containerPath(sid(m.ServiceID), m.Name.ValueString()), map[string]any{"confirm": true})
	if err != nil && !client.IsNotFound(err) && r.exists(ctx, sid(m.ServiceID), m.Name.ValueString()) {
		resp.Diagnostics.AddError("Deleting container service failed", err.Error())
	}
}

// Import ID: <service_id>/<name>
func (r *containerServiceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || p[1] == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<name>, e.g. 123/web")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), strings.ToLower(p[1]))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
