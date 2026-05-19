package parser

import (
	"fmt"
	"os"
	"path/filepath"

	"dalec-mapping/pipeline"
)

func ParseOptionalFileInfo(dockerfile, makefile []byte) {
	ParseDockerfile(dockerfile, &pipeline.Current.Dockerfile)
	imageName := ""
	if pipeline.Current.Onboard != nil {
		imageName = pipeline.Current.Onboard.SpecImageName
	}
	ParseMakefile(makefile, &pipeline.Current.Makefile, imageName)
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
