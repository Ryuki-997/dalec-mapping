package tool

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const GeneratorDir = "generator"

// RegisterGeneratorTools registers all generator-related tools to the MCP server

func RegisterGeneratorTools(s *server.MCPServer) {
// Add the discover tool
	discoverTool := mcp.NewTool("Discover Build Files",
		mcp.WithDescription("Step 1: Discover all Dockerfiles and Makefiles in a GitHub repository using DFS search. This is Step 0 of the dalec-spec-generator workflow."),
		mcp.WithString("repo",
			mcp.Required(),
			mcp.Description("GitHub repository in format 'owner/repo' or 'owner/repo/tree/branch' or 'owner/repo/tree/branch/subdir'"),
		),
	)
	s.AddTool(discoverTool, DiscoverHandler)
	
	populateTool := mcp.NewTool("Populate Non-Deterministic Fields",
		mcp.WithDescription("Step 2: Populate non-deterministic fields in a Dalec specification YAML file."),
		mcp.WithArray("dockerfile_paths",
			mcp.Description("Paths to the Dockerfiles (e.g., '/tmp/result/repo/Dockerfile')"),
		),
		mcp.WithArray("makefile_paths",
			mcp.Description("Paths to the Makefiles (e.g., '/tmp/result/repo/Makefile')"),
		),
	)
	s.AddTool(populateTool, PopulateHandler)

	// Add the generate tool
	generateTool := mcp.NewTool("Generate Dalec Spec",
		mcp.WithDescription("Step 3: Generate a Dalec specification YAML file from a Dockerfile and Makefile. Requires discover_build_files to be run first."),
		mcp.WithString("repo",
			mcp.Required(),
			mcp.Description("GitHub repository in format 'owner/repo' or 'owner/repo/tree/branch'"),
		),
		mcp.WithString("dockerfile",
			mcp.Description("Path to the downloaded Dockerfile (e.g., '/tmp/result/repo/Dockerfile')"),
		),
		mcp.WithString("makefile",
			mcp.Description("Path to the downloaded Makefile (optional, e.g., '/tmp/result/repo/Makefile')"),
		),
		mcp.WithString("output",
			mcp.Description("Name for the output YAML file (without extension)"),
		),
	)
	s.AddTool(generateTool, GenerateHandler)
}