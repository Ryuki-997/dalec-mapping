package parser

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"os"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

/*
How Buildkit Parser Works:
==========================

Instead of manually parsing Dockerfile syntax, we use buildkit's parser which:
1. Handles all Dockerfile syntax rules (backslashes, quotes, JSON arrays, etc.)
2. Returns an AST (Abstract Syntax Tree)
3. We just walk the tree and extract structured data

The AST has this structure:
- result.AST.Children = array of instruction nodes (FROM, RUN, COPY, etc.)
- Each node has:
  * node.Value = instruction name (e.g., "FROM", "RUN")
  * node.Next = linked list of arguments
  * node.Flags = flags like --platform=, --from=
  * node.Attributes = metadata like whether it's JSON format

Example:
  Dockerfile: FROM --platform=linux/amd64 golang:1.21 AS builder
  Buildkit gives us:
    node.Value = "FROM"
    node.Flags = ["--platform=linux/amd64"]
    node.Next.Value = "golang:1.21"
    node.Next.Next.Value = "AS"
    node.Next.Next.Next.Value = "builder"
*/

// ParseDockerfile uses buildkit parser to parse a Dockerfile
// The buildkit parser handles all the complex parsing for us
func ParseDockerfile(filepath string, info *contents.DockerfileInfo) (*contents.DockerfileInfo, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open Dockerfile: %w", err)
	}
	defer f.Close()

	// Docker buildkit parses the entire Dockerfile and returns an AST
	result, err := parser.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	var currentStage *contents.Stage

	// Walk the AST - each child is a Dockerfile instruction
	for _, node := range result.AST.Children {
		instruction := strings.ToUpper(node.Value)

		// Add raw instruction to current stage if it exists
		if currentStage != nil {
			rawInst := contents.RawInstruction{
				Type:  instruction,
				Args:  []string{},
				Flags: make(map[string]string),
			}

			// Collect arguments
			for n := node.Next; n != nil; n = n.Next {
				rawInst.Args = append(rawInst.Args, n.Value)
			}

			// Collect flags
			if node.Flags == nil {
				continue
			}

			for _, flag := range node.Flags {
				if !strings.Contains(flag, "=") {
					continue
				}
				parts := strings.SplitN(flag, "=", 2)
				key := strings.TrimPrefix(parts[0], "--")
				rawInst.Flags[key] = parts[1]
			}

			currentStage.Instructions = append(currentStage.Instructions, rawInst)
		}

		switch instruction {
		case "FROM":
			currentStage = parseFromInstruction(node)
			info.Stages = append(info.Stages, *currentStage)
			// Update pointer to the stage in the slice
			currentStage = &info.Stages[len(info.Stages)-1]

		case "ARG":
			key, value := parseKeyValue(node.Next)
			info.Args[key] = value
			if currentStage != nil {
				currentStage.Args[key] = value
			}

		case "ENV":
			if currentStage != nil {
				key, value := parseKeyValue(node.Next)
				currentStage.Env[key] = value
			}

		case "WORKDIR":
			if currentStage != nil && node.Next != nil {
				currentStage.Workdir = node.Next.Value
			}

		case "RUN":
			if currentStage != nil {
				// buildkit already parsed the command for us
				cmd := reconstructCommand(node.Next)
				currentStage.Runs = append(currentStage.Runs, cmd)
			}

		case "COPY", "ADD":
			if currentStage != nil {
				copy := parseCopyInstruction(node, instruction)
				currentStage.Copies = append(currentStage.Copies, copy)
			}

		case "ENTRYPOINT":
			if currentStage != nil {
				currentStage.Entrypoint = parseCommandArray(node)
			}

		case "CMD":
			if currentStage != nil {
				currentStage.Cmd = parseCommandArray(node)
			}

		case "EXPOSE":
			if currentStage != nil && node.Next != nil {
				currentStage.Expose = append(currentStage.Expose, node.Next.Value)
			}

		case "LABEL":
			key, value := parseKeyValue(node.Next)
			info.Labels[key] = strings.Trim(value, "\"")
		}
	}

	return info, nil
}

// parseFromInstruction extracts information from a FROM instruction
// Example: FROM --platform=linux/amd64 golang:1.21 AS builder
func parseFromInstruction(node *parser.Node) *contents.Stage {
	stage := &contents.Stage{
		Args:         make(map[string]string),
		Env:          make(map[string]string),
		Copies:       []contents.CopyInstruction{},
		Runs:         []string{},
		Expose:       []string{},
		Instructions: []contents.RawInstruction{},
	}

	// Check for flags (buildkit already parsed them)
	if node.Flags != nil {
		for _, flag := range node.Flags {
			if strings.HasPrefix(flag, "--platform=") {
				stage.Platform = strings.TrimPrefix(flag, "--platform=")
			}
		}
	}

	// Get base image (first argument)
	if node.Next != nil {
		stage.From = node.Next.Value

		// Check for "AS <name>" clause
		n := node.Next.Next
		if n != nil && strings.ToUpper(n.Value) == "AS" && n.Next != nil {
			stage.Name = n.Next.Value
		}
	}

	return stage
}

