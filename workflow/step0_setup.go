// ═══════════════════════════════════════════════════════════════════════════════
// Step 0 — Setup
//
//   Parses CLI flags, loads environment, and runs the initial onboard/tag
//   resolution steps to produce the set of actionable (component, tag) states.
//
//   Functions are ordered by call sequence:
//     ParseFlags()
//     LoadEnv()
//     RunFetchOnboard()
//     RunResolveTagCache()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"flag"
	"fmt"
	"log"
	"os"

	"dalec-mapping/pipeline"
	"dalec-mapping/utils"

	"github.com/joho/godotenv"
)

// ForcePR skips the existing-PR check in CreatePR when true.
var ForcePR bool

// ParseFlags registers and parses CLI flags, returning the resolved values.
func ParseFlags() (string, bool) {
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. containernetworking and containernetworking/azure-cns both work). Omit to fetch all under specs/")
	patchMode := flag.Bool("patch", false, "Run patching workflow: fetch MCR images and scan for vulnerabilities")
	force := flag.Bool("force", false, "Force create a PR regardless of whether one already exists")
	branch := flag.String("branch", "", "Override the onboard branch (defaults to utils.OnboardBranch)")
	flag.Parse()
	ForcePR = *force
	if *branch != "" {
		utils.OnboardBranch = *branch
	}
	return *inputPath, *patchMode
}

// LoadEnv loads environment variables from a .env file and validates required
// tokens are present.
func LoadEnv() {
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)

	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found: %v", err)
	}

	if tok := os.Getenv("GH_TOKEN"); tok == "" {
		log.Printf("⚠️  GH_TOKEN is not set — GitHub API calls will be unauthenticated")
	}
}

// RunFetchOnboard fetches onboard configs and returns the component queue and
// existing spec paths. Fatals on error or empty results.
func RunFetchOnboard(inputPath string) ([]pipeline.State, map[string]bool) {
	log.Println("═══ Step 1: Fetch Onboard Files ═══")
	log.Println("Purpose: Fetching onboard configs and separating into component queue")
	log.Printf("Input path: %s", inputPath)

	states, existingPaths, err := FetchOnboardStates(inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(states) == 0 {
		log.Fatalf("❌ Potentially No onboarding files found at path: %s", inputPath)
	}

	log.Printf("Component queue (%d components):", len(states))
	for i, state := range states {
		log.Printf("  [%d] %-20s repo=%s", i+1, state.Onboard.SpecImageName, state.Onboard.Repository)
	}
	log.Println()

	return states, existingPaths
}

// RunResolveTagCache builds the global tag-to-commit cache and resolves
// actionable tags per component. Fatals on error or empty results.
func RunResolveTagCache(componentStates []pipeline.State, existingPaths map[string]bool) []pipeline.State {
	log.Println("═══ Step 2: Resolve Tag Cache ═══")
	log.Println("Purpose: Building global tag-to-commit cache and resolving actionable tags per component")

	states, err := ResolveTagCache(componentStates, existingPaths)
	if err != nil {
		log.Fatalf("❌ Failed to resolve tag cache: %v", err)
	}

	if len(states) == 0 {
		log.Fatalf("❌ No actionable tags found for any component")
	}

	tagsByComponent := make(map[string][]string)
	for _, state := range states {
		name := state.Onboard.SpecImageName
		tagsByComponent[name] = append(tagsByComponent[name], fmt.Sprintf("%s (R%d)", state.Tag.Stripped, state.Tag.Revision))
	}
	log.Printf("Tag cache (%d tags across %d components):", len(states), len(tagsByComponent))
	for component, tags := range tagsByComponent {
		log.Printf("  %s:", component)
		for _, tag := range tags {
			log.Printf("    %s", tag)
		}
	}
	log.Println()

	return states
}
