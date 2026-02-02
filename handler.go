package main

import (
	"azure-spec-generation/tool"
	"bufio"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

const (
	generatorDir = "generator"
	resultDir    = "/tmp/result"
)

func initialize() {
	// Load .env file first
	loadEnvFile()

	// Verify GITHUB_TOKEN is set
	if token := os.Getenv("GITHUB_TOKEN"); token == "" {
		log.Println("Warning: GITHUB_TOKEN not set. API rate limits will apply (60 requests/hour).")
	} else {
		log.Printf("GITHUB_TOKEN found (%d chars). Using authenticated API requests.", len(token))
	}

	// Create MCP server
	s := server.NewMCPServer(
		"Dalec Spec Generator",
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	tool.RegisterGeneratorTools(s)

	// Get the port from environment
	port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Printf("Starting MCP server on %s", addr)
	log.Printf("MCP endpoint will be available at http://localhost%s/mcp", addr)

	httpServer := server.NewStreamableHTTPServer(s)

	// Set up HTTP handler
	http.Handle("/mcp", httpServer)

	log.Println("Initializing Streamable HTTP server...")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// loadEnvFile loads environment variables from .env file
func loadEnvFile() {
	// Try current directory first, then parent directories
	envPaths := []string{
		".env",
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
	}

	for _, envPath := range envPaths {
		if err := loadEnvFromFile(envPath); err == nil {
			log.Printf("Loaded environment from %s", envPath)
			return
		}
	}
	log.Println("No .env file found")
}

// loadEnvFromFile reads a .env file and sets environment variables
func loadEnvFromFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		value = strings.Trim(value, `"'`)

		// Set environment variable (overwrite if exists to ensure .env takes effect)
		os.Setenv(key, value)
	}

	return scanner.Err()
}