package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
)

const defaultServerURL = "http://localhost:8080/mcp"

// MCPClient holds the session state for MCP communication
type MCPClient struct {
	serverURL string
	sessionID string
	requestID int64
}

func main() {
	repo := flag.String("repo", "azure/azure-container-networking", "GitHub repository (owner/repo)")
	serverURL := flag.String("server", defaultServerURL, "MCP server URL")
	flag.Parse()

	log.Printf("Running Dalec spec generation for: %s", *repo)

	// Initialize MCP client with session
	client, err := NewMCPClient(*serverURL)
	if err != nil {
		log.Fatalf("Failed to initialize MCP client: %v", err)
	}
	log.Printf("MCP session initialized: %s", client.sessionID)

	// Step 1: Discover build files
	log.Println("\n=== Step 1: Discover Build Files ===")
	discoverResult, err := client.callTool("Discover Build Files", map[string]interface{}{
		"repo": *repo,
	})
	if err != nil {
		log.Fatalf("Step 1 failed: %v", err)
	}
	fmt.Println(discoverResult)

	// Step 2: Populate non-deterministic fields
	log.Println("\n=== Step 2: Populate Non-Deterministic Fields ===")
	populateResult, err := client.callTool("Populate Non-Deterministic Fields", map[string]interface{}{})
	if err != nil {
		log.Fatalf("Step 2 failed: %v", err)
	}
	fmt.Println(populateResult)

	// Step 3: Generate Dalec spec
	log.Println("\n=== Step 3: Generate Dalec Spec ===")
	generateResult, err := client.callTool("Generate Dalec Spec", map[string]interface{}{
		"repo":       *repo,
		"output":     "dalec-spec",
	})
	if err != nil {
		log.Fatalf("Step 3 failed: %v", err)
	}
	fmt.Println(generateResult)

	log.Println("\n=== Generation Complete ===")
}

// NewMCPClient creates a new MCP client and initializes the session
func NewMCPClient(serverURL string) (*MCPClient, error) {
	client := &MCPClient{
		serverURL: serverURL,
		requestID: 1,
	}

	// Send initialize request
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      client.nextID(),
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]interface{}{
				"elicitation": map[string]interface{}{},
			},
			"clientInfo": map[string]interface{}{
				"name":    "dalec-spec-runner",
				"version": "1.0.0",
			},
		},
	}

	body, err := json.Marshal(initReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal init request: %w", err)
	}

	resp, err := http.Post(serverURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send init request: %w", err)
	}
	defer resp.Body.Close()

	// Get session ID from response header
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		// Try reading from body if not in header
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("no session ID in response. Status: %d, Body: %s", resp.StatusCode, string(respBody))
	}

	client.sessionID = sessionID

	// Send initialized notification
	initializedReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}

	body, _ = json.Marshal(initializedReq)
	req, _ := http.NewRequest("POST", serverURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	http.DefaultClient.Do(req)

	return client, nil
}

func (c *MCPClient) nextID() int64 {
	c.requestID++
	return c.requestID
}

// callTool makes an MCP tool call to the server
func (c *MCPClient) callTool(toolName string, args map[string]interface{}) (string, error) {
	// Create JSON-RPC request
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      c.nextID(),
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}

	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.serverURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", c.sessionID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON-RPC response
	var rpcResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w\nBody: %s", err, string(respBody))
	}

	if rpcResp.Error != nil {
		return "", fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	if rpcResp.Result.IsError {
		if len(rpcResp.Result.Content) > 0 {
			return "", fmt.Errorf("tool error: %s", rpcResp.Result.Content[0].Text)
		}
		return "", fmt.Errorf("tool returned error")
	}

	if len(rpcResp.Result.Content) > 0 {
		return rpcResp.Result.Content[0].Text, nil
	}

	return "", nil
}

/* TODO: 
1. move to new branch adapter
2. pass api token to internal generator calls
3. verify non deterministic population works end to end
4. verify dalec spec generation works end to end
5. check upstream deterministic dependency resolution
6. include patching from upstream suggestion
7. testing and validation
*/