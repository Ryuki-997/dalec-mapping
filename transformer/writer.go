package transformer

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type PreviousDalecSpec struct {
	// Comparison Fields
	Commit string

	// Updatable Fields
	Revision string
}

// ReadYAML reads a DalecSpec file and unmarshal updated values
func ReadYAML(path string) (PreviousDalecSpec, error) {
	data := PreviousDalecSpec{}

	// TODO: populate fields from previous spec file.
	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return data, fmt.Errorf("failed to read file: %w", err)
	}

	// Unmarshal YAML content
	if err := yaml.Unmarshal(content, &data); err != nil {
		return data, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return data, nil
}

// WriteYAML converts DalecSpec to formatted YAML
func WriteYAML(spec DalecSpec) (string, error) {
	// Create buffer for output
	var buf bytes.Buffer

	// Handle syntax header specially (needs to be first, with special format)
	if syntax, ok := spec["# syntax"]; ok {
		buf.WriteString(fmt.Sprintf("# syntax=%v\n\n", syntax))

		// Create a copy without the syntax line for yaml encoding
		specCopy := make(DalecSpec)
		for k, v := range spec {
			if k != "# syntax" {
				specCopy[k] = v
			}
		}
		spec = specCopy
	}

	// Create YAML encoder with proper indentation
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	// Encode the spec
	if err := encoder.Encode(spec); err != nil {
		return "", fmt.Errorf("failed to encode YAML: %w", err)
	}

	encoder.Close()

	// Post-process YAML to match Dalec format conventions
	result := buf.String()
	result = formatDalecYAML(result)

	return result, nil
}

// formatDalecYAML applies Dalec-specific formatting
func formatDalecYAML(yamlStr string) string {
	lines := strings.Split(yamlStr, "\n")
	var formatted []string

	for i, line := range lines {
		// Add blank line before major sections
		if i > 0 && !strings.HasPrefix(line, " ") && line != "" {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine != "" && !strings.HasPrefix(lines[i-1], " ") {
				// Check if previous line was not empty and not indented
				if i > 0 && strings.TrimSpace(lines[i-1]) != "" {
					formatted = append(formatted, "")
				}
			}
		}

		formatted = append(formatted, line)
	}

	return strings.Join(formatted, "\n")
}

// MarshalYAML provides custom YAML marshaling for DalecSpec
func (spec DalecSpec) MarshalYAML() (interface{}, error) {
	// Return the map directly for standard marshaling
	return map[string]interface{}(spec), nil
}
