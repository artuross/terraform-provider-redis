// Package redisstring implements the redis_string resource.
package redisstring

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/redis/go-redis/v9"
)

var (
	_ resource.Resource                = (*stringResource)(nil)
	_ resource.ResourceWithConfigure   = (*stringResource)(nil)
	_ resource.ResourceWithImportState = (*stringResource)(nil)
)

var pathKey = path.Root("key")

type stringResource struct {
	client *redis.Client
}

type stringModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

func NewResource() resource.Resource {
	return &stringResource{}
}

func (r *stringResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_string"
}

func (r *stringResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Redis string.",
		Attributes: map[string]schema.Attribute{
			"key": schema.StringAttribute{
				MarkdownDescription: "Key of the string.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "The value.",
				Required:            true,
			},
		},
	}
}

func (r *stringResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *stringResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan stringModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// NX: never overwrite a key Terraform doesn't manage
	created, err := r.client.SetNX(ctx, plan.Key.ValueString(), plan.Value.ValueString(), 0).Result()
	if err != nil {
		resp.Diagnostics.AddError("setting string", err.Error())
		return
	}

	if !created {
		resp.Diagnostics.AddAttributeError(
			pathKey,
			"key already exists",
			fmt.Sprintf(
				"The key %q already exists. Import it to manage it with Terraform, or remove it first.",
				plan.Key.ValueString(),
			),
		)

		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state stringModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	value, err := r.client.Get(ctx, state.Key.ValueString()).Result()
	if errors.Is(err, redis.Nil) {
		resp.State.RemoveResource(ctx)
		return
	}

	if err != nil {
		resp.Diagnostics.AddError("reading string", err.Error())
		return
	}

	state.Value = types.StringValue(value)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *stringResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan stringModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.Set(ctx, plan.Key.ValueString(), plan.Value.ValueString(), 0).Err()
	if err != nil {
		resp.Diagnostics.AddError("setting string", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *stringResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state stringModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// a key that's already gone is fine
	if err := r.client.Del(ctx, state.Key.ValueString()).Err(); err != nil {
		resp.Diagnostics.AddError("deleting string", err.Error())
	}
}

// ImportState imports a string by its key; Read then fills in `value`.
func (r *stringResource) ImportState(
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
) {
	resource.ImportStatePassthroughID(ctx, pathKey, req, resp)
}
