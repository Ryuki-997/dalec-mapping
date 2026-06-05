package parser

import (
	"fmt"
	"log"

	"dalec-mapping/domain/workplan"
)

// ParseOptionalFileInfo parses Dockerfile and Makefile bytes and writes the
// results onto component.BuildFiles. Returns without touching component when both
// inputs are empty. Each input is parsed only when non-empty, so first-time
// onboards (which legitimately have no sibling Dockerfile yet) are handled
// gracefully instead of aborting the pipeline.
func ParseOptionalFileInfo(component *workplan.WorkComponent, dockerfile, makefile []byte) {
	if dockerfile == nil && makefile == nil {
		return
	}
	imageName := component.Naming.SpecImageName
	if len(dockerfile) > 0 {
		df, err := ParseDockerfile(dockerfile)
		if err != nil {
			log.Fatalf("❌ failed to parse Dockerfile: %v", err)
		}
		df.Source = dockerfile
		component.BuildFiles.Dockerfile = df
	}
	if len(makefile) > 0 {
		mf, err := ParseMakefile(makefile, imageName)
		if err != nil {
			log.Fatalf("❌ failed to parse Makefile: %v", err)
		}
		mf.Source = makefile
		component.BuildFiles.Makefile = mf
	}
}

// EncodeDalecSpec marshals a DalecSpec to YAML bytes (no disk write).
func EncodeDalecSpec(dalecSpec DalecSpec) ([]byte, error) {
	writer := &DalecSpecWriter{}
	yamlContent, err := writer.WriteYAML(dalecSpec)
	if err != nil {
		return nil, fmt.Errorf("generating YAML: %w", err)
	}
	return []byte(yamlContent), nil
}
