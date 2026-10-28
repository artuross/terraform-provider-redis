//go:build unit

package functionlibrary_test

import (
	_ "embed"
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

	"github.com/artuross/terraform-provider-redis/internal/provider/functionlibrary"
)

// a valid library named "hello"
//
//go:embed testdata/hello.lua
var helloLibrary string

var (
	nullString    = tftypes.NewValue(tftypes.String, nil)
	unknownString = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
)

func TestNewResource(t *testing.T) {
	r := functionlibrary.NewResource()

	assert.Implements(t, (*resource.ResourceWithConfigure)(nil), r)
	assert.Implements(t, (*resource.ResourceWithImportState)(nil), r)
	assert.Implements(t, (*resource.ResourceWithModifyPlan)(nil), r)
}

func TestResource_Metadata(t *testing.T) {
	resp := &resource.MetadataResponse{}
	functionlibrary.NewResource().
		Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "redis"}, resp)

	assert.Equal(t, "redis_function_library", resp.TypeName)
}

func TestResource_Schema(t *testing.T) {
	s := resourceSchema(t)

	require.Empty(t, s.ValidateImplementation(t.Context()))
	require.Len(t, s.Attributes, 2)

	t.Run("content", func(t *testing.T) {
		attr, ok := s.Attributes["content"]
		require.True(t, ok)

		assert.True(t, attr.IsRequired())
	})

	t.Run("name", func(t *testing.T) {
		attr, ok := s.Attributes["name"]
		require.True(t, ok)

		assert.True(t, attr.IsComputed())
		assert.False(t, attr.IsOptional())
		assert.False(t, attr.IsRequired())
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

				assert.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
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

func TestResource_ModifyPlan(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		type testCase struct {
			name            string
			state           *libraryValues // nil when creating
			plan            *libraryValues // nil when destroying
			wantName        tftypes.Value
			wantReplacement bool
		}

		tests := []testCase{
			{
				name:     "create",
				plan:     &libraryValues{content: str(helloLibrary), name: unknownString},
				wantName: str("hello"),
			},
			{
				name:     "create with options after the name",
				plan:     &libraryValues{content: str("#!lua name=hello foo=bar\n"), name: unknownString},
				wantName: str("hello"),
			},
			{
				name:     "create with leading whitespace",
				plan:     &libraryValues{content: str("  #!lua   name=hello  \n"), name: unknownString},
				wantName: str("hello"),
			},
			{
				name:     "unknown content",
				plan:     &libraryValues{content: unknownString, name: unknownString},
				wantName: unknownString,
			},
			{
				name:     "update keeping the name",
				state:    &libraryValues{content: str(helloLibrary), name: str("hello")},
				plan:     &libraryValues{content: str("#!lua name=hello\n-- changed\n"), name: unknownString},
				wantName: str("hello"),
			},
			{
				name:            "rename",
				state:           &libraryValues{content: str(helloLibrary), name: str("hello")},
				plan:            &libraryValues{content: str("#!lua name=renamed\n"), name: unknownString},
				wantName:        str("renamed"),
				wantReplacement: true,
			},
			{
				name:  "destroy",
				state: &libraryValues{content: str(helloLibrary), name: str("hello")},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := modifyPlan(t, tt.state, tt.plan)
				require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

				if tt.plan == nil {
					assert.True(t, resp.Plan.Raw.IsNull())
					assert.Empty(t, resp.RequiresReplace)

					return
				}

				var name types.String

				require.False(t, resp.Plan.GetAttribute(t.Context(), path.Root("name"), &name).HasError())
				assert.Equal(t, tt.wantName.IsKnown(), !name.IsUnknown())

				if tt.wantName.IsKnown() {
					var want string

					require.NoError(t, tt.wantName.As(&want))
					assert.Equal(t, want, name.ValueString())
				}

				if tt.wantReplacement {
					assert.Equal(t, path.Paths{path.Root("name")}, resp.RequiresReplace)
				} else {
					assert.Empty(t, resp.RequiresReplace)
				}
			})
		}
	})

	t.Run("failure", func(t *testing.T) {
		const (
			noShebang = "content must start with a shebang line, e.g. `#!lua name=mylib`"
			noName    = "shebang line must contain `name=<library name>`"
		)

		type testCase struct {
			name       string
			content    string
			wantDetail string
		}

		tests := []testCase{
			{name: "empty", content: "", wantDetail: noShebang},
			{name: "no shebang", content: "redis.register_function('f', f)\n", wantDetail: noShebang},
			{name: "shebang on second line", content: "\n#!lua name=hello\n", wantDetail: noShebang},
			{name: "no name", content: "#!lua\n", wantDetail: noName},
			{name: "empty name", content: "#!lua name=\n", wantDetail: noName},
			{name: "name in engine position", content: "#!name=hello\n", wantDetail: noName},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := modifyPlan(t, nil, &libraryValues{content: str(tt.content), name: unknownString})

				want := diag.Diagnostics{
					diag.NewAttributeErrorDiagnostic(path.Root("content"), "invalid library content", tt.wantDetail),
				}
				assert.Equal(t, want, resp.Diagnostics)
			})
		}
	})
}

