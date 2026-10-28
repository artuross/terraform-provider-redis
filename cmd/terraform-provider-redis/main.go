package main

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/rs/zerolog"
	"github.com/urfave/cli/v3"

	"github.com/artuross/terraform-provider-redis/internal/provider"
)

// version is set at build/release time
var version = "dev"

func main() {
	// stdout is reserved for the plugin handshake, Terraform collects stderr as logs
	logger := zerolog.New(zerolog.ConsoleWriter{
		Out:     os.Stderr,
		NoColor: !isTerminal(os.Stderr),
	}).
		With().
		Timestamp().
		Logger()

	cmd := &cli.Command{
		Name:    "terraform-provider-redis",
		Usage:   "Terraform / OpenTofu provider for Redis",
		Version: version,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "run the provider with support for debuggers like delve",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts := providerserver.ServeOpts{
				Address: "registry.terraform.io/artuross/redis",
				Debug:   cmd.Bool("debug"),
			}

			return providerserver.Serve(ctx, provider.New(version), opts)
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		logger.Fatal().Err(err).Msg("running the provider")
	}
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
