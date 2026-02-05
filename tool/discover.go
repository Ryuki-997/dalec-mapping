package tool

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

type FilePathResult struct {
	Dockerfiles []string `yaml:"dockerfiles"`
	Makefiles   []string `yaml:"makefiles"`
}

// Discover runs the discovery step to find all Dockerfiles and Makefiles
func Discover(ctx context.Context, repo string) (string, error) {
	log.Printf("Running discovery for repo: %s", repo)

	generatorPath, err := getGeneratorPath()
	if err != nil {
		return "", fmt.Errorf("failed to find generator directory: %w", err)
	}

	log.Printf("Generator path: %s", generatorPath)

	// Run the generator CLI in discover mode
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-repo", repo, "--discover")
	cmd.Dir = generatorPath
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))

	log.Printf("Running command: go run . -repo %s --discover (in %s)", repo, generatorPath)

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("discovery failed: %w", err)
	}

	// Read the generated filepath.yml
	repoName := extractRepoName(repo)
	resultPath := filepath.Join(generatorPath, "..", "result", repoName)
	filepathYml := filepath.Join(resultPath, "filepath.yml")

	log.Printf("Looking for filepath.yml at: %s", filepathYml)

	content, err := os.ReadFile(filepathYml)
	if err != nil {
		return "", fmt.Errorf("failed to read filepath.yml at %s: %w", filepathYml, err)
	}

	return string(content), nil
}

