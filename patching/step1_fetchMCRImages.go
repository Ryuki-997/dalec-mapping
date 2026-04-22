// ═══════════════════════════════════════════════════════════════════════════════
// Patching Step 1 — Fetch ACR Images
//
//   Lists all repositories in the ACR, filters to those under the expected
//   public/aks/managed-dalec/ prefix, fetches tags via ORAS, validates the
//   first tag, and runs Trivy scans against matching images.
//
//   Chunk 1 · ORAS       OrasReference, Validate(), Exists(), ValidateAndExists()
//   Chunk 2 · ACR        ListACRImages(), FetchAndScanACRImages()
//   Chunk 3 · TRIVY      ScanImage(), scanCmd
// ═══════════════════════════════════════════════════════════════════════════════

package patching

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"dalec-mapping/utils"
)

// ─── Chunk 1 · ORAS ─────────────────────────────────────────────────────────

// OrasReference wraps ORAS registry operations for ACR image validation.
type OrasReference struct {
	Ctx context.Context
}

// authenticateACR is the first step of the patching workflow.
// It uses the federated OIDC token written by the GitHub Actions / Azure DevOps
// pipeline (AZURE_FEDERATED_TOKEN_FILE) to log in as the service principal,
// obtain a short-lived ACR access token, and store it in ACR_ACCESS_TOKEN.
//
// Required env vars (set in pipeline / .env):
//   AZURE_CLIENT_ID           — app registration client ID
//   AZURE_TENANT_ID           — Entra tenant ID
//   AZURE_FEDERATED_TOKEN_FILE — path to OIDC token file (set by pipeline runtime)
//
// The function shells out to `az` CLI which must be available in the runner.
func authenticateACR() error {
	clientID := strings.TrimSpace(os.Getenv("AZURE_CLIENT_ID"))
	tenantID := strings.TrimSpace(os.Getenv("AZURE_TENANT_ID"))
	tokenFile := strings.TrimSpace(os.Getenv("AZURE_FEDERATED_TOKEN_FILE"))

	if clientID == "" || tenantID == "" {
		return fmt.Errorf("missing AZURE_CLIENT_ID or AZURE_TENANT_ID")
	}
	if tokenFile == "" {
		return fmt.Errorf("AZURE_FEDERATED_TOKEN_FILE is not set — this workflow must run in a pipeline with workload identity federation")
	}

	oidcToken, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("failed to read federated token file %s: %w", tokenFile, err)
	}

	log.Printf("🔐 Logging in as SP %s via federated credential\n", clientID)

	loginCmd := exec.Command("az", "login", "--service-principal",
		"--username", clientID,
		"--tenant", tenantID,
		"--federated-token", strings.TrimSpace(string(oidcToken)),
		"--output", "none",
	)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr
	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("az login failed: %w", err)
	}

	// Exchange Azure session for a short-lived ACR access token
	tokenOut, err := exec.Command("az", "acr", "login",
		"--name", ACRBaseURL,
		"--expose-token",
		"--query", "accessToken",
		"-o", "tsv",
	).Output()
	if err != nil {
		return fmt.Errorf("az acr login failed: %w", err)
	}

	acrToken := strings.TrimSpace(string(tokenOut))
	if acrToken == "" {
		return fmt.Errorf("az acr login returned empty token")
	}

	os.Setenv("ACR_ACCESS_TOKEN", acrToken)
	os.Setenv("ACR_USERNAME", "00000000-0000-0000-0000-000000000000")
	log.Printf("✅ ACR token obtained for %s\n", ACRBaseURL)

	// Log out immediately — token is now in env
	_ = exec.Command("az", "logout", "--output", "none").Run()

	return nil
}

// newAuthClient builds an ORAS auth client from ACR_ACCESS_TOKEN in env.
func newAuthClient() (*auth.Client, error) {
	username := strings.TrimSpace(os.Getenv("ACR_USERNAME"))
	if username == "" {
		username = "00000000-0000-0000-0000-000000000000"
	}

	password := strings.TrimSpace(os.Getenv("ACR_ACCESS_TOKEN"))
	if password == "" {
		return nil, fmt.Errorf("ACR_ACCESS_TOKEN is not set — call authenticateACR() first")
	}

	return &auth.Client{
		Credential: auth.StaticCredential(ACRBaseURL, auth.Credential{
			Username: username,
			Password: password,
		}),
	}, nil
}

