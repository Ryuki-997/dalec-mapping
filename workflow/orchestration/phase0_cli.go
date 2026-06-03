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
	"log"
	"os"

	"dalec-mapping/config"
	"dalec-mapping/workflow/services/specrepo"

	"github.com/joho/godotenv"
)

// ParseFlags registers and parses CLI flags, returning the resolved values.
// It also writes the force-PR flag into specrepo.ForcePR for the publish phase.
func ParseFlags() (inputPath string, patchMode bool, noPublish bool) {
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
	return *inputPathFlag, *patchModeFlag, *noPublishFlag
}

// LoadEnv loads environment variables from a .env file and validates required
// tokens are present.
func LoadEnv() {
	workingDir, _ := os.Getwd()
	log.Printf("Working directory: %s", workingDir)

	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found: %v", err)
	}

	if token := os.Getenv("GH_TOKEN"); token == "" {
		log.Printf("⚠️  GH_TOKEN is not set — GitHub API calls will be unauthenticated")
	}
}
