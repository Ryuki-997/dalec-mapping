package transformer

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type PreviousDalecSpec struct {
	Args struct {
		Version  string `yaml:"VERSION"`
		Revision int    `yaml:"REVISION"`
	} `yaml:"args"`
}

// ReadYAML reads a DalecSpec file and unmarshal updated values
func ReadYAML(path string) (PreviousDalecSpec, error) {
	data := PreviousDalecSpec{}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return data, fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal YAML content
	if err := yaml.Unmarshal(content, &data); err != nil {
		return data, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	fmt.Printf("Version: %v, Revision: %v\n", data.Args.Version, data.Args.Revision)

	return data, nil
}

// WriteYAML converts DalecSpec to formatted YAML
func WriteYAML(spec DalecSpec) (string, error) {
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
			fmt.Printf("Key %s not found, skipping\n", key)
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

	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(rootNode); err != nil {
		return "", fmt.Errorf("failed to encode YAML: %w", err)
	}
	encoder.Close()

	result := buf.String()

	result = strings.TrimPrefix(result, "---\n")

	return result, nil
}

// MarshalYAML provides custom YAML marshaling for DalecSpec
func (spec DalecSpec) MarshalYAML() (interface{}, error) {
	// Return the map directly for standard marshaling
	return map[string]interface{}(spec), nil
}
