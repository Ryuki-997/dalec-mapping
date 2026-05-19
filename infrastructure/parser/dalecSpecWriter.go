package parser

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type DalecSpecWriter struct{}

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
type DalecSpec map[string]interface{}


// WriteYAML converts DalecSpec to formatted YAML
func (w *DalecSpecWriter) WriteYAML(spec DalecSpec, outputPath string) (string, error) {
	var buf bytes.Buffer

	// Handle syntax header specially (needs to be first, with special format)
	if syntax, ok := spec["# syntax"]; ok {
		buf.WriteString(fmt.Sprintf("# syntax=%v\n", syntax))
	}

	// Define the order of top-level fields to match tmp.yml
	fieldOrder := []string{
		"args",
		"name",
		"packager",
		"vendor",
		"license",
		"website",
		"description",
		"version",
		"revision",
		"x-build-extensions",
		"sources",
		"dependencies",
		"targets",
		"build",
		"artifacts",
		"image",
		"tests",
	}

	rootNode := &yaml.Node{
		Kind: yaml.MappingNode,
	}

	for _, key := range fieldOrder {
		value, ok := spec[key]
		if !ok {
			continue
		}

		keyNode := &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: key,
		}

		var valueNode yaml.Node
		if err := valueNode.Encode(value); err != nil {
			return "", fmt.Errorf("failed to encode field %s: %w", key, err)
		}

		rootNode.Content = append(rootNode.Content, keyNode, &valueNode)
	}

	out, err := yaml.Marshal(rootNode)
	if err != nil {
		return "", fmt.Errorf("failed to marshal YAML: %w", err)
	}

	buf.Write(out)
	result := buf.String()

	result = strings.TrimPrefix(result, "---\n")

	// Write to output file if path is provided
	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
			return "", fmt.Errorf("failed to write YAML to file: %w", err)
		}
	}

	return result, nil
}
