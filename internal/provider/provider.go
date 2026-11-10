// Package provider implements the "gyro" Terraform provider on top of the Gyro Public API v1.
package provider

import (
	"context"
	"os"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*gyroProvider)(nil)

type gyroProvider struct {
	version string
}

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &gyroProvider{version: version} }
}

type providerModel struct {
	Endpoint     types.String `tfsdk:"endpoint"`
	Token        types.String `tfsdk:"token"`
	PollInterval types.Int64  `tfsdk:"poll_interval_seconds"`
	PollTimeout  types.Int64  `tfsdk:"poll_timeout_minutes"`
}

// providerData is handed to resources and data sources.
type providerData struct {
	c            *client.Client
	pollInterval time.Duration
	pollTimeout  time.Duration
}

func (p *gyroProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "gyro"
	resp.Version = p.version
}

func (p *gyroProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Gyro (gyrohosting.com) cloud servers, SSH keys, mail, object storage, DNS records and more through the Gyro Public API. Servers are chosen by plan (e.g. standart-2-4) and region (e.g. eu-de-1).",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "API base URL. Defaults to $GYRO_ENDPOINT or " + client.DefaultEndpoint + ".",
			},
			"token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Client API key (wck_...). Defaults to $GYRO_TOKEN. Needs Module:Addons/GyroAPI/cloud_read and, for writes, cloud_write / cloud_order / cloud_delete.",
			},
			"poll_interval_seconds": schema.Int64Attribute{Optional: true, Description: "Seconds between status polls (default 5)."},
			"poll_timeout_minutes":  schema.Int64Attribute{Optional: true, Description: "Maximum minutes to wait for a server to become ready (default 15)."},
		},
	}
}

func (p *gyroProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint := os.Getenv("GYRO_ENDPOINT")
	if !m.Endpoint.IsNull() && !m.Endpoint.IsUnknown() && m.Endpoint.ValueString() != "" {
		endpoint = m.Endpoint.ValueString()
	}
	token := os.Getenv("GYRO_TOKEN")
	if !m.Token.IsNull() && !m.Token.IsUnknown() && m.Token.ValueString() != "" {
		token = m.Token.ValueString()
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing Gyro API key",
			"Set the provider \"token\" argument or the GYRO_TOKEN environment variable to a client API key (wck_...).")
		return
	}
	pd := &providerData{
		c:            client.New(endpoint, token, "terraform-provider-gyro/"+p.version),
		pollInterval: 5 * time.Second,
		pollTimeout:  15 * time.Minute,
	}
	if !m.PollInterval.IsNull() && m.PollInterval.ValueInt64() > 0 {
		pd.pollInterval = time.Duration(m.PollInterval.ValueInt64()) * time.Second
	}
	if !m.PollTimeout.IsNull() && m.PollTimeout.ValueInt64() > 0 {
		pd.pollTimeout = time.Duration(m.PollTimeout.ValueInt64()) * time.Minute
	}
	resp.DataSourceData = pd
	resp.ResourceData = pd
}

func (p *gyroProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{newServerResource, newSSHKeyResource, newWebhookResource, newCustomerResource, newAPIKeyResource, newMailboxResource, newAliasResource, newBucketResource, newStorageKeyResource, newDNSRecordResource, newAppResource, newLoadBalancerResource, newDNSZoneResource, newDNSRRsetResource}
}

func (p *gyroProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newProductsDataSource, newLocationsDataSource, newImagesDataSource, newServicesDataSource}
}
