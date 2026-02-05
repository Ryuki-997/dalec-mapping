package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const skillPath = "skills/non-deterministic-setup/SKILL.md"

type ChatRequest struct {
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Model       string        `json:"model,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

type FilePathYml struct {
	Dockerfiles []string `yaml:"dockerfiles"`
	Makefiles   []string `yaml:"makefiles"`
}

// Populate analyzes dockerfiles and makefiles using LLM to extract non-deterministic values
func Populate(ctx context.Context, repo string) error {
	log.Printf("Running populate for repo: %s", repo)

	// Get paths
	generatorPath, err := getGeneratorPath()
	if err != nil {
		return fmt.Errorf("failed to find generator directory: %w", err)
	}

	repoName := extractRepoName(repo)
	resultPath := filepath.Join(generatorPath, "..", "result", repoName)

	// Read skill.md
	skillFullPath := filepath.Join(generatorPath, "..", skillPath)
	skillContent, err := os.ReadFile(skillFullPath)
	if err != nil {
		return fmt.Errorf("failed to read SKILL.md at %s: %w", skillFullPath, err)
	}
	log.Printf("Loaded skill from: %s", skillFullPath)

	// Read filepath.yml to get dockerfile and makefile paths
	filepathYml := filepath.Join(resultPath, "filepath.yml")
	filepathContent, err := os.ReadFile(filepathYml)
	if err != nil {
		return fmt.Errorf("failed to read filepath.yml: %w", err)
	}

	var filePaths FilePathYml
	if err := yaml.Unmarshal(filepathContent, &filePaths); err != nil {
		return fmt.Errorf("failed to parse filepath.yml: %w", err)
	}

	// Fetch dockerfile contents from GitHub
	dockerfileContents, err := fetchFileContents(ctx, repo, filePaths.Dockerfiles)
	if err != nil {
		log.Printf("Warning: failed to fetch some dockerfiles: %v", err)
	}

	// Fetch makefile contents from GitHub
	makefileContents, err := fetchFileContents(ctx, repo, filePaths.Makefiles)
	if err != nil {
		log.Printf("Warning: failed to fetch some makefiles: %v", err)
	}

	// Build user prompt
	userPrompt := buildPrompt(dockerfileContents, makefileContents)

	// Call Azure Foundry API
	log.Println("Calling Azure OpenAI API...")
	response, err := callAzureAPI(ctx, string(skillContent), userPrompt)
	if err != nil {
		return fmt.Errorf("Azure API call failed: %w", err)
	}

	// Save result
	outputPath := filepath.Join(resultPath, "NonDeterministicValues.yml")
	if err := os.WriteFile(outputPath, []byte(response), 0644); err != nil {
		return fmt.Errorf("failed to write NonDeterministicValues.yml: %w", err)
	}

	log.Printf("Non-deterministic values saved to: %s", outputPath)
	return nil
}

// fetchFileContents fetches file contents from GitHub
func fetchFileContents(ctx context.Context, repo string, paths []string) (map[string]string, error) {
	contents := make(map[string]string)

	// Parse repo URL
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repo format: %s", repo)
	}
	owner, repoName := parts[0], parts[1]

	for _, path := range paths {
		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", owner, repoName, path)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			log.Printf("Warning: failed to create request for %s: %v", path, err)
			continue
		}

		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "token "+token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Warning: failed to fetch %s: %v", path, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("Warning: failed to fetch %s: status %d", path, resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Warning: failed to read %s: %v", path, err)
			continue
		}

		contents[path] = string(body)
	}

	return contents, nil
}

// buildPrompt constructs the user prompt with dockerfile and makefile contents
func buildPrompt(dockerfiles, makefiles map[string]string) string {
	var sb strings.Builder

	sb.WriteString("Analyze the following build files and extract non-deterministic values.\n\n")

	sb.WriteString("## Dockerfiles\n")
	if len(dockerfiles) == 0 {
		sb.WriteString("(No Dockerfiles found)\n\n")
	} else {
		for path, content := range dockerfiles {
			sb.WriteString(fmt.Sprintf("### %s\n```dockerfile\n%s\n```\n\n", path, content))
		}
	}

	sb.WriteString("## Makefiles\n")
	if len(makefiles) == 0 {
		sb.WriteString("(No Makefiles found)\n\n")
	} else {
		for path, content := range makefiles {
			sb.WriteString(fmt.Sprintf("### %s\n```makefile\n%s\n```\n\n", path, content))
		}
	}

	sb.WriteString("\nReturn the output in the exact YAML format specified in the skill document.")

	return sb.String()
}

// callAzureAPI sends request to Azure OpenAI Foundry API
func callAzureAPI(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiKey := os.Getenv("AZURE_OPENAI_KEY")
	deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT")

	if endpoint == "" || apiKey == "" || deployment == "" {
		return "", fmt.Errorf("missing Azure OpenAI config: AZURE_OPENAI_ENDPOINT, AZURE_OPENAI_KEY, or AZURE_OPENAI_DEPLOYMENT")
	}

	requestBody := ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   4000,
		Temperature: 0.1,
		Model:       deployment,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Azure AI Foundry endpoint format
    endpoint = strings.TrimSuffix(endpoint, "/")
    url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2025-01-01-preview", endpoint, deployment)

	log.Printf("Calling: %s", url)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var response ChatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if response.Error != nil {
		return "", fmt.Errorf("API error: %s (code: %s)", response.Error.Message, response.Error.Code)
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from Azure OpenAI")
	}

	return response.Choices[0].Message.Content, nil
}
