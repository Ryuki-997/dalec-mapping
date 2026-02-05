package tool

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// Generate runs the generation step to create dalec specs
func Generate(ctx context.Context, repo string) (string, error) {
	log.Printf("Running generation for repo: %s", repo)

	generatorPath, err := getGeneratorPath()
	if err != nil {
		return "", fmt.Errorf("failed to find generator directory: %w", err)
	}

	// Run the generator CLI in generate mode
	cmd := exec.CommandContext(ctx, "go", "run", "main.go", "-repo", repo)
	cmd.Dir = generatorPath
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("generation failed: %w\nOutput: %s", err, string(output))
	}

	// Read the generated dalec spec
	repoName := extractRepoName(repo)
	resultPath := filepath.Join(generatorPath, "..", "result", repoName)
	specYml := filepath.Join(resultPath, "output.yml")
	content, err := os.ReadFile(specYml)
	if err != nil {
		return "", fmt.Errorf("failed to read output.yml: %w", err)
	}

	return string(content), nil
}