//go:build integration

package functionlibrary_test

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
	"github.com/artuross/terraform-provider-redis/internal/provider/functionlibrary"
)

// Redis image to test against, e.g. `REDIS_IMAGE=redis:7.0-alpine`
var redisImage = cmp.Or(os.Getenv("REDIS_IMAGE"), "redis:8.10.3-alpine3.23")

type redisServer struct {
	// provider url, redis://host:port or rediss://host:port
	url string
	// client to inspect and change Redis behind Terraform's back
	client *redis.Client
}

// startRedis starts a Redis container of REDIS_IMAGE for this test only, it's removed when the test finishes. opts can
// e.g. replace the command to configure ACL users.
func startRedis(t *testing.T, opts ...testcontainers.ContainerCustomizer) *redisServer {
	t.Helper()

	return startRedisImage(t, redisImage, opts...)
}

// startRedisImage is startRedis for a specific image, e.g. an old version.
func startRedisImage(t *testing.T, image string, opts ...testcontainers.ContainerCustomizer) *redisServer {
	t.Helper()

	_, address := runContainer(t, image, opts...)

	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })

	return &redisServer{
		url:    "redis://" + address,
		client: client,
	}
}

// startRedisWithTLS starts Redis accepting only TLS connections, with a self-signed certificate. Clients don't need a
// certificate of their own.
func startRedisWithTLS(t *testing.T) *redisServer {
	t.Helper()

	container, address := runContainer(
		t,
		redisImage,
		tcredis.WithTLS(),
		// WithTLS requires client certificates, the provider doesn't support them
		testcontainers.WithCmdArgs("--tls-auth-clients", "no"),
	)

	client := redis.NewClient(&redis.Options{Addr: address, TLSConfig: container.TLSConfig()})
	t.Cleanup(func() { _ = client.Close() })

	return &redisServer{
		url:    "rediss://" + address,
		client: client,
	}
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

// startRedisWithUsers starts Redis with the passwordless default user disabled and two ACL users: `admin` (may do
// anything) and `reader` (may connect, but not manage functions). The returned client is logged in as admin.
func startRedisWithUsers(t *testing.T) *redisServer {
	t.Helper()

	server := startRedis(
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
		Addr:     strings.TrimPrefix(server.url, "redis://"),
		Username: "admin",
		Password: "admin-password",
	})
	t.Cleanup(func() { _ = admin.Close() })

	server.client = admin

	return server
}

// providerFactories returns the provider reading environment variables (e.g. credentials) from env only, never from
// the process environment.
func providerFactories(env map[string]string) map[string]func() (tfprotov6.ProviderServer, error) {
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

// providerConfig returns a provider block for the given url followed by the given resources.
func providerConfig(url string, resources ...string) string {
	return fmt.Sprintf("provider \"redis\" {\n  url = %q\n}\n\n%s", url, strings.Join(resources, "\n"))
}

// libraryResource returns a redis_function_library resource named `name` with the given content.
func libraryResource(name, content string) string {
	return fmt.Sprintf("resource \"redis_function_library\" %q {\n  content = <<EOT\n%sEOT\n}\n", name, content)
}

// luaLibrary returns library code registering a single function that returns `value`.
func luaLibrary(library, function, value string) string {
	return fmt.Sprintf(
		"#!lua name=%s\n\nredis.register_function('%s', function(keys, args)\n  return '%s'\nend)\n",
		library,
		function,
		value,
	)
}

// loadLibrary loads a library behind Terraform's back.
func (s *redisServer) loadLibrary(t *testing.T, content string, replace bool) {
	t.Helper()

	load := s.client.FunctionLoad
	if replace {
		load = s.client.FunctionLoadReplace
	}

	require.NoError(t, load(context.Background(), content).Err())
}

// deleteLibrary deletes a library behind Terraform's back.
func (s *redisServer) deleteLibrary(t *testing.T, name string) {
	t.Helper()

	require.NoError(t, s.client.FunctionDelete(context.Background(), name).Err())
}

// checkFCall checks that calling the function returns `want`.
func (s *redisServer) checkFCall(function, want string) func(*terraform.State) error {
	return func(*terraform.State) error {
		got, err := s.client.FCall(context.Background(), function, nil).Text()
		if err != nil {
			return fmt.Errorf("FCALL %s: %w", function, err)
		}

		if got != want {
			return fmt.Errorf("FCALL %s: got %q, want %q", function, got, want)
		}

		return nil
	}
}

// checkLibraries checks that exactly the given libraries are loaded.
func (s *redisServer) checkLibraries(want ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		libraries, err := s.client.FunctionList(context.Background(), redis.FunctionListQuery{}).Result()
		if err != nil {
			return fmt.Errorf("FUNCTION LIST: %w", err)
		}

		got := make(map[string]bool, len(libraries))
		for _, library := range libraries {
			got[library.Name] = true
		}

		wantSet := make(map[string]bool, len(want))
		for _, name := range want {
			wantSet[name] = true
		}

		for name := range wantSet {
			if !got[name] {
				return fmt.Errorf("library %q is not loaded", name)
			}
		}

		for name := range got {
			if !wantSet[name] {
				return fmt.Errorf("unexpected library %q is loaded", name)
			}
		}

		return nil
	}
}

