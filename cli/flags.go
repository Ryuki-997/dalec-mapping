package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

type BuildTarget string

const (
	AzLinux3Rpm           BuildTarget = "azlinux3/rpm"
	AzLinux3Container     BuildTarget = "azlinux3/container"
	NobleDeb              BuildTarget = "noble/deb"
	JammyDeb              BuildTarget = "jammy/deb"
	FocalDeb              BuildTarget = "focal/deb"
	BionicDeb             BuildTarget = "bionic/deb"
	BookwormDeb           BuildTarget = "bookworm/deb"
	WindowsCrossContainer BuildTarget = "windowscross/container"
)

type CLIOptions struct {
	// Required
	RepoPath string

	// Optional paths
	DockerfilePath string
	SpecFilePath   string
	MakefilePath   string
	OutputPath     string

	// Field overrides
	Name        string
	Description string
	License     string
	Tag         string

	// Build targets
	Targets []BuildTarget

	// Flags
	Discover    bool
	Verbose     bool
	ShowHelp    bool
	ShowContext bool
}

func DefineFlags() CLIOptions {
	var opts CLIOptions
	usedFlags := make(map[string]bool)

	// Define all flags
	repoPath := flag.String("repo", "", "GitHub repository (required)")
	dockerfilePath := flag.String("dockerfile", "", "Path to Dockerfile")
	specFilePath := flag.String("spec", "", "Path to previous Dalec spec")
	makefilePath := flag.String("makefile", "", "Path to Makefile")
	outputPath := flag.String("output", "output.yml", "Output YAML file path")

	name := flag.String("name", "", "Override package name")
	description := flag.String("description", "", "Override description")
	license := flag.String("license", "", "Override license")
	tag := flag.String("tag", "", "Fetch specific git tag and its commit SHA")

	var targetsStr string
	flag.StringVar(&targetsStr, "targets", "", "Comma-separated build targets")

	discover := flag.Bool("discover", false, "Discover Dockerfile and Makefile paths in repository")
	verbose := flag.Bool("v", false, "Verbose output")
	showHelp := flag.Bool("help", false, "Show complete usage")
	showContext := flag.Bool("h", false, "Show usage for current flags")

	flag.Usage = func() {
		printFullUsage()
	}

	flag.Parse()

	flag.Visit(func(f *flag.Flag) {
		usedFlags[f.Name] = true
	})

	if *showHelp {
		printFullUsage()
		os.Exit(0)
	}

	if *showContext {
		printContextualHelp(usedFlags)
		os.Exit(0)
	}

	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -repo flag is required\n\n")
		fmt.Fprintf(os.Stderr, "Usage: dalec-gen -repo owner/repo [options]\n")
		fmt.Fprintf(os.Stderr, "Run 'dalec-gen -help' for full information\n\n")
		os.Exit(1)
	}

	opts.RepoPath = *repoPath
	opts.DockerfilePath = *dockerfilePath
	opts.SpecFilePath = *specFilePath
	opts.MakefilePath = *makefilePath
	opts.OutputPath = *outputPath
	opts.Name = *name
	opts.Description = *description
	opts.License = *license
	opts.Tag = *tag
	opts.Discover = *discover
	opts.Verbose = *verbose

	if targetsStr != "" {
		for _, t := range strings.Split(targetsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				opts.Targets = append(opts.Targets, BuildTarget(t))
			}
		}
	}

	return opts
}

func printFullUsage() {
	fmt.Fprintf(os.Stderr, `Dalec Spec Generator

USAGE:
    dalec -repo <owner/repo> [options]

REQUIRED:
    -repo string
        GitHub repository (owner/repo or URL)

MODES:
    -discover
        Discover Dockerfile and Makefile paths in repository
        Outputs filepath.yml to result/{repo}/ directory

OPTIONAL PATHS:
    -dockerfile string
        Path to Dockerfile
    -spec string
        Path to previous spec
    -makefile string
        Path to Makefile
    -output string
        Output file path (default: "output.yml")

FIELD OVERRIDES:
    -name string
        Override package name
    -description string
        Override description
    -license string
        Override license (e.g., MIT, Apache-2.0)
    -tag string
        Fetch specific git tag and SHA

BUILD CONFIG:
    -targets string
        Comma-separated targets
        (azlinux3/rpm,azlinux3/container,noble/deb,etc.)

FLAGS:
    -v
        Verbose output
    -h
        Show contextual help
    -help
        Show this help

EXAMPLES:
    # Discover build files in a repository
    go run main.go -repo owner/repo -discover

    # Generate Dalec spec after discovery
    go run main.go -repo owner/repo -dockerfile result/repo/Dockerfile -output result/repo/spec.yml

    # With branch specification
    go run main.go -repo owner/repo/tree/main -discover

`)
}

func printContextualHelp(usedFlags map[string]bool) {
	fmt.Fprintf(os.Stderr, "Current command flags:\n\n")

	flagInfo := map[string]string{
		"repo":        "GitHub repository (required)",
		"dockerfile":  "Path to Dockerfile",
		"makefile":    "Path to Makefile",
		"spec":        "Path to previous Dalec spec",
		"output":      "Output YAML file path",
		"name":        "Override package name",
		"description": "Override description",
		"license":     "Override license identifier",
		"tag":         "Fetch specific git tag and SHA",
		"targets":     "Comma-separated build targets",
		"v":           "Enable verbose output",
		"h":           "Show contextual help",
		"help":        "Show complete usage",
	}

	order := []string{"repo", "dockerfile", "makefile", "spec", "output", "name",
		"description", "license", "tag", "targets", "v", "h", "help"}

	for _, f := range order {
		if usedFlags[f] {
			fmt.Fprintf(os.Stderr, "  -%s\n      %s\n\n", f, flagInfo[f])
		}
	}

	fmt.Fprintf(os.Stderr, "Run 'dalec -help' for complete information\n\n")
}
