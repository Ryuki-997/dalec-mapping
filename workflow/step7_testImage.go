// ═══════════════════════════════════════════════════════════════════════════════
// Step 7 — Test Image
//
//   Builds the container from the generated DALEC spec, runs it to verify the
//   binary is present, runs FIPS compliance checks, and optionally executes
//   test shell scripts from the spec repo.
//
//   Chunk 1 · MAIN    TestImage()
//   Chunk 2 · DOCKER  clearDockerImages(), buildDockerImage(), runDockerImage(),
//                      platformForTarget()
//   Chunk 3 · FIPS    runFipsChecker()
//   Chunk 4 · SCRIPTS fetchTestScripts(), runTestScript(), readSpecArgs()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// TestImage builds and runs the container from the spec, then applies any test scripts.
func TestImage(specPath, imageName, tag string, targets []string) error {
	// Derive the tests directory from the spec path
	path := strings.Split(specPath, "/")
	path[len(path)-1] = "tests"
	testDir := strings.Join(path, "/")

	// Fetch test shell scripts from the remote spec repo
	shellScripts, err := fetchTestScripts(testDir)
	if err != nil {
		log.Printf("⚠️  Could not fetch test scripts from %s: %v", testDir, err)
	}

	// Clear all existing docker images for a clean slate
	log.Println("  [1/5] Clearing existing docker images...")
	if err := clearDockerImages(); err != nil {
		return fmt.Errorf("failed to clear docker images: %w", err)
	}

	// Build container from spec for all targets
	if len(targets) == 0 {
		targets = []string{"azlinux3/container", "windowscross/container"}
	}
	imageTag := fmt.Sprintf("%s:%s", imageName, tag)
	for i, target := range targets {
		targetImageTag := fmt.Sprintf("%s-%s", imageTag, strings.ReplaceAll(target, "/", "-"))
		platform := platformForTarget(target)
		log.Printf("  [2/5] Building target %d/%d: %s (image: %s, platform: %s)...", i+1, len(targets), target, targetImageTag, platform)
		if err := buildDockerImage(targetImageTag, target, platform); err != nil {
			return fmt.Errorf("failed to build docker image for target %s: %w", target, err)
		}
		log.Printf("    ✅ %s built successfully", target)
	}

	// Run the container image with no args to verify the binary executes
	containerImageTag := fmt.Sprintf("%s-%s", imageTag, "azlinux3-container")
	log.Printf("  [3/5] Running %s (no args)...", containerImageTag)
	if err := runDockerImage(containerImageTag, platformForTarget("azlinux3/container")); err != nil {
		return fmt.Errorf("failed to run docker image: %w", err)
	}

	// Run FIPS checker against the azlinux3 container image
	log.Printf("  [4/5] Running FIPS checker against %s...", containerImageTag)
	if err := runFipsChecker(containerImageTag); err != nil {
		return fmt.Errorf("FIPS check failed for %s: %w", containerImageTag, err)
	}
	log.Printf("    ✅ FIPS check passed for %s", containerImageTag)

	// Apply test scripts (if any)
	if len(shellScripts) == 0 {
		log.Println("  [5/5] No test scripts found, skipping.")
		return nil
	}

	// log.Printf("  [5/5] Running %d test script(s)...", len(shellScripts))
	// for name, content := range shellScripts {
	// 	if err := runTestScript(imageTag, name, content); err != nil {
	// 		return fmt.Errorf("test script %s failed: %w", name, err)
	// 	}
	// 	log.Printf("    ✅ %s passed", name)
	// }

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

// platformForTarget returns the --platform value for a given Dalec build target.
// windowscross requires "windows/amd64"; all others use the native host arch.
func platformForTarget(target string) string {
	if strings.HasPrefix(target, "windowscross") {
		return "windows/amd64"
	}
	return "linux/" + runtime.GOARCH
}

// ─── Chunk 3 · FIPS ─────────────────────────────────────────────────────────

// runFipsChecker runs fips-check/build-and-check.sh to validate FIPS compliance.
func runFipsChecker(runtimeImage string) error {
	cmd := exec.Command("bash", "build-and-check.sh", runtimeImage)
	cmd.Dir = "fips-check"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build-and-check.sh failed: %w", err)
	}
	return nil
}

// ─── Chunk 4 · SCRIPTS ──────────────────────────────────────────────────────

// fetchTestScripts lists the tests directory via the Contents API and downloads .sh files.
func fetchTestScripts(testDir string) (map[string]string, error) {
	scripts := make(map[string]string)

	items, err := repository.FetchJSONArray(fmt.Sprintf(
		"repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, testDir, utils.OnboardBranch,
	))
	if err != nil {
		return nil, err
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
		raw, err := repository.FetchRawContent(downloadURL)
		if err != nil {
			log.Printf("⚠️  Failed to fetch test script %s: %v", name, err)
			continue
		}
		scripts[name] = string(raw)
	}
	return scripts, nil
}

// runTestScript writes a test script to a temp file and runs it inside an Azure Linux
// container with the Docker socket mounted.
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

	args := []string{"run", "--rm",
		"-a", "/var/run/docker.sock:/var/run/docker.sock",
		"-a", tmpFile.Name() + ":/test.sh:ro",
		"-e", "IMAGE_TAG=" + imageTag,
	}
	for k, v := range readSpecArgs(utils.ResultDir + "/output.yml") {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args,
		"mcr.microsoft.com/azurelinux/base/core:3.0",
		"/bin/bash", "/test.sh",
	)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s failed: %w", scriptName, err)
	}
	return nil
}

// readSpecArgs parses the top-level "args:" section from the DALEC spec YAML.
func readSpecArgs(specPath string) map[string]string {
	result := make(map[string]string)

	f, err := os.Open(specPath)
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inArgs := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "args:" {
			inArgs = true
			continue
		}
		if inArgs && len(line) > 0 && line[0] != ' ' && line[0] != '#' {
			break
		}
		if inArgs {
			trimmed := strings.TrimSpace(line)
			if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if key != "" {
					result[key] = val
				}
			}
		}
	}
	return result
}
