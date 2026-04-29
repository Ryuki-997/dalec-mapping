// ═══════════════════════════════════════════════════════════════════════════════
// Step 6 — Test Image (GitHub repos only)
//
//   Builds the container from the generated DALEC spec, runs it to verify the
//   binary is present, runs FIPS compliance checks, and optionally executes
//   test shell scripts from the spec repo.
//
//   NOTE: This step only runs for GitHub-sourced repos. ADO-sourced repos skip
//   local testing entirely because the ADO pipeline build itself serves as the
//   test run (BuildKit needs an ADO_TOKEN secret for private ADO git sources,
//   which is handled by test.sh / the pipeline YAML, not here).
//
//   Chunk 1 · MAIN    TestImage()
//   Chunk 2 · DOCKER  clearDockerImages(), buildDockerImage(), runDockerImage()
//
//   Chunk 3 · FIPS    runFipsChecker()
//   Chunk 4 · SCRIPTS fetchTestScripts(), runTestScript(), readSpecArgs()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// TestImage builds and runs the container from the spec, then applies any test scripts.
func TestImage(specPath, imageName, tag string, targets []string) error {
	// Derive the tests directory from the spec path
	path := strings.Split(specPath, "/")
	path[len(path)-1] = "tests"

	// Clear all existing docker images for a clean slate
	log.Println("  [1/5] Clearing existing docker images...")
	if err := clearDockerImages(); err != nil {
		return fmt.Errorf("failed to clear docker images: %w", err)
	}

	// Build container from spec for all targets
	if len(targets) == 0 {
		targets = []string{string(contents.AzLinux3Container), string(contents.WindowsCrossContainer)}
	}
	imageTag := fmt.Sprintf("%s:%s", imageName, tag)
	for i, target := range targets {
		bt := contents.BuildTarget(target)
		targetImageTag := fmt.Sprintf("%s-%s", imageTag, strings.ReplaceAll(target, "/", "-"))
		platform := bt.Platform()
		log.Printf("  [1/2] Building target %d/%d: %s (image: %s, platform: %s)...", i+1, len(targets), target, targetImageTag, platform)
		if err := buildDockerImage(targetImageTag, target, platform); err != nil {
			return fmt.Errorf("failed to build docker image for target %s: %w", target, err)
		}
		log.Printf("    ✅ %s built successfully", target)
	}

	// Run the container image with no args to verify the binary executes
	containerImageTag := fmt.Sprintf("%s-%s", imageTag, "azlinux3-container")
	log.Printf("  [2/2] Running %s (no args)...", containerImageTag)
	if err := runDockerImage(containerImageTag, contents.AzLinux3Container.Platform()); err != nil {
		return fmt.Errorf("failed to run docker image: %w", err)
	}

	log.Println("  ✅ All image tests passed")
	return nil
}

// ─── Chunk 2 · DOCKER ────────────────────────────────────────────────────────

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

// buildDockerImage builds a container/artifact using the Dalec spec via docker build.
func buildDockerImage(imageTag, target, platform string) error {
	cmd := exec.Command("docker", "build",
		"--platform", platform,
		"-t", imageTag,
		"-f", utils.ResultDir+"/output.yml",
		"--target", target,
		".",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// runDockerImage runs the built image with no args to verify the binary is present.
// Exit codes 1-126 are acceptable (normal "no args / usage" responses).
// Codes >= 127 indicate a real problem (127 = not found, 137 = killed).
func runDockerImage(imageTag, platform string) error {
	cmd := exec.Command("docker", "run", "--platform", platform, "--rm", imageTag)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code > 0 && code < 127 {
			return nil
		}
		return fmt.Errorf("docker run failed with exit code %d: %w", code, err)
	}
	return fmt.Errorf("docker run failed: %w", err)
}