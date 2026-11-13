package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// API 2.23.0 Gyro Pipeline (2.24.0: SSH deploy key / host key, provider "archive"): /cloud/containers/{id}/pipelines (typed tool routes, GyroKapsayici "ecs-pipeline").
// A pipeline connects a git repository to a container environment: push / tag webhook -> Dockerfile build in an isolated
// build environment -> <registry>/<code>/<image>:<sha7> -> rolling deploy of the target services with automatic rollback.

var _ resource.ResourceWithImportState = (*containerPipelineResource)(nil)

type containerPipelineResource struct{ pd *providerData }

func newContainerPipelineResource() resource.Resource { return &containerPipelineResource{} }

type containerPipelineModel struct {
	ID            types.String `tfsdk:"id"`
	ServiceID     types.Int64  `tfsdk:"service_id"`
	PipelineID    types.String `tfsdk:"pipeline_id"`
	Name          types.String `tfsdk:"name"`
	Provider      types.String `tfsdk:"git_provider"`
	Repo          types.String `tfsdk:"repo"`
	Token         types.String `tfsdk:"token"`
	GitUser       types.String `tfsdk:"git_user"`
	Branch        types.String `tfsdk:"branch"`
	Tags          types.String `tfsdk:"tags"`
	Auto          types.Bool   `tfsdk:"auto"`
	RepoFile      types.Bool   `tfsdk:"repo_file"`
	Image         types.String `tfsdk:"image"`
	Dockerfile    types.String `tfsdk:"dockerfile"`
	Context       types.String `tfsdk:"context"`
	Targets       types.List   `tfsdk:"targets"`
	Approval      types.Bool   `tfsdk:"approval"`
	AutoRollback  types.Bool   `tfsdk:"auto_rollback"`
	BuildImage    types.String `tfsdk:"build_image"`
	Commands      types.List   `tfsdk:"commands"`
	BuildEnv      types.Map    `tfsdk:"build_env"`
	BuildArgs     types.Map    `tfsdk:"build_args"`
	Secrets       types.Map    `tfsdk:"secrets"`
	CPU           types.Int64  `tfsdk:"cpu_m"`
	Memory        types.Int64  `tfsdk:"memory_mi"`
	Timeout       types.Int64  `tfsdk:"timeout_min"`
	Verify        types.Int64  `tfsdk:"verify_min"`
	Notify        types.List   `tfsdk:"notify"`
	WebhookURL    types.String `tfsdk:"webhook_url"`
	WebhookSecret types.String `tfsdk:"webhook_secret"`
	RegistryImage types.String `tfsdk:"registry_image"`
	// 2.24.0 (Faz 2): SSH deploy key / host key
	DeployKey    types.Bool   `tfsdk:"deploy_key"`
	DeployKeyPub types.String `tfsdk:"deploy_key_public"`
	DeployKeyFP  types.String `tfsdk:"deploy_key_fingerprint"`
	HostKeyFP    types.String `tfsdk:"ssh_host_fingerprint"`
}

type pipelineSSHView struct {
	Host     string `json:"host"`
	Pinned   bool   `json:"pinned"`
	Trusted  bool   `json:"host_key_trusted"`
	HostKeys []struct {
		Type        string `json:"type"`
		Fingerprint string `json:"fingerprint"`
	} `json:"host_keys"`
	DeployKey *struct {
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
	} `json:"deploy_key"`
}

