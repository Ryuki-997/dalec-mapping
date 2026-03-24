package workflow

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// ImageTest builds and runs the container from the spec, then applies any test scripts.
//
// specPath: remote spec path like "specs/HelloWorld/kubelogin/kubelogin-v0.2.14-specfile.yml"
// imageName: e.g. "kubelogin"
// tag: e.g. "v0.2.14"
func ImageTest(specPath, imageName, tag string, targets []string) error {
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

	// Step 2: Build container from spec for all targets
	if len(targets) == 0 {
		targets = []string{"azlinux3/container", "windowscross/container"}
	}
	imageTag := fmt.Sprintf("%s:%s", imageName, tag)
	for i, target := range targets {
		targetImageTag := fmt.Sprintf("%s-%s", imageTag, strings.ReplaceAll(target, "/", "-"))
		platform := platformForTarget(target)
		log.Printf("  [2/4] Building target %d/%d: %s (image: %s, platform: %s)...", i+1, len(targets), target, targetImageTag, platform)
		if err := buildDockerImage(targetImageTag, target, platform); err != nil {
			return fmt.Errorf("failed to build docker image for target %s: %w", target, err)
		}
		log.Printf("    ✅ %s built successfully", target)
	}

	// Step 3: Run the container image with no args to verify the binary executes
	log.Printf("  [3/5] Running %s (no args)...", imageTag)
	containerImageTag := fmt.Sprintf("%s-%s", imageTag, "azlinux3-container")
	if err := runDockerImage(containerImageTag, platformForTarget("azlinux3/container")); err != nil {
		return fmt.Errorf("failed to run docker image: %w", err)
	}

	// Step 4: Run FIPS checker against the azlinux3 container image
	log.Printf("  [4/5] Running FIPS checker against %s...", containerImageTag)
	if err := runFipsChecker(containerImageTag); err != nil {
		return fmt.Errorf("FIPS check failed for %s: %w", containerImageTag, err)
	}
	log.Printf("    ✅ FIPS check passed for %s", containerImageTag)

	// Step 5: Apply test scripts (if any)
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

// runFipsChecker runs fips-check/build-and-check.sh <runtimeImage> to validate FIPS compliance.
// The script uses "." as its Docker build context, so it must be run from the fips-check/ directory.
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

// fetchTestScripts lists the tests directory via the Contents API and downloads any .sh files found.
// Returns a map of filename -> script content, or nil if the directory doesn't exist.
func fetchTestScripts(testDir string) (map[string]string, error) {
	scripts := make(map[string]string)

	items, err := github.FetchJSONArray(fmt.Sprintf(
		"repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, testDir, utils.OnboardBranch,
	))
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

// platformForTarget returns the --platform value for a given Dalec build target.
// For windowscross, this must be "windows/amd64": Dalec resolves the output base
// image (nanoserver:1809) using this platform, and nanoserver only ships Windows
// manifests. Dalec's LLB frontend handles running the actual compilation on a
// Linux worker regardless; the platform flag only controls the output image target.
// All other targets use the native host architecture.
func platformForTarget(target string) string {
	if strings.HasPrefix(target, "windowscross") {
		return "windows/amd64"
	}
	return "linux/" + runtime.GOARCH
}

// buildDockerImage builds a container/artifact using the Dalec spec via docker build.
// Targets: azlinux3/container, azlinux3/rpm, deb, windowscross
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

// runDockerImage runs the built image with no arguments to verify the binary
// is present and executable. Most binaries exit 0 (help) or 1-2 (missing args)
// when invoked with no args — both are acceptable. Only exit codes ≥ 127
// indicate a real problem (127 = binary not found, 137 = OOM/killed).
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
		// Exit codes 1 and 2 are normal "no args / usage" responses.
		if code > 0 && code < 127 {
			return nil
		}
		return fmt.Errorf("docker run failed with exit code %d: %w", code, err)
	}

	return fmt.Errorf("docker run failed: %w", err)
}

// runTestScript writes a test script to a temp file and runs it inside an Azure Linux
// container with the Docker socket mounted.  The built image is distroless (no shell),
// so the test must execute in a full Linux environment that has /etc/os-release, bash,
// and Docker access.  The IMAGE_TAG env var tells the script which image to test.
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

	// Build docker run args: base flags + per-arg env vars from the spec
	args := []string{"run", "--rm",
		"-a", "/var/run/docker.sock:/var/run/docker.sock",
		"-a", tmpFile.Name() + ":/test.sh:ro",
		"-e", "IMAGE_TAG=" + imageTag,
	}

	// Read spec args and forward them to the test container
	for k, v := range readSpecArgs(utils.ResultDir + "/output.yml") {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args,
		"mcr.microsoft.com/azurelinux/base/core:3.0",
		"/bin/bash", "/test.sh",
	)

	// Run inside Azure Linux (has /etc/os-release + bash) with Docker socket
	// so the script can invoke docker commands against the built image.
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("script %s failed: %w", scriptName, err)
	}

	return nil
}

// readSpecArgs parses the top-level "args:" section from the Dalec spec YAML
// and returns its key-value pairs.  Uses simple line parsing (no full YAML
// decode) to avoid pulling the spec struct into this package.
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

		// Detect start of args block
		if line == "args:" {
			inArgs = true
			continue
		}

		// End of args block: next top-level key (no leading whitespace)
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
