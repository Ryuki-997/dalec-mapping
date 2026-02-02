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

type FilePathResult struct {
	Dockerfiles []string `yaml:"dockerfiles"`
	Makefiles   []string `yaml:"makefiles"`
}

// DiscoverHandler runs the discovery step to find all Dockerfiles and Makefiles
func DiscoverHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := request.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	log.Printf("Running discovery for repo: %s", repo)

	// Run the generator CLI in discover mode
	cmd := exec.CommandContext(ctx, "go", "run", "main.go", "-repo", repo, "--discover")
	cmd.Dir, err = getGeneratorPath()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to find generator directory: %v", err)), nil
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Discovery failed: %v\nOutput: %s", err, string(output))), nil
	}

	// Read the generated filepath.yml
	repoName := extractRepoName(repo)
	resultPath := filepath.Join(cmd.Dir, "result", repoName)
	filepathYml := filepath.Join(resultPath, "filepath.yml")
	content, err := os.ReadFile(filepathYml)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to read filepath.yml: %v", err)), nil
	}

	return mcp.NewToolResultText(string(content)), nil
}

