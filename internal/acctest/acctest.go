//go:build integration

// Package acctest has the shared helpers of the integration (acceptance) tests: Redis/Valkey containers, the provider
// configured like Terraform does, and assertions.
package acctest

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/artuross/terraform-provider-redis/internal/provider"
)

// RedisImage is the image to test against, e.g. `REDIS_IMAGE=valkey/valkey:9.1.3-alpine3.24`.
var RedisImage = cmp.Or(os.Getenv("REDIS_IMAGE"), "redis:8.10.3-alpine3.23")

// Server is a Redis/Valkey container of a single test.
type Server struct {
	// URL for the provider, redis://host:port or rediss://host:port
	URL string
	// Client to inspect and change the server behind Terraform's back.
	Client *redis.Client
}

// StartRedis starts a container of RedisImage for this test only, it's removed when the test finishes. opts can e.g.
// replace the command to configure ACL users.
func StartRedis(t *testing.T, opts ...testcontainers.ContainerCustomizer) *Server {
	t.Helper()

	return StartRedisImage(t, RedisImage, opts...)
}

// StartRedisImage is StartRedis for a specific image, e.g. an old version.
func StartRedisImage(t *testing.T, image string, opts ...testcontainers.ContainerCustomizer) *Server {
	t.Helper()

	_, address := runContainer(t, image, opts...)

	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })

	return &Server{
		URL:    "redis://" + address,
		Client: client,
	}
}

// StartRedisWithTLS starts Redis accepting only TLS connections, with a self-signed certificate. Clients don't need a
// certificate of their own.
func StartRedisWithTLS(t *testing.T) *Server {
	t.Helper()

	container, address := runContainer(
		t,
		RedisImage,
		tcredis.WithTLS(),
		// WithTLS requires client certificates, the provider doesn't support them
		testcontainers.WithCmdArgs("--tls-auth-clients", "no"),
	)

	client := redis.NewClient(&redis.Options{Addr: address, TLSConfig: container.TLSConfig()})
	t.Cleanup(func() { _ = client.Close() })

	return &Server{
		URL:    "rediss://" + address,
		Client: client,
	}
}

// StartRedisWithUsers starts Redis with the passwordless default user disabled and two ACL users: `admin` (may do
// anything) and `reader` (may connect and read, but not write or manage functions). The returned client is logged in
// as admin.
func StartRedisWithUsers(t *testing.T) *Server {
	t.Helper()

	server := StartRedis(
		t,
		// every ACL rule must be its own argument
		testcontainers.WithCmd(
			"redis-server",
			"--user", "default", "off",
			"--user", "admin", "on", ">admin-password", "~*", "&*", "+@all",
			"--user", "reader", "on", ">reader-password", "~*", "&*", "+@read", "+@connection",
		),
	)

	admin := redis.NewClient(&redis.Options{
		Addr:     strings.TrimPrefix(server.URL, "redis://"),
		Username: "admin",
		Password: "admin-password",
	})
	t.Cleanup(func() { _ = admin.Close() })

	server.Client = admin

	return server
}

// runContainer starts the container and returns it with its host:port.
func runContainer(
	t *testing.T,
	image string,
	opts ...testcontainers.ContainerCustomizer,
) (*tcredis.RedisContainer, string) {
	t.Helper()

	// same opt-in as resource.Test, also skips tests that don't use it, before starting a container
	if os.Getenv("TF_ACC") == "" {
		t.Skip("integration tests skipped unless env 'TF_ACC' set, run them with `moon run test-integration`")
	}

	ctx := context.Background()

	container, err := tcredis.Run(ctx, image, opts...)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	address, err := container.PortEndpoint(ctx, "6379/tcp", "")
	require.NoError(t, err)

	return container, address
}

// ProviderFactories returns the provider reading environment variables (e.g. credentials) from env only, never from
// the process environment.
func ProviderFactories(env map[string]string) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"redis": providerserver.NewProtocol6WithError(
			provider.New(
				"test",
				provider.WithGetenv(func(key string) string {
					return env[key]
				}),
			)(),
		),
	}
}

// ProviderConfig returns a provider block for the given url followed by the given resources.
func ProviderConfig(url string, resources ...string) string {
	return fmt.Sprintf("provider \"redis\" {\n  url = %q\n}\n\n%s", url, strings.Join(resources, "\n"))
}

// CheckError checks that `check` fails with exactly the error `want`.
func CheckError(check func(*terraform.State) error, want string) func(*terraform.State) error {
	return func(state *terraform.State) error {
		err := check(state)
		if err == nil {
			return fmt.Errorf("expected the error %q", want)
		}

		if err.Error() != want {
			return fmt.Errorf("expected the error %q, got %q", want, err.Error())
		}

		return nil
	}
}

// Terraform only prints diagnostics, so the tests of failing calls call resources directly to compare them exactly.

// ConfigureResource configures r the way Terraform does: through the provider, with the given url and environment.
func ConfigureResource(t *testing.T, r fwresource.Resource, url string, env map[string]string) {
	t.Helper()

	ctx := t.Context()
	p := provider.New("test", provider.WithGetenv(func(key string) string { return env[key] }))()

	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(ctx, fwprovider.SchemaRequest{}, schemaResp)

	config := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"url": tftypes.NewValue(tftypes.String, url),
		}),
	}

	configureResp := &fwprovider.ConfigureResponse{}
	p.Configure(ctx, fwprovider.ConfigureRequest{Config: config}, configureResp)
	require.Empty(t, configureResp.Diagnostics)

	client, ok := configureResp.ResourceData.(*redis.Client)
	require.True(t, ok)
	t.Cleanup(func() { _ = client.Close() })

	configurable, ok := r.(fwresource.ResourceWithConfigure)
	require.True(t, ok)

	resourceResp := &fwresource.ConfigureResponse{}
	configurable.Configure(ctx, fwresource.ConfigureRequest{ProviderData: client}, resourceResp)
	require.Empty(t, resourceResp.Diagnostics)
}

// SchemaOf returns the schema of r.
func SchemaOf(t *testing.T, r fwresource.Resource) schema.Schema {
	t.Helper()

	resp := &fwresource.SchemaResponse{}
	r.Schema(t.Context(), fwresource.SchemaRequest{}, resp)
	require.Empty(t, resp.Diagnostics)

	return resp.Schema
}

// AssertRedisError checks that diags is exactly one error with our summary, e.g. "loading function library". The
// detail is the error of Redis or the connection, which isn't ours to assert on.
func AssertRedisError(t *testing.T, diags diag.Diagnostics, summary string) {
	t.Helper()

	require.Len(t, diags, 1)
	assert.Equal(t, diag.SeverityError, diags[0].Severity())
	assert.Equal(t, summary, diags[0].Summary())
	assert.NotEmpty(t, diags[0].Detail())
}
