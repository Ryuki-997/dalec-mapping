// ═══════════════════════════════════════════════════════════════════════════════
// CLI — Flag parsing and environment loading
//
//   Front-facing entrypoint helpers used by main.go before the three pipeline
//   phases run. These are not phases themselves; they configure the process
//   for the phases that follow.
// ═══════════════════════════════════════════════════════════════════════════════

package orchestration

import (
	"flag"
	"os"

	"dalec-mapping/config"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/services/specrepo"

	"github.com/joho/godotenv"
)

// CLIFlags captures the resolved command-line flag values.
type CLIFlags struct {
	InputPath string
	PatchMode bool
	NoPublish bool
}

// ParseFlags registers and parses CLI flags, returning the resolved values.
// It also writes the force-PR flag into specrepo.ForcePR for the publish phase
// and overrides config.OnboardBranch when -branch is supplied.
func ParseFlags() CLIFlags {
	inputPathFlag := flag.String("path", "", "Input path to search for onboarding files (e.g. containernetworking and containernetworking/azure-cns both work). Omit to fetch all under specs/")
	patchModeFlag := flag.Bool("patch", false, "Run patching workflow: fetch MCR images and scan for vulnerabilities")
	force := flag.Bool("force", false, "Force create a PR regardless of whether one already exists")
	branch := flag.String("branch", "", "Override the onboard branch (defaults to config.OnboardBranch)")
	noPublishFlag := flag.Bool("no-publish", false, "Skip Phase 3 (PR publishing); run Phase 1 + Phase 2 only")
	flag.Parse()
	specrepo.ForcePR = *force
	if *branch != "" {
		config.OnboardBranch = *branch
	}
	return CLIFlags{
		InputPath: *inputPathFlag,
		PatchMode: *patchModeFlag,
		NoPublish: *noPublishFlag,
	}
}

// LoadEnv loads environment variables from a .env file and gathers the init
// facts (working dir, env-file presence, GH_TOKEN presence, target branch,
// publish mode) into a logging.InitInfo for the init banner. Silent — the
// caller emits the banner once via logging.PrintInitBanner.
func LoadEnv(flags CLIFlags) logging.InitInfo {
	workingDir, _ := os.Getwd()
	envLoaded := godotenv.Load() == nil
	ghTokenPresent := os.Getenv("GH_TOKEN") != ""

	return logging.InitInfo{
		WorkingDir:     workingDir,
		OnboardPath:    flags.InputPath,
		SpecBranch:     config.OnboardBranch,
		PublishEnabled: !flags.NoPublish,
		GHTokenPresent: ghTokenPresent,
		EnvLoaded:      envLoaded,
	}
}