// Validate checks that the image URL is a well-formed registry reference.
func (ch *OrasReference) Validate(imgURL string) (bool, error) {
	if imgURL == "" {
		return false, fmt.Errorf("image URL is empty")
	}
	if !strings.Contains(imgURL, "/") {
		return false, fmt.Errorf("invalid image reference: %s", imgURL)
	}
	return true, nil
}

// Exists checks whether the image manifest exists in the remote registry.
func (ch *OrasReference) Exists(repo *remote.Repository, imgURL string) (bool, error) {
	ref := imgURL
	if idx := strings.LastIndex(imgURL, ":"); idx > 0 {
		ref = imgURL[idx+1:]
	}
	desc, err := repo.Resolve(ch.Ctx, ref)
	if err != nil {
		return false, nil // not found is not an error
	}
	return desc.Size > 0, nil
}

// ValidateAndExists validates the image reference then checks if it exists.
func (ch *OrasReference) ValidateAndExists(imgURL string) (bool, error) {
	valid, err := ch.Validate(imgURL)
	if err != nil || !valid {
		return false, fmt.Errorf("invalid image reference %s: %w", imgURL, err)
	}

	repo, err := remote.NewRepository(imgURL)
	if err != nil {
		return false, fmt.Errorf("failed to create ORAS repository for %s: %w", imgURL, err)
	}

	authClient, err := newAuthClient()
	if err != nil {
		return false, err
	}
	repo.Client = authClient

	exists, err := ch.Exists(repo, imgURL)
	if err != nil {
		return false, fmt.Errorf("failed to check existence of %s: %w", imgURL, err)
	}

	return exists, nil
}

// ─── Chunk 2 · ACR ──────────────────────────────────────────────────────────

// ACRBaseURL is the base registry for the managed Dalec ACR.
const ACRBaseURL = "testmanageddalecacr.azurecr.io"

// ACRPathPrefix is the path prefix for public images in the ACR.
const ACRPathPrefix = "public/aks/managed-dalec/"

// ACRImage represents a discovered image in the ACR.
type ACRImage struct {
	Repository string   // e.g. "public/aks/managed-dalec/containernetworkingauto/azure-ipam"
	Tags       []string // all tags for this repo
}

// ListACRImages connects to the ACR, lists all repositories, and returns
// those matching the expected prefix (public/aks/managed-dalec/) with their tags.
func ListACRImages(ctx context.Context) ([]ACRImage, error) {
	reg, err := remote.NewRegistry(ACRBaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create registry client for %s: %w", ACRBaseURL, err)
	}

	authClient, err := newAuthClient()
	if err != nil {
		return nil, err
	}
	reg.Client = authClient

	log.Printf("🔍 Listing repositories in %s\n", ACRBaseURL)

	repos, err := registry.Repositories(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("failed to list repositories in %s: %w", ACRBaseURL, err)
	}

	var images []ACRImage
	for _, repoName := range repos {
		if !strings.HasPrefix(repoName, ACRPathPrefix) {
			continue
		}

		log.Printf("  📦 Found matching repo: %s\n", repoName)

		// Fetch tags for this repository
		repoRef, err := remote.NewRepository(ACRBaseURL + "/" + repoName)
		if err != nil {
			log.Printf("⚠️  Failed to open repository %s: %v\n", repoName, err)
			continue
		}
		repoRef.Client = authClient

		tags, err := registry.Tags(ctx, repoRef)
		if err != nil {
			log.Printf("⚠️  Failed to list tags for %s: %v\n", repoName, err)
			continue
		}

		if len(tags) == 0 {
			log.Printf("  ⏭️  No tags found for %s — skipping\n", repoName)
			continue
		}

		images = append(images, ACRImage{
			Repository: repoName,
			Tags:       tags,
		})
	}

	log.Printf("📋 Discovered %d matching repositories\n", len(images))
	return images, nil
}

