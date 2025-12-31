package main

import (
	"flag"
	"fmt"
	"os"

	"dalec-mapping/github"
	"dalec-mapping/parser"
	"dalec-mapping/transformer"
)

func main() {
	// Define CLI flags
	repoPath := flag.String("repo", "", "GitHub repository (e.g., owner/repo or https://github.com/owner/repo)")
	dockerfilePath := flag.String("dockerfile", "Dockerfile", "Path to Dockerfile")
	outputPath := flag.String("output", "test.yml", "Output YAML file path")
	verbose := flag.Bool("v", false, "Verbose output")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Converts Dockerfile to Dalec specification with GitHub metadata.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s -repo Ryuki-997/HelloWorld\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -repo https://github.com/owner/repo -dockerfile ./Dockerfile -output spec.yml\n", os.Args[0])
	}

	flag.Parse()

	// Validate required argument
	if *repoPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -repo flag is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	fmt.Println("🚀 Dalec Spec Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Fetch GitHub repository information
	fmt.Println("=== FETCHING GITHUB METADATA ===")
	repoInfo, err := github.FetchRepoInfo(*repoPath)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	} else {
		github.PrintRepoInfo(repoInfo)
	}

	// Parse Dockerfile
	fmt.Println("=== PARSING DOCKERFILE ===")
	dockerfileInfo, err := parser.ParseDockerfile(*dockerfilePath)
	if err != nil {
		fmt.Printf("❌ Error parsing Dockerfile: %v\n", err)
		os.Exit(1)
	}

	if *verbose {
		parser.PrintDockerfileInfo(dockerfileInfo)
	} else {
		fmt.Printf("✅ Parsed %d build stages\n\n", len(dockerfileInfo.Stages))
	}

	// Transform to Dalec spec with repository metadata
	fmt.Println("=== TRANSFORMING TO DALEC SPEC ===")

	// Convert RepoInfo to RepoMetadata for transformer
	var repoMeta *transformer.RepoMetadata
	if repoInfo != nil {
		repoMeta = &transformer.RepoMetadata{
			GitURL:      repoInfo.GitURL,
			Commit:      repoInfo.LatestCommit,
			Website:     repoInfo.Website,
			Description: repoInfo.Description,
			License:     repoInfo.License,
			RepoName:    repoInfo.Repo,
		}
	}

	dalecSpec := transformer.TransformToDalec(dockerfileInfo, repoMeta)

	// Write to output file
	yamlContent, err := transformer.WriteYAML(dalecSpec)
	if err != nil {
		fmt.Printf("❌ Error generating YAML: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(*outputPath, []byte(yamlContent), 0644)
	if err != nil {
		fmt.Printf("❌ Error writing %s: %v\n", *outputPath, err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", *outputPath)

	fmt.Println("📝 Automatically populated fields:")
	if repoInfo.GitURL != "" {
		fmt.Printf("  ✓ Source URL: %s\n", repoInfo.GitURL)
	}
	if repoInfo.LatestCommit != "" {
		fmt.Printf("  ✓ Commit: %s\n", repoInfo.LatestCommit)
	}
	if repoInfo.Website != "" {
		fmt.Printf("  ✓ Website: %s\n", repoInfo.Website)
	}
	if repoInfo.Description != "" {
		fmt.Printf("  ✓ Description: %s\n", repoInfo.Description)
	}
	if repoInfo.License != "" {
		fmt.Printf("  ✓ License: %s\n", repoInfo.License)
	}

	// Show what still needs manual input
	needsManual := []string{}
	if repoInfo.GitURL == "" {
		needsManual = append(needsManual, "source URL")
	}
	if repoInfo.Description == "" {
		needsManual = append(needsManual, "description")
	}
	if repoInfo.License == "" {
		needsManual = append(needsManual, "license")
	}

	if len(needsManual) > 0 {
		fmt.Println("\n⚠️  Fields requiring manual input:")
		for _, field := range needsManual {
			fmt.Printf("  • %s\n", field)
		}
	}
}