type pipelineView struct {
	Auth          string            `json:"auth"`
	SSH           *pipelineSSHView  `json:"ssh"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Provider      string            `json:"provider"`
	Repo          string            `json:"repo"`
	GitUser       string            `json:"git_user"`
	Branch        string            `json:"branch"`
	Tags          string            `json:"tags"`
	Auto          bool              `json:"auto"`
	RepoFile      bool              `json:"repo_file"`
	Image         string            `json:"image"`
	Dockerfile    string            `json:"dockerfile"`
	Context       string            `json:"context"`
	Targets       []string          `json:"targets"`
	Approval      bool              `json:"approval"`
	AutoRollback  bool              `json:"auto_rollback"`
	BuildImage    string            `json:"build_image"`
	Commands      []string          `json:"commands"`
	BuildEnv      map[string]string `json:"build_env"`
	BuildArgs     map[string]string `json:"build_args"`
	CPU           int64             `json:"cpu_m"`
	Memory        int64             `json:"memory_mi"`
	Timeout       int64             `json:"timeout_min"`
	Verify        int64             `json:"verify_min"`
	Notify        map[string]bool   `json:"notify"`
	WebhookURL    string            `json:"webhook_url"`
	WebhookSecret string            `json:"webhook_secret"`
	SecretNames   []string          `json:"secret_names"`
	RegistryImage string            `json:"registry_image"`
}

func (r *containerPipelineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_pipeline"
}

func (r *containerPipelineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	strList := func(desc string) schema.ListAttribute {
		return schema.ListAttribute{ElementType: types.StringType, Optional: true, Computed: true, Default: listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})), Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Gyro Pipeline (CodePipeline-like CI/CD) of a Gyro Containers (ECS) environment: connects a GitHub, GitLab, Gitea or any https git repository. " +
			"A push or tag (webhook) builds the repository's Dockerfile in an isolated build environment, pushes `<registry>/<code>/<image>:<sha7>` (+ `latest`) to the " +
			"environment's private registry and rolls the `targets` services out to it with a health watch and automatic rollback. Add `webhook_url` + `webhook_secret` " +
			"to the repository's webhook settings (GitHub: application/json, push event). `token` and `secrets` values are write-only (never read back from Gyro).",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<pipeline_id>"},
			"service_id":  schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}, Description: "Container environment (service) id."},
			"pipeline_id": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Pipeline id (p + 8 characters)."},
			"name":        schema.StringAttribute{Required: true, Description: "Display name (1-48)."},
			"git_provider": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("github"),
				Description: "github, gitlab, gitea, git (any git server; gyro-pipeline.yml is not read for git) or archive (no repository: builds only from uploaded archives, API POST .../uploads)."},
			"repo": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				Description: "https clone URL without credentials (https://github.com/gyro-ornek/uygulama) or SSH address (git@github.com:gyro-ornek/uygulama.git, ssh://git@git.example.com:2222/team/app.git; needs deploy_key = true). Empty for git_provider = archive."},
			"token":    schema.StringAttribute{Optional: true, Sensitive: true, Description: "Read-only access token for a private repository (stored encrypted by Gyro, never returned)."},
			"git_user": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "User name for the token (default per provider)."},
			"branch":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("main"), Description: "Branches whose pushes build (comma separated globs, empty = none)."},
			"tags":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Tags that build (globs, e.g. v*; empty = none)."},
			"auto":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Webhook pushes start runs."},
			"repo_file": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				Description: "Read gyro-pipeline.yml from the repository (it overrides these settings for a run)."},
			"image":         schema.StringAttribute{Required: true, Description: "Image name in the environment registry (lowercase)."},
			"dockerfile":    schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("Dockerfile"), Description: "Dockerfile path from the repository root."},
			"context":       schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Build folder (empty = repository root)."},
			"targets":       strList("Services that get the new image (empty = build only)."),
			"approval":      schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Wait for approval before deploying."},
			"auto_rollback": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Roll back when the new version is unhealthy."},
			"build_image":   schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Image that runs `commands` (e.g. node:22-alpine)."},
			"commands":      strList("Shell commands run in the repository root before the Dockerfile (tests, npm ci ...)."),
			"build_env":     schema.MapAttribute{ElementType: types.StringType, Optional: true, Description: "Variables for `commands`."},
			"build_args":    schema.MapAttribute{ElementType: types.StringType, Optional: true, Description: "Dockerfile ARG values (visible in the image history)."},
			"secrets":       schema.MapAttribute{ElementType: types.StringType, Optional: true, Sensitive: true, Description: "Build secrets for `commands` (write-only, masked in logs). Removing a key deletes the secret."},
			"cpu_m":         schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Build CPU in millicores (plan limit)."},
			"memory_mi":     schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Build memory in MiB (plan limit)."},
			"timeout_min":   schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Build time limit in minutes (plan limit)."},
			"verify_min":    schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Health wait after deploy (1-30 minutes)."},
			"notify": schema.ListAttribute{ElementType: types.StringType, Optional: true, Computed: true,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("approval"), types.StringValue("failed"), types.StringValue("rolled_back")})),
				Description: "Notifications: failed, succeeded, approval, rolled_back (channels = the environment's alert settings). Sorted."},
			"webhook_url":    schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Webhook address for the repository."},
			"webhook_secret": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep, Description: "Webhook secret (GitHub Secret / GitLab Secret token / Gitea secret)."},
			"registry_image": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Image repository the pipeline pushes to (<registry>/<code>/<image>)."},
			"deploy_key": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "SSH repositories: Gyro creates an ed25519 deploy key (the private key never leaves Gyro). Add deploy_key_public to the repository as a read-only deploy key. Setting it back to false deletes the key."},
			"deploy_key_public":      schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Public key (authorized_keys line) to add to the repository."},
			"deploy_key_fingerprint": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "SHA256 fingerprint of the deploy key."},
			"ssh_host_fingerprint": schema.StringAttribute{Optional: true,
				Description: "SSH servers other than GitHub / GitLab / Bitbucket (whose keys are pinned): the server's SHA256 host key fingerprint you verified (trust on first use). Builds stop if the server key changes."},
		},
	}
}

func (r *containerPipelineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
		return
	}
	r.pd = pd
}

func plStrings(ctx context.Context, l types.List) []string {
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out
	}
	_ = l.ElementsAs(ctx, &out, false)
	return out
}

func plMap(ctx context.Context, m types.Map) map[string]string {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out
	}
	_ = m.ElementsAs(ctx, &out, false)
	return out
}

func (m *containerPipelineModel) body(ctx context.Context) map[string]any {
	b := map[string]any{"name": m.Name.ValueString(), "provider": m.Provider.ValueString(), "repo": m.Repo.ValueString(), "git_user": m.GitUser.ValueString(),
		"branch": m.Branch.ValueString(), "tags": m.Tags.ValueString(), "auto": m.Auto.ValueBool(), "repo_file": m.RepoFile.ValueBool(), "image": m.Image.ValueString(),
		"dockerfile": m.Dockerfile.ValueString(), "context": m.Context.ValueString(), "targets": plStrings(ctx, m.Targets), "approval": m.Approval.ValueBool(),
		"auto_rollback": m.AutoRollback.ValueBool(), "build_image": m.BuildImage.ValueString(), "commands": plStrings(ctx, m.Commands),
		"build_env": plMap(ctx, m.BuildEnv), "build_args": plMap(ctx, m.BuildArgs), "notify": plStrings(ctx, m.Notify)}
	for k, v := range map[string]types.Int64{"cpu_m": m.CPU, "memory_mi": m.Memory, "timeout_min": m.Timeout, "verify_min": m.Verify} {
		if !v.IsNull() && !v.IsUnknown() {
			b[k] = v.ValueInt64()
		}
	}
	return b
}

func (r *containerPipelineResource) apply(ctx context.Context, m *containerPipelineModel, v pipelineView, readSecrets bool) {
	m.PipelineID = types.StringValue(v.ID)
	m.ID = types.StringValue(fmt.Sprintf("%d/%s", m.ServiceID.ValueInt64(), v.ID))
	m.Name, m.Provider, m.Repo, m.GitUser = types.StringValue(v.Name), types.StringValue(v.Provider), types.StringValue(v.Repo), types.StringValue(v.GitUser)
	m.Branch, m.Tags, m.Auto, m.RepoFile = types.StringValue(v.Branch), types.StringValue(v.Tags), types.BoolValue(v.Auto), types.BoolValue(v.RepoFile)
	m.Image, m.Dockerfile, m.Context = types.StringValue(v.Image), types.StringValue(v.Dockerfile), types.StringValue(v.Context)
	m.Approval, m.AutoRollback, m.BuildImage = types.BoolValue(v.Approval), types.BoolValue(v.AutoRollback), types.StringValue(v.BuildImage)
	lv := func(xs []string) types.List {
		vals := []attr.Value{}
		for _, x := range xs {
			vals = append(vals, types.StringValue(x))
		}
		return types.ListValueMust(types.StringType, vals)
	}
	m.Targets, m.Commands = lv(v.Targets), lv(v.Commands)
	mv := func(cur types.Map, src map[string]string) types.Map {
		if len(src) == 0 && cur.IsNull() {
			return cur
		}
		vals := map[string]attr.Value{}
		for k, x := range src {
			vals[k] = types.StringValue(x)
		}
		return types.MapValueMust(types.StringType, vals)
	}
	m.BuildEnv, m.BuildArgs = mv(m.BuildEnv, v.BuildEnv), mv(m.BuildArgs, v.BuildArgs)
	m.CPU, m.Memory, m.Timeout, m.Verify = types.Int64Value(v.CPU), types.Int64Value(v.Memory), types.Int64Value(v.Timeout), types.Int64Value(v.Verify)
	n := []string{}
	for k, on := range v.Notify {
		if on {
			n = append(n, k)
		}
	}
	sort.Strings(n)
	m.Notify = lv(n)
	m.WebhookURL, m.RegistryImage = types.StringValue(v.WebhookURL), types.StringValue(v.RegistryImage)
	m.DeployKeyPub, m.DeployKeyFP = types.StringValue(""), types.StringValue("")
	if v.SSH != nil && v.SSH.DeployKey != nil {
		m.DeployKeyPub, m.DeployKeyFP = types.StringValue(v.SSH.DeployKey.PublicKey), types.StringValue(v.SSH.DeployKey.Fingerprint)
	}
	if m.DeployKey.IsNull() || m.DeployKey.IsUnknown() || readSecrets {
		m.DeployKey = types.BoolValue(v.SSH != nil && v.SSH.DeployKey != nil)
	}
	if readSecrets && !m.HostKeyFP.IsNull() && v.SSH != nil && !v.SSH.Pinned {
		fp := ""
		if len(v.SSH.HostKeys) > 0 {
			fp = v.SSH.HostKeys[0].Fingerprint
		}
		m.HostKeyFP = types.StringValue(fp)
	}
	if v.WebhookSecret != "" {
		m.WebhookSecret = types.StringValue(v.WebhookSecret)
	} else if m.WebhookSecret.IsUnknown() {
		m.WebhookSecret = types.StringValue("")
	}
	// secrets: keep the configured values for names Gyro still has (values are write-only)
	if readSecrets && !m.Secrets.IsNull() && !m.Secrets.IsUnknown() {
		cfg := plMap(ctx, m.Secrets)
		have := map[string]bool{}
		for _, s := range v.SecretNames {
			have[s] = true
		}
		vals := map[string]attr.Value{}
		for k, x := range cfg {
			if have[k] {
				vals[k] = types.StringValue(x)
			}
		}
		m.Secrets = types.MapValueMust(types.StringType, vals)
	}
}

func (r *containerPipelineResource) syncSecrets(ctx context.Context, sid int64, pid string, want, had map[string]string) error {
	set := map[string]string{}
	for k, v := range want {
		if had[k] != v {
			set[k] = v
		}
	}
	if len(set) > 0 {
		if _, err := r.pd.c.Do(ctx, "PUT", fmt.Sprintf("cloud/containers/%d/pipelines/%s/secrets", sid, pid), nil, map[string]any{"set": set}, "", nil); err != nil {
			return err
		}
	}
	for k := range had {
		if _, ok := want[k]; !ok {
			if _, err := r.pd.c.Do(ctx, "DELETE", fmt.Sprintf("cloud/containers/%d/pipelines/%s/secrets/%s", sid, pid, k), nil, map[string]any{"confirm": true}, "", nil); err != nil && !client.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

// sshSync: deploy key and host key confirmation (API 2.24.0), then the fresh view.
func (r *containerPipelineResource) sshSync(ctx context.Context, sid int64, pid string, m *containerPipelineModel, v pipelineView) (pipelineView, error) {
	base := fmt.Sprintf("cloud/containers/%d/pipelines/%s", sid, pid)
	has := v.SSH != nil && v.SSH.DeployKey != nil
	changed := false
	switch want := m.DeployKey.ValueBool(); {
	case want && !has:
		if v.SSH == nil {
			return v, fmt.Errorf("deploy_key needs an SSH repository address (git@host:org/repo.git)")
		}
		if _, err := r.pd.c.Do(ctx, "PUT", base+"/deploy-key", nil, map[string]any{}, "", nil); err != nil {
			return v, err
		}
		changed = true
	case !want && has:
		if _, err := r.pd.c.Do(ctx, "DELETE", base+"/deploy-key", nil, map[string]any{"confirm": true}, "", nil); err != nil && !client.IsNotFound(err) {
			return v, err
		}
		changed = true
	}
	if fp := m.HostKeyFP.ValueString(); fp != "" && v.SSH != nil && !v.SSH.Pinned {
		cur := ""
		if len(v.SSH.HostKeys) > 0 {
			cur = v.SSH.HostKeys[0].Fingerprint
		}
		if cur != fp {
			if _, err := r.pd.c.Do(ctx, "PUT", base+"/host-key", nil, map[string]any{"fingerprint": fp}, "", nil); err != nil {
				return v, err
			}
			changed = true
		}
	}
	if !changed {
		return v, nil
	}
	var nv pipelineView
	if _, err := r.pd.c.Do(ctx, "GET", base, nil, nil, "", &nv); err != nil {
		return v, err
	}
	nv.WebhookSecret = v.WebhookSecret
	return nv, nil
}

func (r *containerPipelineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerPipelineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid := m.ServiceID.ValueInt64()
	b := m.body(ctx)
	if t := m.Token.ValueString(); t != "" {
		b["token"] = t
	}
	var out struct {
		Pipeline pipelineView `json:"pipeline"`
	}
	if _, err := r.pd.c.Do(ctx, "POST", fmt.Sprintf("cloud/containers/%d/pipelines", sid), nil, b, "", &out); err != nil {
		resp.Diagnostics.AddError("Creating the pipeline failed", err.Error())
		return
	}
	if err := r.syncSecrets(ctx, sid, out.Pipeline.ID, plMap(ctx, m.Secrets), nil); err != nil {
		resp.Diagnostics.AddError("Setting the build secrets failed", err.Error())
	}
	if nv, err := r.sshSync(ctx, sid, out.Pipeline.ID, &m, out.Pipeline); err != nil {
		resp.Diagnostics.AddError("SSH deploy key / host key failed", err.Error())
	} else {
		out.Pipeline = nv
	}
	r.apply(ctx, &m, out.Pipeline, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerPipelineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var v pipelineView
	_, err := r.pd.c.Do(ctx, "GET", fmt.Sprintf("cloud/containers/%d/pipelines/%s", m.ServiceID.ValueInt64(), m.PipelineID.ValueString()), nil, nil, "", &v)
	if client.IsNotFound(err) || (err != nil && strings.Contains(err.Error(), "pipeline_not_found")) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the pipeline failed", err.Error())
		return
	}
	if m.WebhookSecret.ValueString() == "" { // imported: fetch the secret once
		var h pipelineView
		if _, err := r.pd.c.Do(ctx, "GET", fmt.Sprintf("cloud/containers/%d/pipelines/%s/webhook", m.ServiceID.ValueInt64(), v.ID), nil, nil, "", &h); err == nil {
			v.WebhookSecret = h.WebhookSecret
		}
	}
	r.apply(ctx, &m, v, true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerPipelineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, st containerPipelineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid, pid := st.ServiceID.ValueInt64(), st.PipelineID.ValueString()
	b := m.body(ctx)
	if m.Token.ValueString() != st.Token.ValueString() {
		if t := m.Token.ValueString(); t != "" {
			b["token"] = t
		} else {
			b["token"] = "-"
		}
	}
	var out struct {
		Pipeline pipelineView `json:"pipeline"`
	}
	if _, err := r.pd.c.Do(ctx, "PATCH", fmt.Sprintf("cloud/containers/%d/pipelines/%s", sid, pid), nil, b, "", &out); err != nil {
		resp.Diagnostics.AddError("Updating the pipeline failed", err.Error())
		return
	}
	if err := r.syncSecrets(ctx, sid, pid, plMap(ctx, m.Secrets), plMap(ctx, st.Secrets)); err != nil {
		resp.Diagnostics.AddError("Updating the build secrets failed", err.Error())
	}
	m.WebhookSecret = st.WebhookSecret
	if nv, err := r.sshSync(ctx, sid, pid, &m, out.Pipeline); err != nil {
		resp.Diagnostics.AddError("SSH deploy key / host key failed", err.Error())
	} else {
		out.Pipeline = nv
	}
	r.apply(ctx, &m, out.Pipeline, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerPipelineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerPipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.pd.c.Do(ctx, "DELETE", fmt.Sprintf("cloud/containers/%d/pipelines/%s", m.ServiceID.ValueInt64(), m.PipelineID.ValueString()), nil, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) && !strings.Contains(err.Error(), "pipeline_not_found") {
		resp.Diagnostics.AddError("Deleting the pipeline failed", err.Error())
	}
}

func (r *containerPipelineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	p := strings.SplitN(req.ID, "/", 2)
	n, err := strconv.ParseInt(p[0], 10, 64)
	if len(p) != 2 || err != nil || !strings.HasPrefix(p[1], "p") {
		resp.Diagnostics.AddError("Invalid import id", "Use <service_id>/<pipeline_id>, e.g. 123/p1a2b3c4d")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_id"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("pipeline_id"), p[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("webhook_secret"), "")...)
}
