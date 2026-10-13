package main

import (
	"fmt"
	"path/filepath"

	"github.com/regiellis/hyperchi/hyperchi/deployment"
	"github.com/spf13/cobra"
)

func deployBuildCmd() *cobra.Command {
	cfg := deployment.Config{}
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a Linux artifact with a Docker or systemd adapter",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Log = cmd.ErrOrStderr()
			manifest, err := deployment.Build(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			output, err := filepath.Abs(cfg.Output)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Built %s for %s/%s (%s) in %s\n", manifest.Name, manifest.OS, manifest.Arch, manifest.Target, output)
			return err
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&cfg.Name, "name", "", "Application name (required)")
	flags.StringVar(&cfg.Target, "target", "docker", "Runtime adapter: docker or systemd")
	flags.StringVar(&cfg.Project, "project", ".", "Source project directory")
	flags.StringVar(&cfg.Output, "output", ".hyperchi/deploy", "New artifact directory, relative to the current directory")
	flags.StringVar(&cfg.Package, "package", ".", "Local main package, such as ./cmd/server")
	flags.StringVar(&cfg.Arch, "arch", "", "Linux architecture: amd64 or arm64 (default host)")
	flags.IntVar(&cfg.Port, "port", 8080, "Default runtime port (1024-65535)")
	flags.StringSliceVar(&cfg.Assets, "assets", nil, "Top-level runtime files/directories to copy (comma-separated)")
	flags.StringVar(&cfg.RuntimeImage, "runtime-image", "", "Docker base image, optionally pinned by digest")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
