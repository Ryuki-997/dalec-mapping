package workflow

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// ImageTest builds and runs the container from the spec, then applies any test scripts.
//
// specPath: remote spec path like "specs/HelloWorld/kubelogin/kubelogin-v0.2.14-specfile.yml"
// imageName: e.g. "kubelogin"
// tag: e.g. "v0.2.14"
func ImageTest(specPath, imageName, tag string) error {
	log.Println("\n=== Step 6: Image Test ===")

	// Derive testDir: tests live alongside the spec under the same directory
	// With specRepository:    specs/HelloWorld/kubelogin/kubelogin-v0.2.14-specfile.yml -> specs/HelloWorld/kubelogin/tests
	// Without specRepository: specs/kubelogin/kubelogin-v0.2.14-specfile.yml            -> specs/kubelogin/tests
	path := strings.Split(specPath, "/")
	path[len(path)-1] = "tests"
	testDir := strings.Join(path, "/")

	// Fetch test shell scripts from remote
	shellScripts, err := fetchTestScripts(testDir)
	if err != nil {
		log.Printf("⚠️  Could not fetch test scripts from %s: %v", testDir, err)
		// Non-fatal: we still run basic steps 1-3
	}

	// Step 1: Clear all existing docker images for this image name
	log.Println("  [1/4] Clearing existing docker images...")
	if err := clearDockerImages(); err != nil {
		return fmt.Errorf("failed to clear docker images: %w", err)
	}

	// Step 2: Build container from spec
	imageTag := fmt.Sprintf("%s:%s", imageName, tag)
	log.Printf("  [2/4] Building container image %s...", imageTag)
	if err := buildDockerImage(imageTag); err != nil {
		return fmt.Errorf("failed to build docker image: %w", err)
	}

	// Step 3: Run the image with --version
	log.Printf("  [3/4] Running %s --version...", imageTag)
	if err := runDockerImage(imageTag); err != nil {
		return fmt.Errorf("failed to run docker image: %w", err)
	}

	// Step 4: Apply test scripts (if any)
	if len(shellScripts) == 0 {
		log.Println("  [4/4] No test scripts found, skipping.")
		return nil
	}

	log.Printf("  [4/4] Running %d test script(s)...", len(shellScripts))
	for name, content := range shellScripts {
		if err := runTestScript(imageTag, name, content); err != nil {
			return fmt.Errorf("test script %s failed: %w", name, err)
		}
		log.Printf("    ✅ %s passed", name)
	}

	log.Println("  ✅ All image tests passed")
	return nil
}

// fetchTestScripts lists the tests directory via the Contents API and downloads any .sh files found.
// Returns a map of filename -> script content, or nil if the directory doesn't exist.
func fetchTestScripts(testDir string) (map[string]string, error) {
	scripts := make(map[string]string)

	dirURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		targetOwner, targetRepo, testDir, targetBranch,
	)

	items, err := github.MakeGitHubRequest[[]map[string]interface{}](repository.GithubRequest{URL: dirURL})
	if err != nil {
		return nil, err // directory likely doesn't exist
	}

	for _, item := range items {
		name, _ := item["name"].(string)
		if !strings.HasSuffix(name, ".sh") {
			continue
		}

		downloadURL, _ := item["download_url"].(string)
		if downloadURL == "" {
			continue
		}

		raw, err := github.FetchRawContent(downloadURL)
		if err != nil {
			log.Printf("⚠️  Failed to fetch test script %s: %v", name, err)
			continue
		}

		scripts[name] = string(raw)
	}

	return scripts, nil
}

// clearDockerImages does a full docker system prune to ensure a clean slate.
func clearDockerImages() error {
	cmd := exec.Command("docker", "system", "prune", "-a", "--volumes", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker system prune failed: %w", err)
	}

	log.Println("    Pruned all docker images, containers, and volumes")
	return nil
}

// buildDockerImage builds a container using the Dalec spec via docker build.
// Currently base test only runs on azlinux3/container target
func buildDockerImage(imageTag string) error {
	cmd := exec.Command("docker", "build",
		"-t", imageTag,
		"-f", utils.ResultDir+"/output.yml",
		"--target", "azlinux3/container",
		".",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	return nil
}

// runDockerImage runs the built image with --version.
func runDockerImage(imageTag string) error {
	cmd := exec.Command("docker", "run", "--rm", imageTag, "--version")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run failed: %w", err)
	}

	return nil
}

// runTestScript writes a test script to a temp file and executes it against the image.
func runTestScript(imageTag, scriptName, scriptContent string) error {
	tmpFile, err := os.CreateTemp("", "dalec-test-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp script: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(scriptContent); err != nil {
		return fmt.Errorf("failed to write temp script: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		return fmt.Errorf("failed to chmod temp script: %w", err)
	}

	cmd := exec.Command("docker", "run", "--rm",
		"-v", tmpFile.Name()+":/test.sh:ro",
		imageTag,
		"/bin/sh", "/test.sh",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s failed: %w", scriptName, err)
	}

	return nil
}
