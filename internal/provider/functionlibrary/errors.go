package functionlibrary

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// servers before Redis 7.0 reply e.g. "ERR unknown command `FUNCTION`, with args beginning with: ..."
var unknownFunctionCommand = regexp.MustCompile("(?i)unknown command [`']function[`']")

// addError adds an error diagnostic for a Redis call that returned an error. When the server doesn't know the FUNCTION
// command, the detail explains which versions support functions.
func (r *functionLibraryResource) addError(ctx context.Context, diags *diag.Diagnostics, summary string, err error) {
	if !unknownFunctionCommand.MatchString(err.Error()) {
		diags.AddError(summary, err.Error())

		return
	}

	diags.AddError(
		summary,
		fmt.Sprintf(
			"%s doesn't support functions, they require Redis 7.0 or later, or Valkey 7.2 or later.\n\nServer error: %s",
			r.serverVersion(ctx),
			err,
		),
	)
}

// serverVersion returns e.g. "Redis 6.2.24" or "Valkey 7.2.15", or "The server" when it can't be determined.
func (r *functionLibraryResource) serverVersion(ctx context.Context) string {
	info, err := r.client.Info(ctx, "server").Result()
	if err != nil {
		return "The server"
	}

	fields := make(map[string]string)

	for line := range strings.SplitSeq(info, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			fields[key] = value
		}
	}

	// Valkey also reports redis_version, the Redis version it's compatible with
	if version := fields["valkey_version"]; version != "" {
		return "Valkey " + version
	}

	if version := fields["redis_version"]; version != "" {
		return "Redis " + version
	}

	return "The server"
}
