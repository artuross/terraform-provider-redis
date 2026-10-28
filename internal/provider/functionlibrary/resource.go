// Package functionlibrary implements the redis_function_library resource.
package functionlibrary

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/redis/go-redis/v9"
)

var (
	_ resource.Resource                = (*functionLibraryResource)(nil)
	_ resource.ResourceWithConfigure   = (*functionLibraryResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*functionLibraryResource)(nil)
	_ resource.ResourceWithImportState = (*functionLibraryResource)(nil)
)

var (
	pathContent = path.Root("content")
	pathName    = path.Root("name")
)

type functionLibraryResource struct {
	client *redis.Client
}

type functionLibraryModel struct {
	Content types.String `tfsdk:"content"`
	Name    types.String `tfsdk:"name"`
}

func NewResource() resource.Resource {
	return &functionLibraryResource{}
}

func (r *functionLibraryResource) Metadata(
	_ context.Context,
	req resource.MetadataRequest,
	resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_function_library"
}

func (r *functionLibraryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Redis function library.",
		Attributes: map[string]schema.Attribute{
			"content": schema.StringAttribute{
				MarkdownDescription: "Library source code. The first line is the shebang with the engine and the " +
					"library name, e.g. `#!lua name=mylib`.",
				Required: true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Library name, taken from the shebang line of `content`.",
				Computed:            true,
			},
		},
	}
}

func (r *functionLibraryResource) Configure(
	_ context.Context,
	req resource.ConfigureRequest,
	resp *resource.ConfigureResponse,
) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*redis.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"unexpected provider data",
			fmt.Sprintf("expected *redis.Client, got %T", req.ProviderData),
		)
		return
	}

	r.client = client
}

// ModifyPlan derives `name` from the shebang so that renaming a library forces replacement
// (FUNCTION LOAD REPLACE would otherwise leave the old library behind).
func (r *functionLibraryResource) ModifyPlan(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan functionLibraryModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Content.IsUnknown() {
		return
	}

	name, err := parseLibraryName(plan.Content.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(pathContent, "invalid library content", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, pathName, name)...)

	if req.State.Raw.IsNull() {
		return
	}

	var state functionLibraryModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.Name.ValueString() != name {
		resp.RequiresReplace = append(resp.RequiresReplace, pathName)
	}
}

func (r *functionLibraryResource) Create(
	ctx context.Context,
	req resource.CreateRequest,
	resp *resource.CreateResponse,
) {
	var plan functionLibraryModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name, err := r.client.FunctionLoad(ctx, plan.Content.ValueString()).Result()
	if err != nil {
		r.addError(ctx, &resp.Diagnostics, "loading function library", err)
		return
	}

	plan.Name = types.StringValue(name)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *functionLibraryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state functionLibraryModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	libs, err := r.client.FunctionList(ctx, redis.FunctionListQuery{
		LibraryNamePattern: state.Name.ValueString(),
		WithCode:           true,
	}).Result()
	if err != nil {
		r.addError(ctx, &resp.Diagnostics, "listing function libraries", err)
		return
	}

	// LIBRARYNAME is a pattern, so look for an exact match
	for _, lib := range libs {
		if lib.Name == state.Name.ValueString() {
			state.Content = types.StringValue(lib.Code)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

			return
		}
	}

	resp.State.RemoveResource(ctx)
}

func (r *functionLibraryResource) Update(
	ctx context.Context,
	req resource.UpdateRequest,
	resp *resource.UpdateResponse,
) {
	var plan functionLibraryModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name, err := r.client.FunctionLoadReplace(ctx, plan.Content.ValueString()).Result()
	if err != nil {
		r.addError(ctx, &resp.Diagnostics, "replacing function library", err)
		return
	}

	plan.Name = types.StringValue(name)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *functionLibraryResource) Delete(
	ctx context.Context,
	req resource.DeleteRequest,
	resp *resource.DeleteResponse,
) {
	var state functionLibraryModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.FunctionDelete(ctx, state.Name.ValueString()).Err()
	if err != nil && !strings.Contains(err.Error(), "Library not found") {
		r.addError(ctx, &resp.Diagnostics, "deleting function library", err)
	}
}

// ImportState imports a library by its name; Read then fills in `content`.
func (r *functionLibraryResource) ImportState(
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
) {
	resource.ImportStatePassthroughID(ctx, pathName, req, resp)
}

// parseLibraryName extracts the `name` from a shebang like `#!lua name=mylib`.
func parseLibraryName(content string) (string, error) {
	line, _, _ := strings.Cut(content, "\n")
	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "#!") {
		return "", errors.New("content must start with a shebang line, e.g. `#!lua name=mylib`")
	}

	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	for _, field := range fields[min(1, len(fields)):] {
		if value, ok := strings.CutPrefix(field, "name="); ok && value != "" {
			return value, nil
		}
	}

	return "", errors.New("shebang line must contain `name=<library name>`")
}
