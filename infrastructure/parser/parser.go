package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/transformer"

	"gopkg.in/yaml.v3"
)

func ParseOptionalFileInfo(dockerfiles, makefiles []string, specFilePath string, agentResponse []byte) (contents.DockerfileInfo, contents.MakefileInfo, *llm.NonDeterministicValues, transformer.PreviousDalecSpec, error) {
	dockerfileInfo := contents.DockerfileInfo{
		Args:   make(map[string]string),
		Labels: make(map[string]string),
		Stages: []contents.Stage{},
	}

	makefileInfo := contents.MakefileInfo{
		Variables: make(map[string]string),
		Targets:  make(map[string][]string),
	}

	for _, dockerfile := range dockerfiles {
		ParseDockerfile(dockerfile, &dockerfileInfo)
	}

	for _, makefile := range makefiles {
		ParseMakefile(makefile, &makefileInfo)
	}

	previousDalecSpecInfo, err := fetchPreviousYAMLInfo(specFilePath)
	if err != nil {
		return dockerfileInfo, makefileInfo, nil, transformer.PreviousDalecSpec{}, err
	}

	nonDeterministicInfo, err := fetchNonDeterministicValue(agentResponse)
	if err != nil {
		return dockerfileInfo, makefileInfo, nil, transformer.PreviousDalecSpec{}, err
	}

	return dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, nil
}

func fetchPreviousYAMLInfo(filepath string) (transformer.PreviousDalecSpec, error) {
	fmt.Println("=== READING PREVIOUS YAML FILE ===")

	if filepath == "" {
		fmt.Println("⚠️  No previous YAML path provided to read previous spec.")
		return transformer.PreviousDalecSpec{}, nil
	}

	writer := &transformer.DalecSpecWriter{}
	yamlInfo, err := writer.ReadYAML(filepath)
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

func WriteOutput(dalecSpec transformer.DalecSpec) error {
	writer := &transformer.DalecSpecWriter{}

	outputPath := filepath.Join("result", "output.yml")

	yamlContent, err := writer.WriteYAML(dalecSpec, outputPath)
	if err != nil {
		return fmt.Errorf("❌ Error generating YAML: %v\n", err)
	}

	err = os.WriteFile(outputPath, []byte(yamlContent), 0644)
	if err != nil {
		return fmt.Errorf("❌ Error writing %s: %v\n", outputPath, err)
	}

	return nil
}

func fetchNonDeterministicValue(agentResponse []byte) (*llm.NonDeterministicValues, error) {
	if len(agentResponse) == 0 {
		return nil, nil
	}

	var nonDeterministicValues llm.NonDeterministicValues
	err := yaml.Unmarshal(agentResponse, &nonDeterministicValues)
	if err != nil {
		fmt.Printf("❌ Error parsing NonDeterministicValues.yml file: %v\n", err)
		return nil, err
	}

	removeFlags := map[string]string{
		"'":              "\"",
		"`":              "\"",
		"CGO_ENABLED=0 ": "",
		"CGO_ENABLED=1 ": "",
		"GOOS=linux ":    "",
		"GOARCH=amd64 ":  "",
	}

	for i := range nonDeterministicValues.Binaries {
		for key, value := range removeFlags {
			nonDeterministicValues.Binaries[i].BuildCommand = strings.ReplaceAll(nonDeterministicValues.Binaries[i].BuildCommand, key, value)
		}

		fmt.Printf("New Command: %v\n", nonDeterministicValues.Binaries[i].BuildCommand)
	}

	fmt.Println("✅ Successfully read NonDeterministicValues.yml file.")
	return &nonDeterministicValues, nil
}
