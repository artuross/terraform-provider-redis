//go:build integration

package functionlibrary_test

import (
	"context"
	"fmt"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/artuross/terraform-provider-redis/internal/acctest"
	"github.com/artuross/terraform-provider-redis/internal/provider/functionlibrary"
)

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
func loadLibrary(t *testing.T, server *acctest.Server, content string, replace bool) {
	t.Helper()

	load := server.Client.FunctionLoad
	if replace {
		load = server.Client.FunctionLoadReplace
	}

	require.NoError(t, load(context.Background(), content).Err())
}

// deleteLibrary deletes a library behind Terraform's back.
func deleteLibrary(t *testing.T, server *acctest.Server, name string) {
	t.Helper()

	require.NoError(t, server.Client.FunctionDelete(context.Background(), name).Err())
}

// checkFCall checks that calling the function returns `want`.
func checkFCall(server *acctest.Server, function, want string) func(*terraform.State) error {
	return func(*terraform.State) error {
		got, err := server.Client.FCall(context.Background(), function, nil).Text()
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
func checkLibraries(server *acctest.Server, want ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		libraries, err := server.Client.FunctionList(context.Background(), redis.FunctionListQuery{}).Result()
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

// directResource returns the resource configured the way Terraform configures it, see acctest.ConfigureResource.
func directResource(t *testing.T, url string, env map[string]string) fwresource.Resource {
	t.Helper()

	r := functionlibrary.NewResource()
	acctest.ConfigureResource(t, r, url, env)

	return r
}

// callCreate creates a library from content, like `terraform apply` for a new resource.
func callCreate(t *testing.T, r fwresource.Resource, content string) *fwresource.CreateResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)

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

	s := acctest.SchemaOf(t, r)
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

	s := acctest.SchemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: libraryObject(t, s, nil, name)}

	resp := &fwresource.ReadResponse{State: state}
	r.Read(t.Context(), fwresource.ReadRequest{State: state}, resp)

	return resp
}

// callDelete deletes the library `name`.
func callDelete(t *testing.T, r fwresource.Resource, name, content string) *fwresource.DeleteResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: libraryObject(t, s, content, name)}

	resp := &fwresource.DeleteResponse{State: state}
	r.Delete(t.Context(), fwresource.DeleteRequest{State: state}, resp)

	return resp
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
