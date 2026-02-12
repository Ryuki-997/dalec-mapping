package tool

import (
	"bytes"
	"context"
	"dalec-mapping/global"

	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const skillPath = "skills/non-deterministic-setup/SKILL.md"

type ChatRequest struct {
	Input       string  `json:"input"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Model       string  `json:"model,omitempty"`
}

type ChatResponse struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Role string `json:"role"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Populate analyzes dockerfiles and makefiles using LLM to extract non-deterministic values
func Populate(ctx context.Context, onboard *global.OnboardingInfo, fileContents *global.InstructionContents) ([]byte, error) {
	log.Printf("Running populate for repo: %s", onboard.Repository)

	_, repoName, _, _ := global.ExtractRepositorySegments(onboard.Repository)
	resultPath := filepath.Join("result", repoName)

	// Read skill.md
	skillContent, err := os.ReadFile(global.Skillpath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SKILL.md at %s: %w", global.Skillpath, err)
	}

	// Build user prompt
	userPrompt := buildPrompt(fileContents.Dockerfiles, fileContents.Makefiles)

	// Call Azure Foundry API
	log.Println("Calling Azure OpenAI API...")
	response, err := callAzureAPI(ctx, string(skillContent), userPrompt)
	if err != nil {
		return nil, fmt.Errorf("Azure API call failed: %w", err)
	}

	// Save result
	outputPath := filepath.Join(resultPath, "NonDeterministicValues.yml")
	if err := os.MkdirAll(resultPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create result directory: %w", err)
	}
	if err := os.WriteFile(outputPath, []byte(response), 0644); err != nil {
		return nil, fmt.Errorf("failed to write NonDeterministicValues.yml: %w", err)
	}

	log.Printf("Non-deterministic values saved to: %s", outputPath)
	return []byte(response), nil
}

// fetchFileContents fetches file contents from GitHub
func fetchFileContents(onboard *global.OnboardingInfo, paths []string) (map[string]string, error) {
	contents := make(map[string]string)

	owner, repoName, branch, _ := global.ExtractRepositorySegments(onboard.Repository)

	for _, path := range paths {
		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repoName, branch, path)

		req, err := http.NewRequest("GET", url, nil)
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
func buildPrompt(dockerfiles, makefiles []string) string {
	var sb strings.Builder

	sb.WriteString("Analyze the following build files and extract non-deterministic values.\n\n")

	sb.WriteString("## Dockerfiles\n")
	if len(dockerfiles) == 0 {
		sb.WriteString("(No Dockerfiles found)\n\n")
	} else {
		for path, content := range dockerfiles {
			sb.WriteString(fmt.Sprintf("### %v\n```dockerfile\n%s\n```\n\n", path, content))
		}
	}

	sb.WriteString("## Makefiles\n")
	if len(makefiles) == 0 {
		sb.WriteString("(No Makefiles found)\n\n")
	} else {
		for path, content := range makefiles {
			sb.WriteString(fmt.Sprintf("### %v\n```makefile\n%s\n```\n\n", path, content))
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
	version := os.Getenv("AZURE_OPENAI_VERSION")

	if endpoint == "" || apiKey == "" || deployment == "" {
		return "", fmt.Errorf("missing Azure OpenAI config: AZURE_OPENAI_ENDPOINT, AZURE_OPENAI_KEY, or AZURE_OPENAI_DEPLOYMENT")
	}

	combinedPrompt := fmt.Sprintf("%s\n\n%s", systemPrompt, userPrompt)
	requestBody := ChatRequest{
		Input:       combinedPrompt,
		Model:       deployment,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Azure AI Foundry endpoint format
	endpoint = strings.TrimSuffix(endpoint, "/")
	url := fmt.Sprintf("%s/openai/responses?api-version=%s", endpoint, version)

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

	if len(response.Output) == 0 || len(response.Output[0].Content) == 0 {
		return "", fmt.Errorf("no response from Azure OpenAI")
	}

	responseText := response.Output[0].Content[0].Text
	responseText = strings.TrimSpace(responseText)
	responseText = strings.TrimPrefix(responseText, "```yaml")
	responseText = strings.TrimSuffix(responseText, "```")

	return responseText, nil
}