// parseCopyInstruction extracts COPY/ADD instruction details
// Example: COPY --from=builder /app/bin /usr/local/bin
func parseCopyInstruction(node *parser.Node, instType string) contents.CopyInstruction {
	copy := contents.CopyInstruction{
		Type:   instType,
		Source: []string{},
	}

	// Check for --from flag (buildkit already parsed it)
	if node.Flags != nil {
		for _, flag := range node.Flags {
			if strings.HasPrefix(flag, "--from=") {
				copy.From = strings.TrimPrefix(flag, "--from=")
			}
		}
	}

	// Walk through arguments: all but last are sources, last is dest
	var args []string
	for n := node.Next; n != nil; n = n.Next {
		args = append(args, n.Value)
	}

	if len(args) > 0 {
		copy.Dest = args[len(args)-1]
		copy.Source = args[:len(args)-1]
	}

	return copy
}

// parseCommandArray handles both JSON and shell format commands
// buildkit tells us if it's JSON via node.Attributes["json"]
func parseCommandArray(node *parser.Node) []string {
	// Check if buildkit detected JSON format (e.g., ["cmd", "arg1", "arg2"])
	if node.Attributes != nil && node.Attributes["json"] {
		var result []string
		for n := node.Next; n != nil; n = n.Next {
			result = append(result, n.Value)
		}
		return result
	}

	// Shell format - wrap in shell
	cmd := reconstructCommand(node.Next)
	if cmd != "" {
		return []string{"/bin/sh", "-c", cmd}
	}
	return nil
}

// reconstructCommand joins node values back into a single command string
func reconstructCommand(node *parser.Node) string {
	var parts []string
	for n := node; n != nil; n = n.Next {
		parts = append(parts, n.Value)
	}
	return strings.Join(parts, " ")
}

// parseKeyValue extracts key=value or key value pairs
func parseKeyValue(node *parser.Node) (string, string) {
	if node == nil {
		return "", ""
	}

	fullValue := reconstructCommand(node)

	// Try splitting on =
	if strings.Contains(fullValue, "=") {
		parts := strings.SplitN(fullValue, "=", 2)
		return parts[0], parts[1]
	}

	// Try splitting on space
	parts := strings.SplitN(fullValue, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	return fullValue, ""
}

// PrintDockerfileInfo displays parsed Dockerfile information
func PrintDockerfileInfo(info *contents.DockerfileInfo) {
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║     DOCKERFILE PARSING RESULTS           ║")
	fmt.Println("╚══════════════════════════════════════════╝")

	if len(info.Args) > 0 {
		fmt.Println("📋 Global ARGs:")
		for k, v := range info.Args {
			if v == "" {
				fmt.Printf("   • %s (no default)\n", k)
			} else {
				fmt.Printf("   • %s = %s\n", k, v)
			}
		}
		fmt.Println()
	}

	if len(info.Labels) > 0 {
		fmt.Println("🏷️  Labels:")
		for k, v := range info.Labels {
			fmt.Printf("   • %s = %s\n", k, v)
		}
		fmt.Println()
	}

	fmt.Printf("🏗️  Build Stages: %d\n\n", len(info.Stages))

	for i, stage := range info.Stages {
		stageName := stage.Name
		if stageName == "" {
			stageName = fmt.Sprintf("(unnamed stage %d)", i)
		}

		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Stage %d: %s\n", i, stageName)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("  📦 Base: %s\n", stage.From)

		if stage.Platform != "" {
			fmt.Printf("  🖥️  Platform: %s\n", stage.Platform)
		}

		if stage.Workdir != "" {
			fmt.Printf("  📁 Workdir: %s\n", stage.Workdir)
		}

		if len(stage.Args) > 0 {
			fmt.Println("  📋 ARGs:")
			for k, v := range stage.Args {
				fmt.Printf("     • %s = %s\n", k, v)
			}
		}

		if len(stage.Env) > 0 {
			fmt.Println("  🌍 ENV:")
			for k, v := range stage.Env {
				fmt.Printf("     • %s = %s\n", k, v)
			}
		}

		if len(stage.Runs) > 0 {
			fmt.Printf("  ⚙️  RUN commands: %d\n", len(stage.Runs))
			for _, run := range stage.Runs {
				fmt.Printf("     • %s\n", truncate(run, 70))
			}
		}

		if len(stage.Copies) > 0 {
			fmt.Printf("  📋 COPY/ADD: %d\n", len(stage.Copies))
			for _, copy := range stage.Copies {
				fromInfo := ""
				if copy.From != "" {
					fromInfo = fmt.Sprintf(" (from %s)", copy.From)
				}
				fmt.Printf("     • %s: %v → %s%s\n", copy.Type, copy.Source, copy.Dest, fromInfo)
			}
		}

		if len(stage.Entrypoint) > 0 {
			fmt.Printf("  🚀 Entrypoint: %v\n", stage.Entrypoint)
		}

		if len(stage.Cmd) > 0 {
			fmt.Printf("  💻 Cmd: %v\n", stage.Cmd)
		}

		if len(stage.Expose) > 0 {
			fmt.Printf("  🌐 Expose: %v\n", stage.Expose)
		}

		fmt.Println()
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
