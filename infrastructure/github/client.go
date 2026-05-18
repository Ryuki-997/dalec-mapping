package github

// ─── Chunk 2 · HTTP CLIENT ──────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"dalec-mapping/domain/repository"
)

const githubAPIBase = "https://api.github.com"

// GithubReturnType controls how makeGitHubRequest decodes the response body.
type GithubReturnType int

const (
	ReturnJSON      GithubReturnType = iota // → map[string]interface{}
	ReturnJSONArray                         // → []map[string]interface{}
	ReturnRaw                               // → []byte
)

// makeGitHubRequest is the single internal HTTP entry-point.
// All public Fetch* functions delegate here.
func makeGitHubRequest(request repository.GithubRequest, returnType GithubReturnType) (interface{}, error) {
	var bodyReader io.Reader
	if request.Payload != nil && (request.Method == repository.POST || request.Method == repository.PUT || request.Method == repository.PATCH) {
		jsonBody, err := json.Marshal(request.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	method := request.Method
	if method == "" {
		method = repository.GET
	}

	req, err := http.NewRequest(string(method), request.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	if returnType != ReturnRaw {
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	switch returnType {
	case ReturnRaw:
		return body, nil
	case ReturnJSONArray:
		var result []map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON array: %w", err)
		}
		return result, nil
	default: // ReturnJSON
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
		}
		return result, nil
	}
}

// FetchRawContent fetches raw bytes from a URL (e.g. raw.githubusercontent.com).
func FetchRawContent(url string) ([]byte, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: url}, ReturnRaw)
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

// FetchJSON performs an authenticated GET to the GitHub API and returns a JSON object.
// path is relative to api.github.com (e.g. "repos/owner/repo/contents/file").
func FetchJSON(path string) (map[string]interface{}, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: githubAPIBase + "/" + path}, ReturnJSON)
	if err != nil {
		return nil, err
	}
	return result.(map[string]interface{}), nil
}

// FetchJSONArray performs an authenticated GET to the GitHub API and returns a JSON array.
// path is relative to api.github.com.
func FetchJSONArray(path string) ([]map[string]interface{}, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: githubAPIBase + "/" + path}, ReturnJSONArray)
	if err != nil {
		return nil, err
	}
	return result.([]map[string]interface{}), nil
}

// WriteJSON performs a write (PUT/POST) to the GitHub API.
// Returns the response as a JSON object when possible; returns (nil, nil) for
// non-object responses (e.g. the labels API returns an array).
func WriteJSON(path string, method repository.CRUDRequest, payload interface{}) (map[string]interface{}, error) {
	raw, err := makeGitHubRequest(repository.GithubRequest{
		URL:     githubAPIBase + "/" + path,
		Method:  method,
		Payload: payload,
	}, ReturnRaw)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if json.Unmarshal(raw.([]byte), &m) != nil {
		return nil, nil
	}
	return m, nil
}
