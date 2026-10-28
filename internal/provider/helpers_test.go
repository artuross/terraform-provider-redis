//go:build unit

package provider_test

import "github.com/artuross/terraform-provider-redis/internal/provider"

// withEnv makes the provider read environment variables from the given map instead of the process environment.
func withEnv(env map[string]string) provider.Option {
	return provider.WithGetenv(func(key string) string {
		return env[key]
	})
}
