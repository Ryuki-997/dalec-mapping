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
func ParseDockerfile(dockerfile []byte, info *contents.DockerfileInfo) (*contents.DockerfileInfo, error) {
	// Create a temporary file to store the Dockerfile content
	tmpFile, err := os.CreateTemp("", "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write the dockerfile content to the temporary file
	if _, err := tmpFile.Write(dockerfile); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %w", err)
	}

	// Reset file pointer to the beginning
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("failed to seek in temporary file: %w", err)
	}

	// Docker buildkit parses the entire Dockerfile and returns an AST
	result, err := parser.Parse(tmpFile)
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
			// Preserve a valued global ARG when a stage re-declares without a default.
			// Docker inherits the global default in this case (ARG FOO inside a stage).
			if value != "" || info.Args[key] == "" {
				info.Args[key] = value
			}
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

func PrintDockerfileInfo(defaultSpec *contents.DefaultSpec) {
	fmt.Println("Parsed Dockerfile Stages:")
	fmt.Println("")

	for _, stage := range defaultSpec.Stages {
		fmt.Printf("Stage: %s (From: %s)\n", stage.Name, stage.From)
		fmt.Println("  Env:")
		for k, v := range stage.Env {
			fmt.Printf("    %s=%s\n", k, v)
		}
		fmt.Println("  Runs:")
		for _, run := range stage.Runs {
			fmt.Printf("    %s\n", run)
		}
		fmt.Println("  Copies:")
		for _, copy := range stage.Copies {
			fmt.Printf("    From: %s, Source: %v, Dest: %s\n", copy.From, copy.Source, copy.Dest)
		}
		fmt.Println("")
	}

	for k, v := range defaultSpec.Args {
		fmt.Printf("Build Arg: %s=%s\n", k, v)
	}
	fmt.Println("")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
