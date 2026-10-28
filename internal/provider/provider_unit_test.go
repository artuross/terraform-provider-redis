//go:build unit

package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/artuross/terraform-provider-redis/internal/provider"
)

func TestProvider_Metadata(t *testing.T) {
	p := provider.New("1.2.3", withEnv(nil))()

	resp := &fwprovider.MetadataResponse{}
	p.Metadata(t.Context(), fwprovider.MetadataRequest{}, resp)

	assert.Equal(t, "redis", resp.TypeName)
	assert.Equal(t, "1.2.3", resp.Version)
}

func TestProvider_Schema(t *testing.T) {
	s := providerSchema(t)

	require.Empty(t, s.ValidateImplementation(t.Context()))

	t.Run("url", func(t *testing.T) {
		attr, ok := s.Attributes["url"]
		require.True(t, ok)

		assert.True(t, attr.IsRequired())
	})

	// credentials are only accepted from environment variables
	t.Run("no credentials", func(t *testing.T) {
		assert.NotContains(t, s.Attributes, "username")
		assert.NotContains(t, s.Attributes, "password")
	})
}

func TestProvider_Configure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		type testCase struct {
			name         string
			url          tftypes.Value
			env          map[string]string
			wantAddress  string
			wantTLS      bool
			wantUsername string
			wantPassword string
		}

		tests := []testCase{
			{
				name:        "url from config",
				url:         tftypes.NewValue(tftypes.String, "redis://redis:1234"),
				wantAddress: "redis:1234",
			},
			{
				name:        "default port",
				url:         tftypes.NewValue(tftypes.String, "redis://redis"),
				wantAddress: "redis:6379",
			},
			{
				name:        "tls",
				url:         tftypes.NewValue(tftypes.String, "rediss://redis:1234"),
				wantAddress: "redis:1234",
				wantTLS:     true,
			},
			{
				name: "credentials from env",
				url:  tftypes.NewValue(tftypes.String, "redis://redis:1234"),
				env: map[string]string{
					"REDIS_USERNAME": "user",
					"REDIS_PASSWORD": "pass",
				},
				wantAddress:  "redis:1234",
				wantUsername: "user",
				wantPassword: "pass",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := configure(t, tt.url, tt.env)
				require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

				client, ok := resp.ResourceData.(*redis.Client)
				require.True(t, ok)
				assert.Same(t, client, resp.DataSourceData)

				opts := client.Options()
				assert.Equal(t, tt.wantAddress, opts.Addr)
				assert.Equal(t, tt.wantTLS, opts.TLSConfig != nil)
				assert.Equal(t, tt.wantUsername, opts.Username)
				assert.Equal(t, tt.wantPassword, opts.Password)
			})
		}
	})

	t.Run("failure", func(t *testing.T) {
		const credentialsDetail = "the URL must not contain credentials, set the REDIS_USERNAME and REDIS_PASSWORD " +
			"environment variables"

		type testCase struct {
			name        string
			url         tftypes.Value
			wantSummary string
			wantDetail  string
		}

		tests := []testCase{
			{
				name:        "not a url",
				url:         tftypes.NewValue(tftypes.String, "redis://secret@[::1"),
				wantSummary: "invalid Redis URL",
				wantDetail:  "not a valid URL, expected redis://host:port or rediss://host:port",
			},
			{
				name:        "no scheme",
				url:         tftypes.NewValue(tftypes.String, "localhost:6379"),
				wantSummary: "invalid Redis URL",
				wantDetail:  `unsupported scheme "localhost", expected redis:// or rediss:// (TLS)`,
			},
			{
				name:        "unsupported scheme",
				url:         tftypes.NewValue(tftypes.String, "unix:///tmp/redis.sock"),
				wantSummary: "invalid Redis URL",
				wantDetail:  `unsupported scheme "unix", expected redis:// or rediss:// (TLS)`,
			},
			{
				name:        "password in url",
				url:         tftypes.NewValue(tftypes.String, "redis://user:secret@redis:1234"),
				wantSummary: "invalid Redis URL",
				wantDetail:  credentialsDetail,
			},
			{
				name:        "username in url",
				url:         tftypes.NewValue(tftypes.String, "redis://user@redis:1234"),
				wantSummary: "invalid Redis URL",
				wantDetail:  credentialsDetail,
			},
			{
				name:        "unknown url",
				url:         tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
				wantSummary: "unknown Redis URL",
				wantDetail: "The url isn't known until apply, e.g. because it comes from a resource that doesn't " +
					"exist yet. Use a value known while planning, or create that resource first (e.g. with -target).",
			},
			{
				name:        "invalid option",
				url:         tftypes.NewValue(tftypes.String, "redis://redis:1234?dial_timeout=soon"),
				wantSummary: "invalid Redis URL",
				wantDetail:  `redis: invalid dial_timeout duration: time: invalid duration "soon"`,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := configure(t, tt.url, nil)

				want := diag.Diagnostics{
					diag.NewAttributeErrorDiagnostic(path.Root("url"), tt.wantSummary, tt.wantDetail),
				}
				assert.Equal(t, want, resp.Diagnostics)
				assert.Nil(t, resp.ResourceData)
			})
		}
	})
}

func TestProvider_Resources(t *testing.T) {
	resources := provider.New("test", withEnv(nil))().Resources(t.Context())
	require.Len(t, resources, 1)

	resp := &resource.MetadataResponse{}
	resources[0]().Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "redis"}, resp)

	assert.Equal(t, "redis_function_library", resp.TypeName)
}

func TestProvider_DataSources(t *testing.T) {
	assert.Empty(t, provider.New("test", withEnv(nil))().DataSources(t.Context()))
}

func providerSchema(t *testing.T) providerschema.Schema {
	t.Helper()

	resp := &fwprovider.SchemaResponse{}
	provider.New("test", withEnv(nil))().Schema(t.Context(), fwprovider.SchemaRequest{}, resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

	return resp.Schema
}

func configure(t *testing.T, url tftypes.Value, env map[string]string) *fwprovider.ConfigureResponse {
	t.Helper()

	s := providerSchema(t)
	config := tfsdk.Config{
		Schema: s,
		Raw: tftypes.NewValue(s.Type().TerraformType(t.Context()), map[string]tftypes.Value{
			"url": url,
		}),
	}

	resp := &fwprovider.ConfigureResponse{}
	provider.New("test", withEnv(env))().Configure(t.Context(), fwprovider.ConfigureRequest{Config: config}, resp)

	return resp
}
