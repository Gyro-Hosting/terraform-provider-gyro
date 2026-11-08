package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*sshKeyResource)(nil)
	_ resource.ResourceWithImportState = (*sshKeyResource)(nil)
)

type sshKeyResource struct{ pd *providerData }

func newSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	PublicKey   types.String `tfsdk:"public_key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "An account-level SSH public key that can be installed on new gyro_server resources.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "Label, 1-64 characters."},
			"public_key":  schema.StringAttribute{Required: true, PlanModifiers: replace, Description: "OpenSSH public key (ssh-ed25519, ecdsa-sha2-*, ssh-rsa >= 2048 bit)."},
			"fingerprint": schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "SHA256 fingerprint."},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.pd.c.CreateSSHKey(ctx, m.Name.ValueString(), strings.TrimSpace(m.PublicKey.ValueString()))
	if err != nil {
		resp.Diagnostics.AddError("Creating SSH key failed", err.Error())
		return
	}
	m.ID = types.StringValue(strconv.FormatInt(k.ID, 10))
	m.Fingerprint = types.StringValue(k.Fingerprint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid id", err.Error())
		return
	}
	k, err := r.pd.c.SSHKey(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading SSH key failed", err.Error())
		return
	}
	m.Name = types.StringValue(k.Name)
	m.Fingerprint = types.StringValue(k.Fingerprint)
	// The API normalises whitespace; keep the configured spelling when the key material is the same.
	if fields(m.PublicKey.ValueString()) != fields(k.PublicKey) {
		m.PublicKey = types.StringValue(k.PublicKey)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func fields(s string) string {
	f := strings.Fields(s)
	if len(f) >= 2 {
		return f[0] + " " + f[1]
	}
	return strings.TrimSpace(s)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "All gyro_ssh_key arguments force replacement.")
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, _ := strconv.ParseInt(m.ID.ValueString(), 10, 64)
	if err := r.pd.c.DeleteSSHKey(ctx, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting SSH key failed", err.Error())
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
