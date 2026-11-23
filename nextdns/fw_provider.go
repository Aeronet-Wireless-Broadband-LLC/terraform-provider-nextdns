package nextdns

import (
	"context"
	"fmt"
	"os"

	"github.com/Aeronet-Wireless-Broadband-LLC/terraform-provider-nextdns/internal/nextdns"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// frameworkProvider is the NextDNS provider. Its schema and configuration
// behaviour are those of the SDKv2 provider it replaced.
type frameworkProvider struct{}

type frameworkProviderModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

// NewFrameworkProvider returns the Plugin Framework provider.
func NewFrameworkProvider() provider.Provider {
	return &frameworkProvider{}
}

func (p *frameworkProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "nextdns"
}

func (p *frameworkProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "NextDNS API Key. Falls back to the `NEXTDNS_API_KEY` environment variable.",
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the NextDNS API. Defaults to `https://api.nextdns.io/`; falls back to the `NEXTDNS_BASE_URL` environment variable. Only useful for testing against a stub: the API key is sent to this URL, so only `https://api.nextdns.io/` and loopback URLs (`http://127.0.0.1:<port>/`, `http://localhost:<port>/`) are accepted, and anything else is a configuration error.",
			},
		},
	}
}

func (p *frameworkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg frameworkProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := os.Getenv("NEXTDNS_API_KEY")
	if v := cfg.APIKey.ValueString(); v != "" {
		apiKey = v
	}
	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing NextDNS API key",
			"NextDNS API key must be provided in the provider block or NEXTDNS_API_KEY environment variable.",
		)
		return
	}

	opts := []nextdns.ClientOption{nextdns.WithAPIKey(apiKey)}
	baseURL := os.Getenv("NEXTDNS_BASE_URL")
	if v := cfg.BaseURL.ValueString(); v != "" {
		baseURL = v
	}
	if baseURL != "" {
		if _, err := nextdns.ValidateBaseURL(baseURL); err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("base_url"),
				"Invalid NextDNS base URL",
				fmt.Sprintf("base_url (or NEXTDNS_BASE_URL) is not allowed: %s. The API key is sent to this URL, so it must be the NextDNS API or a loopback address for a stub server.", err),
			)
			return
		}
		opts = append(opts, nextdns.WithBaseURL(baseURL))
	}

	client, err := nextdns.New(opts...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create NextDNS client", err.Error())
		return
	}

	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *frameworkProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newAllowlistResource,
		newDenylistResource,
		newParentalControlResource,
		newPrivacyResource,
		newProfileResource,
		newRewriteResource,
		newSecurityResource,
		newSettingsResource,
	}
}

func (p *frameworkProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newSetupEndpointDataSource,
		newSetupLinkedIPDataSource,
	}
}
