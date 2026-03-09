package github

import (
	"bytes"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/repository"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GetGeneratorPath returns the absolute path to the generator directory
func GetGeneratorPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	generatorPath := filepath.Join(cwd, "generator")
	if info, err := os.Stat(generatorPath); err == nil && info.IsDir() {
		absPath, _ := filepath.Abs(generatorPath)
		return absPath, nil
	}

	if info, err := os.Stat(filepath.Join(cwd, "main.go")); err == nil && !info.IsDir() {
		if _, err := os.Stat(filepath.Join(cwd, "cli")); err == nil {
			absPath, _ := filepath.Abs(cwd)
			return absPath, nil
		}
	}

	parentPath := filepath.Join(cwd, "..", "generator")
	if info, err := os.Stat(parentPath); err == nil && info.IsDir() {
		absPath, _ := filepath.Abs(parentPath)
		return absPath, nil
	}

	return "", fmt.Errorf("generator directory not found (cwd: %s)", cwd)
}

// ExtractRepositorySegments extracts the repository segments from a GitHub URL or path
func ExtractRepositorySegments(repo string) (owner, name, branch string) {
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")

	parts := strings.Split(repo, "/")
	if len(parts) == 2 {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s", parts[0], parts[1])
		repoData, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
		if err != nil {
			log.Printf("Error: failed to fetch repo info for %s: %v\n", repo, err)
			os.Exit(1)
		}

		branch := "main"
		if defaultBranch, ok := repoData["default_branch"].(string); ok && defaultBranch != "" {
			branch = defaultBranch
		}

		return parts[0], parts[1], branch
	} else if len(parts) >= 4 && parts[2] == "tree" {
		return parts[0], parts[1], parts[3]
	} 

	log.Printf("Warning: unrecognized repository format: %s\n", repo)
	os.Exit(1)
	return "", "", ""
}

// MakeGitHubRequest makes an authenticated HTTP request to the GitHub API.
func MakeGitHubRequest[T any](request repository.GithubRequest) (T, error) {
	var result T

	// Build request body for methods that need one
	var bodyReader io.Reader
	if request.Payload != nil && (request.Method == repository.POST || request.Method == repository.PUT) {
		jsonBody, err := json.Marshal(request.Payload)
		if err != nil {
			return result, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	method := request.Method
	if method == "" {
		method = repository.GET
	}

	req, err := http.NewRequest(string(method), request.URL, bodyReader)
	if err != nil {
		return result, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	githubToken := os.Getenv("GH_TOKEN")
	req.Header.Set("Authorization", "token "+githubToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

// FetchRawContent fetches raw content from a URL (e.g. raw.githubusercontent.com)
func FetchRawContent(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	githubToken := os.Getenv("GH_TOKEN")
	if githubToken != "" {
		req.Header.Set("Authorization", "token "+githubToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// Helper function to clean ldflags
func cleanLdFlags(ldflags string) string {
	// Only clean quotes, preserve all env variables including ${VERSION}
	cleaned := strings.Trim(ldflags, `"'`)
	return cleaned
}

// cleanedCache retains the last cleaned LdFlags value so the BuildCommand
// case can substitute ${LDFLAGS} references in the build command.
var cleanedCache = llm.CleanedValuesCache{}

func ClearEnvVariables(key string, command *string) {
	if command == nil || *command == "" {
		return
	}

	switch key {
	case "LdFlags":
		cleanedCache.LdFlags = cleanLdFlags(*command)
		*command = cleanedCache.LdFlags

	case "BuildCommand":
		ldFlagsVarPattern := regexp.MustCompile(`\$\{LDFLAGS\}`)
		*command = ldFlagsVarPattern.ReplaceAllString(*command, cleanedCache.LdFlags)

		// Remove prebuilt runtime/OS env assignments that Dalec handles natively
		removeEnvs := map[string]bool{
			"CGO_ENABLED": true,
			"GOOS":        true,
			"GOARCH":      true,
			"GOARM":       true,
			"GOARM64":     true,
			"OS":          true,
			"ARCH":        true,
			"CC":          true, // set globally in build.env via MinGW toolchain source
		}
		// Match patterns like KEY=value, KEY=${VAR}, KEY=${VAR:-default}, KEY=$(VAR)
		envAssignPattern := regexp.MustCompile(`(\w+)=(?:\$[\{\(][^\}\)]*[\}\)]|\S*)`)
		*command = envAssignPattern.ReplaceAllStringFunc(*command, func(match string) string {
			eqIdx := strings.Index(match, "=")
			if eqIdx > 0 {
				varName := match[:eqIdx]
				if removeEnvs[varName] {
					return ""
				}
			}
			return match
		})
		// Clean up leftover extra whitespace
		spacePattern := regexp.MustCompile(`\s{2,}`)
		*command = strings.TrimSpace(spacePattern.ReplaceAllString(*command, " "))

		// Remove stray braces left behind from env var removal (e.g. "} }" from "${GOARMSUFFIX:+v${GOARM}}")
		strayBraces := regexp.MustCompile(`[{}]`)
		// Only remove braces that are NOT part of a valid ${...} reference
		// Temporarily protect valid ${...} patterns, strip remaining braces, restore
		validVarRef := regexp.MustCompile(`\$\{[^}]+\}`)
		placeholders := map[string]string{}
		idx := 0
		cleaned := validVarRef.ReplaceAllStringFunc(*command, func(match string) string {
			key := fmt.Sprintf("__VARREF_%d__", idx)
			placeholders[key] = match
			idx++
			return key
		})
		cleaned = strayBraces.ReplaceAllString(cleaned, "")
		for key, val := range placeholders {
			cleaned = strings.ReplaceAll(cleaned, key, val)
		}
		// Final whitespace cleanup after brace removal
		cleaned = regexp.MustCompile(`\s{2,}`).ReplaceAllString(cleaned, " ")
		// Collapse double slashes in paths (e.g. bin//kubelogin -> bin/kubelogin)
		cleaned = regexp.MustCompile(`/{2,}`).ReplaceAllString(cleaned, "/")
		*command = strings.TrimSpace(cleaned)

	default:
		fmt.Printf("Warning: unrecognized key for ClearEnvVariables: %s\n", key)
		return 
	}
}
