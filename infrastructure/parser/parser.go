package parser

import (
	"fmt"
	"os"
	"path/filepath"

	"dalec-mapping/domain/contents"
)

func ParseOptionalFileInfo(dockerfile, makefile []byte, specFilePath string) (PreviousDalecSpec, error) {
	contents.Dockerfile = contents.DockerfileInfo{
		Args:   make(map[string]string),
		Labels: make(map[string]string),
		Stages: []contents.Stage{},
	}

	contents.Makefile = contents.MakefileInfo{
		Variables: make(map[string]string),
	}

	ParseDockerfile(dockerfile, &contents.Dockerfile)
	ParseMakefile(makefile, &contents.Makefile)

	previousDalecSpecInfo, err := fetchPreviousYAMLInfo(specFilePath)
	if err != nil {
		return PreviousDalecSpec{}, err
	}

	return previousDalecSpecInfo, nil
}

func fetchPreviousYAMLInfo(filepath string) (PreviousDalecSpec, error) {
	fmt.Println("=== READING PREVIOUS YAML FILE ===")

	if filepath == "" {
		fmt.Println("⚠️  No previous YAML path provided to read previous spec.")
		return PreviousDalecSpec{}, nil
	}

	writer := &DalecSpecWriter{}
	yamlInfo, err := writer.ReadYAML(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("⚠️  No previous YAML file found, proceeding without it.")
			return PreviousDalecSpec{}, nil
		}
		fmt.Printf("❌ Error reading previous YAML file: %v\n", err)
		return PreviousDalecSpec{}, err
	}

	fmt.Println("✅ Successfully read previous YAML file.")
	return yamlInfo, nil
}

func WriteOutput(dalecSpec DalecSpec) error {
	writer := &DalecSpecWriter{}

	outputPath := filepath.Join("result", "output.yml")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("❌ Error creating result directory: %v\n", err)
	}

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