// checkError checks that `check` fails with exactly the error `want`.
func checkError(check func(*terraform.State) error, want string) func(*terraform.State) error {
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

// Terraform only prints diagnostics, so the tests of failing calls call the resource directly to compare them exactly.

// directResource returns the resource configured the way Terraform configures it: through the provider, with the given
// url and environment.
func directResource(t *testing.T, url string, env map[string]string) fwresource.Resource {
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

	r := functionlibrary.NewResource()

	configurable, ok := r.(fwresource.ResourceWithConfigure)
	require.True(t, ok)

	resourceResp := &fwresource.ConfigureResponse{}
	configurable.Configure(ctx, fwresource.ConfigureRequest{ProviderData: client}, resourceResp)
	require.Empty(t, resourceResp.Diagnostics)

	return r
}

// callCreate creates a library from content, like `terraform apply` for a new resource.
func callCreate(t *testing.T, r fwresource.Resource, content string) *fwresource.CreateResponse {
	t.Helper()

	s := schemaOf(t, r)

	resp := &fwresource.CreateResponse{State: tfsdk.State{Schema: s, Raw: libraryObject(t, s, nil, nil)}}
	r.Create(t.Context(), fwresource.CreateRequest{
		Config: tfsdk.Config{Schema: s, Raw: libraryObject(t, s, content, nil)},
		Plan:   tfsdk.Plan{Schema: s, Raw: libraryObject(t, s, content, tftypes.UnknownValue)},
	}, resp)

	return resp
}

// callUpdate replaces the library `name`, created from oldContent, with newContent.
func callUpdate(t *testing.T, r fwresource.Resource, name, oldContent, newContent string) *fwresource.UpdateResponse {
	t.Helper()

	s := schemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: libraryObject(t, s, oldContent, name)}

	resp := &fwresource.UpdateResponse{State: state}
	r.Update(t.Context(), fwresource.UpdateRequest{
		Config: tfsdk.Config{Schema: s, Raw: libraryObject(t, s, newContent, nil)},
		Plan:   tfsdk.Plan{Schema: s, Raw: libraryObject(t, s, newContent, name)},
		State:  state,
	}, resp)

	return resp
}

// callRead reads the library `name` without content, like right after `terraform import`.
func callRead(t *testing.T, r fwresource.Resource, name string) *fwresource.ReadResponse {
	t.Helper()

	s := schemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: libraryObject(t, s, nil, name)}

	resp := &fwresource.ReadResponse{State: state}
	r.Read(t.Context(), fwresource.ReadRequest{State: state}, resp)

	return resp
}

// callDelete deletes the library `name`.
func callDelete(t *testing.T, r fwresource.Resource, name, content string) *fwresource.DeleteResponse {
	t.Helper()

	s := schemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: libraryObject(t, s, content, name)}

	resp := &fwresource.DeleteResponse{State: state}
	r.Delete(t.Context(), fwresource.DeleteRequest{State: state}, resp)

	return resp
}

func schemaOf(t *testing.T, r fwresource.Resource) schema.Schema {
	t.Helper()

	resp := &fwresource.SchemaResponse{}
	r.Schema(t.Context(), fwresource.SchemaRequest{}, resp)
	require.Empty(t, resp.Diagnostics)

	return resp.Schema
}

// libraryObject returns a resource object; content and name are a string, nil (null) or tftypes.UnknownValue. Both nil
// returns a null object, e.g. the state before creating.
func libraryObject(t *testing.T, s schema.Schema, content, name any) tftypes.Value {
	t.Helper()

	objectType := s.Type().TerraformType(t.Context())
	if content == nil && name == nil {
		return tftypes.NewValue(objectType, nil)
	}

	return tftypes.NewValue(objectType, map[string]tftypes.Value{
		"content": tftypes.NewValue(tftypes.String, content),
		"name":    tftypes.NewValue(tftypes.String, name),
	})
}

// assertRedisError checks that diags is exactly one error with our summary, e.g. "loading function library". The
// detail is the error of Redis or the connection, which isn't ours to assert on.
func assertRedisError(t *testing.T, diags diag.Diagnostics, summary string) {
	t.Helper()

	require.Len(t, diags, 1)
	assert.Equal(t, diag.SeverityError, diags[0].Severity())
	assert.Equal(t, summary, diags[0].Summary())
	assert.NotEmpty(t, diags[0].Detail())
}
