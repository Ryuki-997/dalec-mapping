package main

import (
	"flag"
	"fmt"
	"os"

	"dalec-mapping/github"
	"dalec-mapping/parser"
	"dalec-mapping/transformer"
)

type cliOptions struct {
	repoPath       *string
	dockerfilePath *string
	outputPath     *string
	verbose        *bool
}

func main() {

	cliOptions := defineFlags()

	fmt.Println("🚀 Dalec Spec Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Fetch GitHub repository info
	repoInfo, err := fetchGitHubRepoInfo(*cliOptions.repoPath)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Parse Dockerfile if path provided
	dockerfileInfo, err := fetchDockerfileInfo(*cliOptions.dockerfilePath, *cliOptions.verbose)
	if err != nil {
		fmt.Printf("❌ Error parsing Dockerfile: %v\n", err)
	}

	// Read previous YAML file if exists
	previousYAMLInfo, err := fetchPreviousYAMLInfo(*cliOptions.outputPath)
	if err != nil {
		fmt.Printf("❌ Error reading previous YAML info: %v\n", err)
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

	dalecSpec := transformer.TransformToDalec(repoMeta, previousYAMLInfo, dockerfileInfo)

	// Write to output file
	yamlContent, err := transformer.WriteYAML(dalecSpec)
	if err != nil {
		fmt.Printf("❌ Error generating YAML: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(*cliOptions.outputPath, []byte(yamlContent), 0644)
	if err != nil {
		fmt.Printf("❌ Error writing %s: %v\n", *cliOptions.outputPath, err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", *cliOptions.outputPath)
}

func defineFlags() cliOptions {
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

	return cliOptions{
		repoPath:       repoPath,
		dockerfilePath: dockerfilePath,
		outputPath:     outputPath,
		verbose:        verbose,
	}
}

func fetchGitHubRepoInfo(repoPath string) (*github.RepoInfo, error) {
	// Fetch GitHub repository information
	fmt.Println("=== FETCHING GITHUB METADATA ===")
	repoInfo, err := github.FetchRepoInfo(repoPath)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		return nil, err
	} else {
		github.PrintRepoInfo(repoInfo)
	}

	return repoInfo, nil
}

func fetchDockerfileInfo(dockerfilePath string, verbose bool) (*parser.DockerfileInfo, error) {
	fmt.Println("=== PARSING DOCKERFILE ===")

	var dockerfileInfo *parser.DockerfileInfo

	if dockerfilePath == "" {
		fmt.Println("❌ No Dockerfile path provided.")
		return nil, nil
	}

	dockerfileInfo, err := parser.ParseDockerfile(dockerfilePath)
	if err != nil {
		fmt.Printf("❌ Error parsing Dockerfile: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		parser.PrintDockerfileInfo(dockerfileInfo)
	} else {
		fmt.Printf("✅ Parsed %d build stages\n\n", len(dockerfileInfo.Stages))
	}

	return dockerfileInfo, nil
}

func fetchPreviousYAMLInfo(outputPath string) (transformer.PreviousDalecSpec, error) {
	fmt.Println("=== READING PREVIOUS YAML FILE ===")

	yamlInfo, err := transformer.ReadYAML(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("⚠️  No previous YAML file found, proceeding without it.")
			return transformer.PreviousDalecSpec{}, nil
		}
		fmt.Printf("❌ Error reading previous YAML file: %v\n", err)
		return transformer.PreviousDalecSpec{}, err
	}

	fmt.Println("✅ Successfully read previous YAML file.")
	return yamlInfo, nil
}
