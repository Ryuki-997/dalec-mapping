package logging

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/workplan"
)

// ActionEntry records what happened to a single component for the action log.
type ActionEntry struct {
	Component string
	Version   string
	Action    string
}

// PrintActionLog outputs a summary of all component actions taken during the run.
func PrintActionLog(entries []ActionEntry) {
	log.Println()
	log.Println("═══ Action Log ═══")
	for _, entry := range entries {
		log.Printf("  %-12s %s @ %s", entry.Action, entry.Component, entry.Version)
	}
	log.Println()
}

// PrintComponentBanner prints a prominent box banner for a component being processed.
func PrintComponentBanner(item workplan.WorkItem) {
	label := fmt.Sprintf("  %s @ %s", item.Naming.SpecImageName, item.Tag.Stripped)
	width := len(label) + 4
	if width < 60 {
		width = 60
	}
	border := strings.Repeat("═", width)

	log.Println()
	log.Println("╔" + border + "╗")
	log.Printf("║  %-*s  ║", width-4, strings.TrimLeft(label, " "))
	log.Println("╚" + border + "╝")
}
