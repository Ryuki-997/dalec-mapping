package parser

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type DalecSpecWriter struct{}

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
type DalecSpec map[string]interface{}

type PreviousDalecSpec struct {
	Args struct {
		Version  string `yaml:"VERSION"`
		Revision int    `yaml:"REVISION"`
	} `yaml:"args"`
}

// ReadYAML reads a DalecSpec file and unmarshal updated values
func (w *DalecSpecWriter) ReadYAML(path string) (PreviousDalecSpec, error) {
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

	log.Printf("Version: %v, Revision: %v\n", data.Args.Version, data.Args.Revision)

	return data, nil
}

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
			log.Printf("Key %s not found, skipping\n", key)
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

	// Write to output file if path is provided
	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(result), 0644); err != nil {
			return "", fmt.Errorf("failed to write YAML to file: %w", err)
		}
	}

	return result, nil
}