// FetchAndScanACRImages is the top-level patching entry point.
// Step 0: authenticate to ACR via federated SP credential.
// Step 1: list all repositories under the expected prefix.
// Step 2: validate first tag per repo and run Trivy scan.
// Returns a list of scan result paths.
func FetchAndScanACRImages() ([]string, error) {
	if err := authenticateACR(); err != nil {
		return nil, fmt.Errorf("ACR authentication failed: %w", err)
	}

	ctx := context.Background()
	oras := &OrasReference{Ctx: ctx}

	images, err := ListACRImages(ctx)
	if err != nil {
		return nil, err
	}

	var scanResults []string

	for _, img := range images {
		// Only validate and scan the first tag (public)
		firstTag := img.Tags[0]
		imgURL := fmt.Sprintf("%s/%s:%s", ACRBaseURL, img.Repository, firstTag)

		// Derive a short name from the repo path for logging/output
		shortName := strings.TrimPrefix(img.Repository, ACRPathPrefix)
		shortName = strings.ReplaceAll(shortName, "/", "-")

		log.Printf("🔍 Validating ACR image: %s\n", imgURL)

		exists, err := oras.ValidateAndExists(imgURL)
		if err != nil {
			log.Printf("⚠️  Failed to validate %s: %v\n", imgURL, err)
			continue
		}
		if !exists {
			log.Printf("  ⏭️  Image %s not found — skipping\n", imgURL)
			continue
		}

		log.Printf("  ✅ Image %s exists\n", imgURL)

		// Run trivy scan on the first (public) tag
		outputPath := filepath.Join(utils.ResultDir, fmt.Sprintf("%s-%s-acr-scan.json", shortName, firstTag))
		if err := ScanImage(imgURL, "os,library", outputPath); err != nil {
			log.Printf("⚠️  Trivy scan failed for %s: %v\n", imgURL, err)
			continue
		}

		log.Printf("  📄 Scan results written to %s\n", outputPath)
		scanResults = append(scanResults, outputPath)

		// Silently skip remaining tags
		for _, tag := range img.Tags[1:] {
			log.Printf("  ⏭️  Skipping %s:%s (non-public)\n", img.Repository, tag)
		}
	}

	return scanResults, nil
}

// ─── Chunk 3 · TRIVY ─────────────────────────────────────────────────────────

// ScanCmd is the trivy command template.
// Args: pkg-types, image URL, output path.
const ScanCmd = "image --detection-priority comprehensive --ignore-unfixed --pkg-types '%s' %s -f json -o %s"

// ScanImage runs Trivy against a remote MCR image using docker run.
// pkgTypes specifies which package types to scan (e.g. "os,library").
func ScanImage(imageURL, pkgTypes, outputPath string) error {
	// Ensure the output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}
	outputDir := filepath.Dir(absOutput)
	outputFile := filepath.Base(absOutput)
	username := strings.TrimSpace(os.Getenv("ACR_USERNAME"))
	if username == "" {
		username = "00000000-0000-0000-0000-000000000000"
	}
	password := strings.TrimSpace(os.Getenv("ACR_ACCESS_TOKEN"))
	if password == "" {
		return fmt.Errorf("missing ACR credentials: set ACR_ACCESS_TOKEN")
	}

	cmdArgs := []string{
		"run", "--rm",
		"-v", outputDir+":/output",
		"aquasec/trivy:latest",
		"image",
		"--image-src", "remote", // skip local docker/containerd/podman — pull directly from registry
		"--detection-priority", "comprehensive",
		"--ignore-unfixed",
		"--pkg-types", pkgTypes,
		"--username", username,
		"--password", password,
		imageURL,
		"-f", "json",
		"-o", "/output/"+outputFile,
	}
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Trivy exits non-zero when vulns found — check if output was produced
		if _, statErr := os.Stat(absOutput); statErr == nil {
			log.Printf("  Trivy exited with error but produced output — vulnerabilities found\n")
			return nil
		}
		return fmt.Errorf("trivy scan failed for %s: %w", imageURL, err)
	}

	return nil
}

// ParseScanResults reads a trivy JSON output file and returns vulnerability counts.
func ParseScanResults(path string) (total int, high int, critical int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to read scan results: %w", err)
	}

	var results map[string]interface{}
	if err := json.Unmarshal(data, &results); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse scan results: %w", err)
	}

	resultsArr, ok := results["Results"].([]interface{})
	if !ok {
		return 0, 0, 0, nil
	}

	for _, r := range resultsArr {
		result, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		vulns, ok := result["Vulnerabilities"].([]interface{})
		if !ok {
			continue
		}
		for _, v := range vulns {
			vuln, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			total++
			switch vuln["Severity"] {
			case "HIGH":
				high++
			case "CRITICAL":
				critical++
			}
		}
	}

	return total, high, critical, nil
}
