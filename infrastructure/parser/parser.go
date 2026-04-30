package parser

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"dalec-mapping/domain/contents"
	"dalec-mapping/pipeline"
)

func ParseOptionalFileInfo(dockerfile, makefile []byte, specFilePath string) (contents.PreviousDalecSpec, error) {
	ParseDockerfile(dockerfile, &pipeline.Current.Dockerfile)
	ParseMakefile(makefile, &pipeline.Current.Makefile)

	previousDalecSpecInfo, err := fetchPreviousYAMLInfo(specFilePath)
	if err != nil {
		return contents.PreviousDalecSpec{}, err
	}

	return previousDalecSpecInfo, nil
}

func fetchPreviousYAMLInfo(filepath string) (contents.PreviousDalecSpec, error) {
	log.Println("=== READING PREVIOUS YAML FILE ===")

	if filepath == "" {
		log.Println("⚠️  No previous YAML path provided to read previous spec.")
		return contents.PreviousDalecSpec{}, nil
	}

	writer := &DalecSpecWriter{}
	yamlInfo, err := writer.ReadYAML(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("⚠️  No previous YAML file found, proceeding without it.")
			return contents.PreviousDalecSpec{}, nil
		}
		log.Printf("❌ Error reading previous YAML file: %v\n", err)
		return contents.PreviousDalecSpec{}, err
	}

	log.Println("✅ Successfully read previous YAML file.")
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
