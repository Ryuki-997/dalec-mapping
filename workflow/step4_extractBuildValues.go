// ═══════════════════════════════════════════════════════════════════════════════
// Step 4 — Extract Build Values
//
//   Uses Azure OpenAI (LLM) to analyze the source Dockerfile and Makefile and
//   extract non-deterministic build values (targets, binaries, commands).
//
//   Chunk 1 · TYPES   ChatRequest, ChatResponse
//   Chunk 2 · MAIN    ExtractBuildValues()
//   Chunk 3 · HELPERS buildPrompt(), callAzureAPI(), extractYAMLContent(),
//                      fetchFileContents()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

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

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · TYPES ────────────────────────────────────────────────────────

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

// ─── Chunk 2 · MAIN ─────────────────────────────────────────────────────────

// ExtractBuildValues sends the Dockerfile and Makefile to Azure OpenAI, extracts
// non-deterministic values, and saves the result as NonDeterministicValues.yml.
func ExtractBuildValues(ctx context.Context, onboard *onboarding.OnboardingInfo) ([]byte, error) {
	log.Printf("Running populate for repo: %s", onboard.Repository)

	_, repoName, _ := repository.FetchRepositorySegments(onboard.Repository)
	resultPath := filepath.Join("result", repoName)

	// Read the skill document that defines the expected output format
	skillContent, err := os.ReadFile(utils.Skillpath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SKILL.md at %s: %w", utils.Skillpath, err)
	}

	// Build the user prompt from Dockerfile + Makefile
	userPrompt := buildPrompt(onboard.DockerfileContent, onboard.MakefileContent, onboard.SpecImageName)

	// Call Azure Foundry API
	log.Println("Calling Azure OpenAI API...")
	response, err := callAzureAPI(ctx, string(skillContent), userPrompt)
	if err != nil {
		return nil, fmt.Errorf("Azure API call failed: %w", err)
	}

	// Save the extracted values
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

// ─── Chunk 3 · HELPERS ──────────────────────────────────────────────────────

// buildPrompt constructs the user prompt with Dockerfile and Makefile contents.
func buildPrompt(dockerfile, makefile []byte, specImageName string) string {
	var sb strings.Builder

	sb.WriteString("Analyze the following build files and extract non-deterministic values.\n\n")
	if specImageName != "" {
		sb.WriteString(fmt.Sprintf("## Target Image\nThe image being built is **%s**. Extract only the binary and build command relevant to this image. Ignore unrelated build targets in the Makefile.\n\n", specImageName))
	}

	sb.WriteString("## Dockerfiles\n")
	if dockerfile == nil {
		sb.WriteString("(No Dockerfile found)\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("### Dockerfile\n```dockerfile\n%s\n```\n\n", string(dockerfile)))
	}

	sb.WriteString("## Makefiles\n")
	if makefile == nil {
		sb.WriteString("(No Makefile found)\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("### Makefile\n```makefile\n%s\n```\n\n", string(makefile)))
	}

	sb.WriteString("\nReturn the output in the exact YAML format specified in the skill document.")
	return sb.String()
}

// callAzureAPI sends the combined system+user prompt to Azure OpenAI Foundry.
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
		Input: combinedPrompt,
		Model: deployment,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

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

	return extractYAMLContent(response.Output[0].Content[0].Text), nil
}

// extractYAMLContent strips markdown code fences and preamble from the LLM response.
func extractYAMLContent(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "```yaml\n"); idx != -1 {
		text = text[idx+len("```yaml\n"):]
		if end := strings.LastIndex(text, "```"); end != -1 {
			text = text[:end]
		}
		return strings.TrimSpace(text)
	}
	text = strings.TrimPrefix(text, "```yaml")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text)
}

// fetchFileContents fetches raw file contents from GitHub for the given paths.
func fetchFileContents(onboard *onboarding.OnboardingInfo, paths []string) (map[string]string, error) {
	contents := make(map[string]string)
	owner, repoName, branch := repository.FetchRepositorySegments(onboard.Repository)

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
