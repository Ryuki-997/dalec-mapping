package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalec/cli"
	"dalec/github"
	"dalec/parser"
	"dalec/transformer"

	"gopkg.in/yaml.v3"
)

func main() {
	cliOptions := cli.DefineFlags()

	fmt.Println("🚀 Dalec Spec Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Fetch GitHub repository info
	repoInfo, err := fetchGitHubRepoInfo(cliOptions.RepoPath, cliOptions.Tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Apply field overrides from CLI
	applyFieldOverrides(repoInfo, cliOptions)

	// Parse Dockerfile if path provided
	dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, err := parseOptionalFileInfo(cliOptions.DockerfilePath, cliOptions.MakefilePath, cliOptions.SpecFilePath, cliOptions.Verbose)
	if err != nil {
		fmt.Printf("❌ Error parsing optional files: %v\n", err)
	}

	// Transform to Dalec spec with repository metadata
	fmt.Println("=== TRANSFORMING TO DALEC SPEC ===")

	defaultSpec := transformer.InitDefaultSpec(repoInfo, dockerfileInfo, previousDalecSpecInfo)

	fmt.Println("=== DOCKER FILE INFO ===")
	transformer.PrintDockerfileInfo(defaultSpec)

	// TODO: maybe add or override targets (currently override)
	// if len(cliOptions.Targets) > 0 {
	// 	defaultSpec.BuildTargets = make([]transformer.BuildTarget, len(cliOptions.Targets))
	// 	for i, t := range cliOptions.Targets {
	// 		defaultSpec.BuildTargets[i] = transformer.BuildTarget(t)
	// 	}
	// }

	dalecSpec := transformer.TransformToDalec(defaultSpec, makefileInfo, nonDeterministicInfo)

	// Write to output file
	fmt.Println("=== WRITING OUTPUT YAML FILE ===")

	err = writeOutput(dalecSpec, cliOptions)
	if err != nil {
		fmt.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", cliOptions.OutputPath)
}

func applyFieldOverrides(repoInfo *github.RepoInfo, opts cli.CLIOptions) {
	if opts.Name != "" {
		repoInfo.Repo = opts.Name
	}
	if opts.Description != "" {
		repoInfo.Description = opts.Description
	}
	if opts.License != "" {
		repoInfo.License = opts.License
	}
}

func fetchGitHubRepoInfo(repoPath, tag string) (*github.RepoInfo, error) {
	// Fetch GitHub repository information
	fmt.Println("=== FETCHING GITHUB METADATA ===")
	repoInfo, err := github.FetchRepoInfo(repoPath)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		return nil, err
	}

	// If tag is specified, fetch that tag's commit SHA
	if tag != "" {
		fmt.Printf("Fetching tag: %s\n", tag)
		err = github.FetchTagInfo(repoInfo, tag)
		if err != nil {
			fmt.Printf("❌ Error fetching tag info: %v\n", err)
			return nil, err
		}
	}

	github.PrintRepoInfo(repoInfo)

	return repoInfo, nil
}

func parseOptionalFileInfo(dockerfilePath, makefilePath, specFilePath string, verbose bool) (*parser.DockerfileInfo, *parser.MakefileInfo, *parser.NonDeterministicValues, transformer.PreviousDalecSpec, error) {
	dockerfileInfo, err := fetchDockerfileInfo(dockerfilePath, verbose)
	if err != nil {
		return nil, nil, nil, transformer.PreviousDalecSpec{}, err
	}

	makefileInfo, err := fetchMakefileInfo(makefilePath, verbose)
	if err != nil {
		return nil, nil, nil, transformer.PreviousDalecSpec{}, err
	}

	previousDalecSpecInfo, err := fetchPreviousYAMLInfo(specFilePath)
	if err != nil {
		return nil, nil, nil, transformer.PreviousDalecSpec{}, err
	}

	nonDeterministicInfo, err := fetchNonDeterministicValue(dockerfilePath)
	if err != nil {
		return nil, nil, nil, transformer.PreviousDalecSpec{}, err
	}

	return dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, nil
}

func fetchDockerfileInfo(dockerfilePath string, verbose bool) (*parser.DockerfileInfo, error) {
	fmt.Println("=== PARSING DOCKERFILE ===")

	var dockerfileInfo *parser.DockerfileInfo

	if dockerfilePath == "" {
		fmt.Println("⚠️  No Dockerfile path provided.")
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

func fetchMakefileInfo(makefilePath string, verbose bool) (*parser.MakefileInfo, error) {
	fmt.Println("=== PARSING MAKEFILE ===")

	var makefileInfo *parser.MakefileInfo

	if makefilePath == "" {
		fmt.Println("⚠️  No Makefile path provided.")
		return nil, nil
	}

	makefileInfo, err := parser.ParseMakefile(makefilePath)
	if err != nil {
		fmt.Printf("❌ Error parsing Makefile: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		fmt.Println("Makefile Variables:")
		for k, v := range makefileInfo.Variables {
			fmt.Printf("  %s = %s\n", k, v)
		}
		fmt.Println()
	} else {
		fmt.Printf("✅ Parsed %d variables from Makefile\n\n", len(makefileInfo.Variables))
	}

	return makefileInfo, nil
}

func fetchPreviousYAMLInfo(filepath string) (transformer.PreviousDalecSpec, error) {
	fmt.Println("=== READING PREVIOUS YAML FILE ===")

	if filepath == "" {
		fmt.Println("⚠️  No previous YAML path provided to read previous spec.")
		return transformer.PreviousDalecSpec{}, nil
	}

	yamlInfo, err := transformer.ReadYAML(filepath)
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

func writeOutput(dalecSpec transformer.DalecSpec, cliOptions cli.CLIOptions) error {
	yamlContent, err := transformer.WriteYAML(dalecSpec)
	if err != nil {
		return fmt.Errorf("❌ Error generating YAML: %v\n", err)
	}

	err = os.WriteFile(cliOptions.OutputPath, []byte(yamlContent), 0644)
	if err != nil {
		return fmt.Errorf("❌ Error writing %s: %v\n", cliOptions.OutputPath, err)
	}

	return nil
}

func fetchNonDeterministicValue(dockerfilePath string) (*parser.NonDeterministicValues, error) {
	dir := filepath.Dir(dockerfilePath)
	path := filepath.Join(dir, "NonDeterministicValues.yml")

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("⚠️  No NonDeterministicValues.yml file found, proceeding without it.")
			return nil, nil
		}
		fmt.Printf("❌ Error reading NonDeterministicValues.yml file: %v\n", err)
		return nil, err
	}

	var nonDeterministicValues parser.NonDeterministicValues
	err = yaml.Unmarshal(content, &nonDeterministicValues)
	if err != nil {
		fmt.Printf("❌ Error parsing NonDeterministicValues.yml file: %v\n", err)
		return nil, err
	}

	// Clear out unecessary flags
	commands := &nonDeterministicValues.BuildCommand

	*commands = strings.ReplaceAll(*commands, "'", "\"")
	*commands = strings.ReplaceAll(*commands, "`", "\"")
	*commands = strings.ReplaceAll(*commands, "CGO_ENABLED=0 ", "")
	*commands = strings.ReplaceAll(*commands, "CGO_ENABLED=1 ", "")
	*commands = strings.ReplaceAll(*commands, "GOOS=linux ", "")
	*commands = strings.ReplaceAll(*commands, "GOARCH=amd64 ", "")

	fmt.Println("✅ Successfully read NonDeterministicValues.yml file.")
	return &nonDeterministicValues, nil
}
