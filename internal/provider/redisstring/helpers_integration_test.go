//go:build integration

package redisstring_test

import (
	"context"
	"errors"
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
	"github.com/artuross/terraform-provider-redis/internal/provider/redisstring"
)

// stringResource returns a redis_string resource named `name`; key and value are Go-quoted, which is valid HCL for
// printable strings (don't use `${`).
func stringResource(name, key, value string) string {
	return fmt.Sprintf("resource \"redis_string\" %q {\n  key   = %q\n  value = %q\n}\n", name, key, value)
}

// setKey sets a key behind Terraform's back.
func setKey(t *testing.T, server *acctest.Server, key, value string) {
	t.Helper()

	require.NoError(t, server.Client.Set(context.Background(), key, value, 0).Err())
}

// deleteKey deletes a key behind Terraform's back.
func deleteKey(t *testing.T, server *acctest.Server, key string) {
	t.Helper()

	require.NoError(t, server.Client.Del(context.Background(), key).Err())
}

// checkValue checks that the key holds `want`.
func checkValue(server *acctest.Server, key, want string) func(*terraform.State) error {
	return func(*terraform.State) error {
		got, err := server.Client.Get(context.Background(), key).Result()
		if err != nil {
			return fmt.Errorf("GET %s: %w", key, err)
		}

		if got != want {
			return fmt.Errorf("GET %s: got %q, want %q", key, got, want)
		}

		return nil
	}
}

// checkKeys checks that exactly the given keys exist.
func checkKeys(server *acctest.Server, want ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		got, err := server.Client.Keys(context.Background(), "*").Result()
		if err != nil {
			return fmt.Errorf("KEYS: %w", err)
		}

		gotSet := make(map[string]bool, len(got))
		for _, key := range got {
			gotSet[key] = true
		}

		wantSet := make(map[string]bool, len(want))
		for _, key := range want {
			wantSet[key] = true
		}

		for key := range wantSet {
			if !gotSet[key] {
				return fmt.Errorf("key %q doesn't exist", key)
			}
		}

		for key := range gotSet {
			if !wantSet[key] {
				return fmt.Errorf("unexpected key %q exists", key)
			}
		}

		return nil
	}
}

// directResource returns the resource configured the way Terraform configures it, see acctest.ConfigureResource.
func directResource(t *testing.T, url string, env map[string]string) fwresource.Resource {
	t.Helper()

	r := redisstring.NewResource()
	acctest.ConfigureResource(t, r, url, env)

	return r
}

// callCreate creates the key, like `terraform apply` for a new resource.
func callCreate(t *testing.T, r fwresource.Resource, key, value string) *fwresource.CreateResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)

	resp := &fwresource.CreateResponse{State: tfsdk.State{Schema: s, Raw: stringObject(t, s, nil, nil)}}
	r.Create(t.Context(), fwresource.CreateRequest{
		Config: tfsdk.Config{Schema: s, Raw: stringObject(t, s, key, value)},
		Plan:   tfsdk.Plan{Schema: s, Raw: stringObject(t, s, key, value)},
	}, resp)

	return resp
}

// callUpdate sets the key, holding oldValue in the state, to newValue.
func callUpdate(t *testing.T, r fwresource.Resource, key, oldValue, newValue string) *fwresource.UpdateResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: stringObject(t, s, key, oldValue)}

	resp := &fwresource.UpdateResponse{State: state}
	r.Update(t.Context(), fwresource.UpdateRequest{
		Config: tfsdk.Config{Schema: s, Raw: stringObject(t, s, key, newValue)},
		Plan:   tfsdk.Plan{Schema: s, Raw: stringObject(t, s, key, newValue)},
		State:  state,
	}, resp)

	return resp
}

// callRead reads the key without a value, like right after `terraform import`.
func callRead(t *testing.T, r fwresource.Resource, key string) *fwresource.ReadResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: stringObject(t, s, key, nil)}

	resp := &fwresource.ReadResponse{State: state}
	r.Read(t.Context(), fwresource.ReadRequest{State: state}, resp)

	return resp
}

// callDelete deletes the key.
func callDelete(t *testing.T, r fwresource.Resource, key, value string) *fwresource.DeleteResponse {
	t.Helper()

	s := acctest.SchemaOf(t, r)
	state := tfsdk.State{Schema: s, Raw: stringObject(t, s, key, value)}

	resp := &fwresource.DeleteResponse{State: state}
	r.Delete(t.Context(), fwresource.DeleteRequest{State: state}, resp)

	return resp
}

// stringObject returns a resource object; key and value are a string or nil (null). Both nil returns a null object,
// e.g. the state before creating.
func stringObject(t *testing.T, s schema.Schema, key, value any) tftypes.Value {
	t.Helper()

	objectType := s.Type().TerraformType(t.Context())
	if key == nil && value == nil {
		return tftypes.NewValue(objectType, nil)
	}

	return tftypes.NewValue(objectType, map[string]tftypes.Value{
		"key":   tftypes.NewValue(tftypes.String, key),
		"value": tftypes.NewValue(tftypes.String, value),
	})
}

// keyType returns the type of the key, e.g. "string", "hash" or "none".
func keyType(t *testing.T, server *acctest.Server, key string) string {
	t.Helper()

	typ, err := server.Client.Type(context.Background(), key).Result()
	if errors.Is(err, redis.Nil) {
		return "none"
	}

	require.NoError(t, err)

	return typ
}
