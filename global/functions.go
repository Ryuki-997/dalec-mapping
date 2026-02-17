package global

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
func ExtractRepositorySegments(repo string) (owner, name, branch, subdir string) {
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")

	parts := strings.Split(repo, "/")
	if len(parts) == 2 {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches", parts[0], parts[1])
		branches, err := MakeGitHubRequest[[]map[string]interface{}](GithubRequest{URL: url})
		if err != nil {
			log.Printf("Error: failed to fetch branches for %s: %v\n", repo, err)
			os.Exit(1)
		}

		branch := "main"
		
		for _, branch := range branches {
			switch branch["name"].(string) {
			case "main", "master":
				branch, ok := branch["name"].(string)
				if ok {
					return parts[0], parts[1], branch, ""
				}
			}
		}

		return parts[0], parts[1], branch, ""
	} else if len(parts) >= 4 && parts[2] == "tree" {
		return parts[0], parts[1], parts[3], strings.Join(parts[4:], "/")
	} 

	log.Printf("Warning: unrecognized repository format: %s\n", repo)
	os.Exit(1)
	return "", "", "", ""
}

// MakeGitHubRequest makes an authenticated HTTP request to the GitHub API.
func MakeGitHubRequest[T any](request GithubRequest) (T, error) {
	var result T

	// Build request body for methods that need one
	var bodyReader io.Reader
	if request.Payload != nil && (request.Method == POST || request.Method == PUT) {
		jsonBody, err := json.Marshal(request.Payload)
		if err != nil {
			return result, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	method := request.Method
	if method == "" {
		method = GET
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

func ClearEnvVariables(key string, command *string) {
	if command == nil || *command == "" {
		return
	}

	removeFlags := map[string]string{
		"'":              "\"",
		"`":              "\"",
		"CGO_ENABLED=0 ": "",
		"CGO_ENABLED=1 ": "",
		"GOOS=linux ":    "",
		"GOARCH=amd64 ":  "",
	}
  
	for old, new := range removeFlags {
		*command = strings.ReplaceAll(*command, old, new)
	}
}