func TestResource_ImportState(t *testing.T) {
	ctx := t.Context()
	s := resourceSchema(t)

	importable, ok := functionlibrary.NewResource().(resource.ResourceWithImportState)
	require.True(t, ok)

	resp := &resource.ImportStateResponse{
		State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)},
	}
	importable.ImportState(ctx, resource.ImportStateRequest{ID: "hello"}, resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

	var name, content types.String

	require.False(t, resp.State.GetAttribute(ctx, path.Root("name"), &name).HasError())
	require.False(t, resp.State.GetAttribute(ctx, path.Root("content"), &content).HasError())

	assert.Equal(t, "hello", name.ValueString())
	// filled in by Read
	assert.True(t, content.IsNull())
}

type libraryValues struct {
	content tftypes.Value
	name    tftypes.Value
}

func str(value string) tftypes.Value {
	return tftypes.NewValue(tftypes.String, value)
}

func resourceSchema(t *testing.T) schema.Schema {
	t.Helper()

	resp := &resource.SchemaResponse{}
	functionlibrary.NewResource().Schema(t.Context(), resource.SchemaRequest{}, resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

	return resp.Schema
}

func configurable(t *testing.T) resource.ResourceWithConfigure {
	t.Helper()

	r, ok := functionlibrary.NewResource().(resource.ResourceWithConfigure)
	require.True(t, ok)

	return r
}

// modifyPlan runs ModifyPlan the way Terraform does: a nil state means create, a nil plan destroy.
func modifyPlan(t *testing.T, state, plan *libraryValues) *resource.ModifyPlanResponse {
	t.Helper()

	ctx := t.Context()
	s := resourceSchema(t)
	objectType := s.Type().TerraformType(ctx)

	toValue := func(values *libraryValues) tftypes.Value {
		if values == nil {
			return tftypes.NewValue(objectType, nil)
		}

		return tftypes.NewValue(objectType, map[string]tftypes.Value{
			"content": values.content,
			"name":    values.name,
		})
	}

	// config has the same content as the plan, but never a value for the computed name
	config := (*libraryValues)(nil)
	if plan != nil {
		config = &libraryValues{content: plan.content, name: nullString}
	}

	req := resource.ModifyPlanRequest{
		Config: tfsdk.Config{Schema: s, Raw: toValue(config)},
		Plan:   tfsdk.Plan{Schema: s, Raw: toValue(plan)},
		State:  tfsdk.State{Schema: s, Raw: toValue(state)},
	}
	resp := &resource.ModifyPlanResponse{Plan: req.Plan}

	modifier, ok := functionlibrary.NewResource().(resource.ResourceWithModifyPlan)
	require.True(t, ok)

	modifier.ModifyPlan(ctx, req, resp)

	return resp
}
