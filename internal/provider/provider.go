package provider

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/redis/go-redis/v9"

	"github.com/artuross/terraform-provider-redis/internal/provider/functionlibrary"
	"github.com/artuross/terraform-provider-redis/internal/provider/redisstring"
)

var _ provider.Provider = (*redisProvider)(nil)

type redisProvider struct {
	version string
	getenv  func(key string) string
}

// provider documentation, Markdown
//
//go:embed description.md
var description string

type redisProviderModel struct {
	URL types.String `tfsdk:"url"`
}

// Option customizes the provider, mainly for tests.
type Option func(*redisProvider)

// WithGetenv replaces os.Getenv for reading the REDIS_* environment variables, so tests don't have to change the
// process environment.
func WithGetenv(getenv func(key string) string) Option {
	return func(p *redisProvider) {
		p.getenv = getenv
	}
}

func New(version string, opts ...Option) func() provider.Provider {
	return func() provider.Provider {
		p := &redisProvider{version: version, getenv: os.Getenv}

		for _, opt := range opts {
			opt(p)
		}

		return p
	}
}

func (p *redisProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "redis"
	resp.Version = p.version
}

func (p *redisProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		// credentials are deliberately not part of the schema so they can't end up in committed .tf files
		MarkdownDescription: strings.TrimSpace(description),
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				MarkdownDescription: "Server URL, `redis://host:port`, or `rediss://host:port` for TLS. " +
					"[go-redis URL options](https://pkg.go.dev/github.com/redis/go-redis/v9#ParseURL) are supported, " +
					"e.g. `?dial_timeout=5s`. Must not contain credentials.",
				Required: true,
			},
		},
	}
}

func (p *redisProvider) Configure(
	ctx context.Context,
	req provider.ConfigureRequest,
	resp *provider.ConfigureResponse,
) {
	var config redisProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// e.g. when it comes from a resource that doesn't exist yet
	if config.URL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("url"),
			"unknown Redis URL",
			"The url isn't known until apply, e.g. because it comes from a resource that doesn't exist yet. Use a "+
				"value known while planning, or create that resource first (e.g. with -target).",
		)

		return
	}

	opts, err := parseURL(config.URL.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "invalid Redis URL", err.Error())
		return
	}

	opts.Username = p.getenv("REDIS_USERNAME")
	opts.Password = p.getenv("REDIS_PASSWORD")

	client := redis.NewClient(opts)

	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *redisProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		functionlibrary.NewResource,
		redisstring.NewResource,
	}
}

func (p *redisProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

// parseURL parses a redis:// or rediss:// URL. Errors never include the URL, it may contain a password.
func parseURL(rawURL string) (*redis.Options, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("not a valid URL, expected redis://host:port or rediss://host:port")
	}

	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, fmt.Errorf("unsupported scheme %q, expected redis:// or rediss:// (TLS)", u.Scheme)
	}

	if u.User != nil {
		return nil, errors.New(
			"the URL must not contain credentials, set the REDIS_USERNAME and REDIS_PASSWORD environment variables",
		)
	}

	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}

	return opts, nil
}
