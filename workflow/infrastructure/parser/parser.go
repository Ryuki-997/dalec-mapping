package parser

import (
	"fmt"
	"log"

	"dalec-mapping/domain/workplan"
)

// ParseOptionalFileInfo parses Dockerfile and Makefile bytes and writes the
// results onto item.BuildFiles. Returns without touching item when both
// inputs are empty. Each input is parsed only when non-empty, so first-time
// onboards (which legitimately have no sibling Dockerfile yet) are handled
// gracefully instead of aborting the pipeline.
func ParseOptionalFileInfo(item *workplan.WorkItem, dockerfile, makefile []byte) {
	if dockerfile == nil && makefile == nil {
		return
	}
	imageName := item.Naming.SpecImageName
	if len(dockerfile) > 0 {
		df, err := ParseDockerfile(dockerfile)
		if err != nil {
			log.Fatalf("❌ failed to parse Dockerfile: %v", err)
		}
		df.Source = dockerfile
		item.BuildFiles.Dockerfile = df
	}
	if len(makefile) > 0 {
		mf, err := ParseMakefile(makefile, imageName)
		if err != nil {
			log.Fatalf("❌ failed to parse Makefile: %v", err)
		}
		mf.Source = makefile
		item.BuildFiles.Makefile = mf
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
