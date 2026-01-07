package transformer

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Permission uint32

// MarshalYAML outputs the permission as an octal literal without quotes
func (p Permission) MarshalYAML() (interface{}, error) {
	node := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: fmt.Sprintf("0%o", p),
	}
	return node, nil
}

func TestCheckFiles(path string, permission os.FileMode) map[string]interface{} {
	return map[string]interface{}{
		"name": "Check files",
		"files": map[string]interface{}{
			"/usr/bin/" + path: map[string]interface{}{
				"permissions": Permission(permission),
			},
		},
	}
}
