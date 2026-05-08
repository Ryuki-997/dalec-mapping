package utils

import (
	"fmt"
	"log"
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
func PrintComponentBanner(component, tag string) {
	label := fmt.Sprintf("  %s @ %s", component, tag)
	width := len(label) + 4
	if width < 60 {
		width = 60
	}
	top := "╔" + repeatChar('═', width) + "╗"
	padded := fmt.Sprintf("║  %-*s  ║", width-4, fmt.Sprintf("%s @ %s", component, tag))
	bottom := "╚" + repeatChar('═', width) + "╝"

	log.Println()
	log.Println(top)
	log.Println(padded)
	log.Println(bottom)
}

// repeatChar returns a string of the given rune repeated n times.
func repeatChar(ch rune, count int) string {
	result := make([]rune, count)
	for i := range result {
		result[i] = ch
	}
	return string(result)
}
