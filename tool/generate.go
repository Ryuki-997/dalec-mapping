package tool

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
)

// GenerateHandler runs the generation step to create the Dalec spec
func GenerateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	log.Printf("Running generation for repo: %s", repo)
	
	generatorPath, err := getGeneratorPath()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to find generator directory: %v", err)), nil
	}

	repoName := extractRepoName(repo)
	resultPath := filepath.Join(generatorPath, "result", repoName)

	dockerfilePath := filepath.Join(resultPath, "Dockerfile")
	makefilePath := filepath.Join(resultPath, "Makefile")
	outputPath := filepath.Join(resultPath, fmt.Sprintf("%s.yml", repoName))

	// Run the generator CLI in generate mode
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-repo", repo, "-dockerfile", dockerfilePath, "-makefile", makefilePath, "-output", outputPath)
	cmd.Dir = generatorPath
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Generation failed: %v\nOutput: %s", err, string(output))), nil
	}

	// Read the generated spec
	specContent, err := os.ReadFile(outputPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to read generated spec at %s: %v", outputPath, err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Generation complete!\n\nOutput: %s\n\n```yaml\n%s\n```", outputPath, string(specContent))), nil
}