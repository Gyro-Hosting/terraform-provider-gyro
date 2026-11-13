package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// API 2.22.0: copy an image from another registry into the environment's Gyro registry (no docker push).
// POST cloud/containers/{id}/registry/imports starts a background copy; GET .../registry/imports shows progress.

var _ resource.Resource = (*containerImageResource)(nil)

type containerImageResource struct{ pd *providerData }

func newContainerImageResource() resource.Resource { return &containerImageResource{} }

type containerImageModel struct {
	ID        types.String `tfsdk:"id"`
	ServiceID types.Int64  `tfsdk:"service_id"`
	Source    types.String `tfsdk:"source"`
	Repo      types.String `tfsdk:"repo"`
	Tag       types.String `tfsdk:"tag"`
	Username  types.String `tfsdk:"username"`
	Password  types.String `tfsdk:"password"`
	Overwrite types.Bool   `tfsdk:"overwrite"`
	Image     types.String `tfsdk:"image"`
	Digest    types.String `tfsdk:"digest"`
	Bytes     types.Int64  `tfsdk:"bytes"`
}

type importJob struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Repo       string `json:"repo"`
	Tag        string `json:"tag"`
	State      string `json:"state"`
	Phase      string `json:"phase"`
	BytesTotal int64  `json:"bytes_total"`
	Digest     string `json:"digest"`
	Error      string `json:"error"`
	Message    string `json:"message"`
}

func (r *containerImageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container_image"
}

func (r *containerImageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An image copied from another registry (Docker Hub, GitHub, Quay or any https registry) into a Gyro Containers (ECS) environment's private " +
			"registry, without docker push. Creating it imports the image (linux/amd64 only) and waits until the copy is done; destroying it deletes the tag " +
			"(refused while a service uses it). Use `image` in gyro_container_service. Optional `username`/`password` are sent only for the copy and never stored " +
			"by Gyro (Terraform keeps them in its state as sensitive values; prefer a read-only token). Limits: 1 running import per environment, 10 per hour, 15 minutes.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "<service_id>/<repo>:<tag>"},
			"service_id": schema.Int64Attribute{Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}, Description: "Container environment (service) id."},
			"source": schema.StringAttribute{Required: true, PlanModifiers: replaceS, Validators: []validator.String{stringvalidator.LengthBetween(1, 400)},
				Description: "Source image, e.g. nginx:1.27, docker.io/library/redis:7, ghcr.io/gyro-ornek/uygulama:1.0 or name@sha256:... (https registries only). Changing it imports again."},
			"repo": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
				Description: "Target name under your registry prefix (lowercase, max 3 levels). Default: last part of the source name."},
			"tag": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
				Description: "Target tag. Default: the source tag (latest for a digest)."},
			"username": schema.StringAttribute{Optional: true, Description: "Private source: username (used only for the copy)."},
			"password": schema.StringAttribute{Optional: true, Sensitive: true, Description: "Private source: token / password (used only for the copy, never stored by Gyro)."},
			"overwrite": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Replace an existing name:tag in the registry (otherwise creation fails with image_exists)."},
			"image":  schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Full image reference for gyro_container_service, e.g. registry.ist.gyrohosting.com/ab12cd/nginx:1.27."},
			"digest": schema.StringAttribute{Computed: true, PlanModifiers: keep, Description: "Manifest digest of the copied linux/amd64 image."},
			"bytes":  schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, Description: "Image size in bytes."},
		},
	}
}

func (r *containerImageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

var importPoll = 3 * time.Second

func (r *containerImageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sid := m.ServiceID.ValueInt64()
	body := map[string]any{"source": m.Source.ValueString(), "overwrite": m.Overwrite.ValueBool()}
	if v := m.Repo.ValueString(); !m.Repo.IsUnknown() && v != "" {
		body["repo"] = v
	}
	if v := m.Tag.ValueString(); !m.Tag.IsUnknown() && v != "" {
		body["tag"] = v
	}
	if u := m.Username.ValueString(); u != "" {
		body["username"], body["password"] = u, m.Password.ValueString()
	}
	var start struct {
		Job importJob `json:"job"`
	}
	if _, err := r.pd.c.Do(ctx, "POST", fmt.Sprintf("cloud/containers/%d/registry/imports", sid), nil, body, "", &start); err != nil {
		resp.Diagnostics.AddError("Starting the image import failed", err.Error())
		return
	}
	job := start.Job
	deadline := time.Now().Add(20 * time.Minute)
	for job.State == "running" || job.State == "" {
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Image import timed out", "The import "+job.ID+" did not finish in 20 minutes.")
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Image import cancelled", ctx.Err().Error())
			return
		case <-time.After(importPoll):
		}
		var st struct {
			Jobs []importJob `json:"jobs"`
		}
		if _, err := r.pd.c.Do(ctx, "GET", fmt.Sprintf("cloud/containers/%d/registry/imports", sid), nil, nil, "", &st); err != nil {
			resp.Diagnostics.AddError("Reading the image import failed", err.Error())
			return
		}
		for _, j := range st.Jobs {
			if j.ID == job.ID {
				job = j
			}
		}
	}
	if job.State != "done" {
		resp.Diagnostics.AddError("Image import failed", fmt.Sprintf("%s: %s", job.Error, job.Message))
		return
	}
	_, repo := splitCode(job.Repo)
	m.Repo, m.Tag = types.StringValue(repo), types.StringValue(job.Tag)
	m.ID = types.StringValue(fmt.Sprintf("%d/%s:%s", sid, repo, job.Tag))
	m.Image, m.Digest, m.Bytes = types.StringValue(job.Target), types.StringValue(job.Digest), types.Int64Value(job.BytesTotal)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// splitCode: "ab12cd/nginx" -> ("ab12cd", "nginx")
func splitCode(full string) (string, string) {
	if i := strings.Index(full, "/"); i > 0 {
		return full[:i], full[i+1:]
	}
	return "", full
}

type registryImage struct {
	Repo   string `json:"repo"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

func (r *containerImageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var l struct {
		Images []registryImage `json:"images"`
	}
	_, err := r.pd.c.Do(ctx, "GET", fmt.Sprintf("cloud/containers/%d/registry/images", m.ServiceID.ValueInt64()), nil, nil, "", &l)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the registry failed", err.Error())
		return
	}
	for _, im := range l.Images {
		if _, repo := splitCode(im.Repo); repo == m.Repo.ValueString() && im.Tag == m.Tag.ValueString() {
			m.Digest, m.Bytes = types.StringValue(im.Digest), types.Int64Value(im.Bytes)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // deleted outside Terraform (or by the clean-up policy)
}

func (r *containerImageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// only username/password/overwrite can change in place: they matter for the next import, nothing to call
	var m, st containerImageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &st)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID, m.Image, m.Digest, m.Bytes, m.Repo, m.Tag = st.ID, st.Image, st.Digest, st.Bytes, st.Repo, st.Tag
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *containerImageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m containerImageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	q := url.Values{"repo": {m.Repo.ValueString()}, "tag": {m.Tag.ValueString()}, "confirm": {"true"}}
	_, err := r.pd.c.Do(ctx, "DELETE", fmt.Sprintf("cloud/containers/%d/registry/images", m.ServiceID.ValueInt64()), q, map[string]any{"confirm": true}, "", nil)
	if err != nil && !client.IsNotFound(err) && !strings.Contains(err.Error(), "image_not_found") && !strings.Contains(strings.ToLower(err.Error()), "bulunamad") {
		resp.Diagnostics.AddError("Deleting the image failed", err.Error()+" (an image used by a service cannot be deleted)")
	}
}
