// editorconfig-checker-disable-file: HCL in raw strings is space-indented.
package nextdns

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestMain points terraform-plugin-testing at OpenTofu when no binary has
// been chosen explicitly, so the suite runs against the engine the
// configuration repositories actually use. No test downloads a released
// provider: every test runs the provider under test against a stub API.
func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC_TERRAFORM_PATH") == "" {
		if p, err := exec.LookPath("tofu"); err == nil {
			_ = os.Setenv("TF_ACC_TERRAFORM_PATH", p)
			if os.Getenv("TF_ACC_PROVIDER_HOST") == "" {
				_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
			}
		}
	}
	os.Exit(m.Run())
}

// Starting the server and fetching the schema validates every resource and
// data source schema, and checks that none went missing.
func TestProviderServerSchema(t *testing.T) {
	ctx := context.Background()
	factory, err := ProviderServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := factory().GetProviderSchema(ctx, &tfprotov5.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov5.DiagnosticSeverityError {
			t.Errorf("schema diagnostic: %s: %s", d.Summary, d.Detail)
		}
	}
	want := []string{
		"nextdns_allowlist", "nextdns_denylist", "nextdns_parental_control", "nextdns_privacy",
		"nextdns_profile", "nextdns_rewrite", "nextdns_security", "nextdns_settings",
	}
	for _, name := range want {
		if _, ok := resp.ResourceSchemas[name]; !ok {
			t.Errorf("resource %s missing from the muxed schema", name)
		}
	}
	for _, name := range []string{"nextdns_setup_endpoint", "nextdns_setup_linkedip"} {
		if _, ok := resp.DataSourceSchemas[name]; !ok {
			t.Errorf("data source %s missing from the muxed schema", name)
		}
	}
}

// protoV5ProviderFactories serves the muxed provider under test.
func protoV5ProviderFactories() map[string]func() (tfprotov5.ProviderServer, error) {
	return map[string]func() (tfprotov5.ProviderServer, error){
		"nextdns": func() (tfprotov5.ProviderServer, error) {
			factory, err := ProviderServer(context.Background())
			if err != nil {
				return nil, err
			}
			return factory(), nil
		},
	}
}

// providerBlock renders the provider configuration for a fake API.
func providerBlock(f *fakeAPI) string {
	return fmt.Sprintf(`
provider "nextdns" {
  api_key  = "test-key"
  base_url = %q
}
`, f.Server.URL+"/")
}

// The API key is sent to base_url, so anything other than the API or a
// loopback stub is a configuration error, whether it is set in the provider
// block or through NEXTDNS_BASE_URL.
func TestProvider_BaseURLMustBeTheAPIOrLoopback(t *testing.T) {
	profile := `
resource "nextdns_profile" "this" {
  name = "base-url-check"
}
`
	for name, tc := range map[string]struct{ block, env string }{
		"attribute": {block: `
provider "nextdns" {
  api_key  = "test-key"
  base_url = "https://example.com/"
}
`},
		"plain http to the API host": {block: `
provider "nextdns" {
  api_key  = "test-key"
  base_url = "http://api.nextdns.io/"
}
`},
		"environment": {block: `
provider "nextdns" {
  api_key = "test-key"
}
`, env: "http://192.0.2.10/"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("NEXTDNS_BASE_URL", tc.env)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV5ProviderFactories: protoV5ProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      tc.block + profile,
					ExpectError: regexp.MustCompile(`Invalid NextDNS base URL`),
				}},
			})
		})
	}
}
