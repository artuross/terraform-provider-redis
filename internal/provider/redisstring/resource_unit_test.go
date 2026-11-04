//go:build unit

package redisstring_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/artuross/terraform-provider-redis/internal/provider/redisstring"
)

func TestNewResource(t *testing.T) {
	r := redisstring.NewResource()

	assert.Implements(t, (*resource.ResourceWithConfigure)(nil), r)
	assert.Implements(t, (*resource.ResourceWithImportState)(nil), r)
}

func TestResource_Metadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	redisstring.NewResource().Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "redis"}, resp)

	assert.Equal(t, "redis_string", resp.TypeName)
}

func TestResource_Schema(t *testing.T) {
	s := resourceSchema(t)

	require.Empty(t, s.ValidateImplementation(t.Context()))
	require.Len(t, s.Attributes, 2)

	t.Run("key", func(t *testing.T) {
		attr, ok := s.Attributes["key"].(schema.StringAttribute)
		require.True(t, ok)

		assert.True(t, attr.IsRequired())
		// a different key is a different string: delete the old one, create the new one
		assert.Len(t, attr.PlanModifiers, 1)
	})

	t.Run("value", func(t *testing.T) {
		attr, ok := s.Attributes["value"].(schema.StringAttribute)
		require.True(t, ok)

		assert.True(t, attr.IsRequired())
		assert.Empty(t, attr.PlanModifiers)
	})
}

func TestResource_Configure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		type testCase struct {
			name         string
			providerData any
		}

		tests := []testCase{
			// the provider isn't configured yet during validation
			{name: "no provider data", providerData: nil},
			{name: "redis client", providerData: redis.NewClient(&redis.Options{})},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := &resource.ConfigureResponse{}
				configurable(t).Configure(t.Context(), resource.ConfigureRequest{ProviderData: tt.providerData}, resp)

				assert.Empty(t, resp.Diagnostics)
			})
		}
	})

	t.Run("failure", func(t *testing.T) {
		resp := &resource.ConfigureResponse{}
		configurable(t).Configure(t.Context(), resource.ConfigureRequest{ProviderData: "not a client"}, resp)

		want := diag.Diagnostics{
			diag.NewErrorDiagnostic("unexpected provider data", "expected *redis.Client, got string"),
		}
		assert.Equal(t, want, resp.Diagnostics)
	})
}

func TestResource_ImportState(t *testing.T) {
	ctx := t.Context()
	s := resourceSchema(t)

	importable, ok := redisstring.NewResource().(resource.ResourceWithImportState)
	require.True(t, ok)

	resp := &resource.ImportStateResponse{
		State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)},
	}
	importable.ImportState(ctx, resource.ImportStateRequest{ID: "app:config"}, resp)
	require.Empty(t, resp.Diagnostics)

	var key, value types.String

	require.False(t, resp.State.GetAttribute(ctx, path.Root("key"), &key).HasError())
	require.False(t, resp.State.GetAttribute(ctx, path.Root("value"), &value).HasError())

	assert.Equal(t, "app:config", key.ValueString())
	// filled in by Read
	assert.True(t, value.IsNull())
}

func resourceSchema(t *testing.T) schema.Schema {
	t.Helper()

	resp := &resource.SchemaResponse{}
	redisstring.NewResource().Schema(t.Context(), resource.SchemaRequest{}, resp)
	require.Empty(t, resp.Diagnostics)

	return resp.Schema
}

func configurable(t *testing.T) resource.ResourceWithConfigure {
	t.Helper()

	r, ok := redisstring.NewResource().(resource.ResourceWithConfigure)
	require.True(t, ok)

	return r
}
