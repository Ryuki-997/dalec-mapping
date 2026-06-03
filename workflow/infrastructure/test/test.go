package test

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

// TestCheckFiles returns a test entry for the given target OS.
//
//   - azlinux3 (and other Linux targets): checks binaryPath; also checks symlinkPath when non-empty.
//   - windowscross: checks /Windows/System32/<binaryName>.exe; symlink is not applicable on Windows.
func TestCheckFiles(targetOS string, binaryName string, binaryPath string, symlinkPath string, permission os.FileMode) map[string]interface{} {
	files := make(map[string]interface{})
	var name string

	switch targetOS {
	case "windowscross":
		name = "Check windowsCross Files"
		files["/Windows/System32/"+binaryName+".exe"] = map[string]interface{}{
			"permissions": Permission(permission),
		}
	default:
		name = "Check " + targetOS + " Files"
		files[binaryPath] = map[string]interface{}{
			"permissions": Permission(permission),
		}
	}

	return map[string]interface{}{
		"name":  name,
		"files": files,
	}
}
