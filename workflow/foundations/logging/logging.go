package logging

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/workplan"
)

// InitInfo carries the facts emitted under the Initialization banner.
// Populated by phase0_cli (ParseFlags + LoadEnv) and rendered once at the
// start of main.
type InitInfo struct {
	WorkingDir     string
	OnboardPath    string // -path flag value, "<all under specs/>" when empty
	SpecBranch     string // config.OnboardBranch
	PublishEnabled bool
	GHTokenPresent bool
	EnvLoaded      bool
}

// ActionEntry records what happened to a single component for the action log.
type ActionEntry struct {
	Component string
	Version   string
	Action    string
}

// PrintInitBanner emits the ═══ Initialization ═══ chunk at the very top of
// a pipeline run. Every fact is sourced from the supplied InitInfo so this
// function has no I/O of its own.
func PrintInitBanner(info InitInfo) {
	publish := "disabled (-no-publish)"
	if info.PublishEnabled {
		publish = "enabled"
	}
	gh := "missing (unauthenticated)"
	if info.GHTokenPresent {
		gh = "present"
	}
	env := "not found"
	if info.EnvLoaded {
		env = "loaded"
	}
	onboard := info.OnboardPath
	if onboard == "" {
		onboard = "<all under specs/>"
	}

	log.Println("═══ Initialization ═══")
	log.Printf("  Working directory:   %s", info.WorkingDir)
	log.Printf("  Onboard search path: %s", onboard)
	log.Printf("  Target spec branch:  %s", info.SpecBranch)
	log.Printf("  Publish mode:        %s", publish)
	log.Printf("  GH_TOKEN:            %s", gh)
	log.Printf("  .env:                %s", env)
}

// PrintFinalizationBanner emits the ═══ Finalization ═══ chunk header. The
// action log, test results, and PR summary follow as indented sub-sections.
func PrintFinalizationBanner() {
	log.Println("═══ Finalization ═══")
}

// PrintActionLog renders the per-component action roll-up as a sub-section
// under the Finalization banner. No banner box, no surrounding blank lines.
func PrintActionLog(entries []ActionEntry) {
	log.Println("  Action Log:")
	for _, entry := range entries {
		log.Printf("    %-12s %s @ %s", entry.Action, entry.Component, entry.Version)
	}
}

// PrintComponentBanner prints a prominent box banner for a component being processed.
func PrintComponentBanner(component *workplan.WorkComponent) {
	label := fmt.Sprintf("  %s @ %s", component.Naming.SpecImageName, component.Tag.Stripped)
	width := len(label) + 4
	if width < 60 {
		width = 60
	}
	border := strings.Repeat("═", width)

	log.Println("╔" + border + "╗")
	log.Printf("║  %-*s  ║", width-4, strings.TrimLeft(label, " "))
	log.Println("╚" + border + "╝")
}
