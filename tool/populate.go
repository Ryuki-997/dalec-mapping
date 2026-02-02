package tool

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	skillsDir     = "skills"
	skillFileName = "SKILL.md"
)

// PopulateHandler runs the population step by invoking the SKILL.md agent instructions
func PopulateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract dockerfile paths (optional, defaults to empty)
	dockerfilePaths := extractStringArray(request, "dockerfile_paths")

	// Extract makefile paths (optional, defaults to empty)
	makefilePaths := extractStringArray(request, "makefile_paths")

	log.Printf("Running population for Dockerfiles: %v and Makefiles: %v", dockerfilePaths, makefilePaths)

	// Get the generator path
	generatorPath, err := getGeneratorPath()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to find generator directory: %v", err)), nil
	}

	// Read the SKILL.md file
	skillPath := filepath.Join(generatorPath, skillsDir, "non-deterministic-setup", skillFileName)
	skillContent, err := os.ReadFile(skillPath)
	if err != nil {
		// Try alternate path
		skillPath = filepath.Join("non-deterministic-setup", skillFileName)
		skillContent, err = os.ReadFile(skillPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to read SKILL.md: %v (tried %s)", err, skillPath)), nil
		}
	}

	// Build the agent prompt with SKILL.md
	var agentPrompt strings.Builder

	agentPrompt.WriteString("# Agent Task: Populate Non-Deterministic Fields\n\n")
	agentPrompt.WriteString("Execute the following skill to populate non-deterministic values.\n\n")

	agentPrompt.WriteString("## SKILL.md Instructions\n\n")
	agentPrompt.WriteString(string(skillContent))
	agentPrompt.WriteString("\n\n")

	// Pass the file paths as context for the agent
	agentPrompt.WriteString("## File Paths\n\n")
	agentPrompt.WriteString("dockerfile_paths:\n")
	for _, path := range dockerfilePaths {
		agentPrompt.WriteString(fmt.Sprintf("  - %s\n", path))
	}
	agentPrompt.WriteString("\nmakefile_paths:\n")
	for _, path := range makefilePaths {
		agentPrompt.WriteString(fmt.Sprintf("  - %s\n", path))
	}

	return mcp.NewToolResultText(agentPrompt.String()), nil
}

// extractStringArray safely extracts a string array from the request arguments
func extractStringArray(request mcp.CallToolRequest, key string) []string {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return []string{}
	}

	val, exists := args[key]
	if !exists || val == nil {
		return []string{}
	}

	switch v := val.(type) {
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	case []string:
		return v
	default:
		return []string{}
	}
}