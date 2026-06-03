package onboarding

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/contents"

	"gopkg.in/yaml.v3"
)

// OnboardFile is the top-level structure of a partner's onboard.yml.
// Top-level keys are either:
//   - Standalone components: the value has a "repository" field
//   - Groups: the value is a map of component names → ComponentConfig
//
// The format auto-detects based on whether "repository" appears in the value.
// Targets are validated during unmarshalling — components with no supported
// targets are dropped from the resulting maps.
type OnboardFile struct {
	Standalone map[string]ComponentConfig            // top-level entries with "repository"
	Groups     map[string]map[string]ComponentConfig // top-level entries that are groups of components
}

// UnmarshalYAML implements custom unmarshalling to auto-detect standalone
// components vs groups based on whether "repository" is present. Targets on
// each decoded ComponentConfig are validated inline; components that end up
// with no valid targets are skipped with a warning.
func (f *OnboardFile) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("onboard file must be a YAML mapping")
	}

	f.Standalone = make(map[string]ComponentConfig)
	f.Groups = make(map[string]map[string]ComponentConfig)

	for i := 0; i+1 < len(value.Content); i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		name := keyNode.Value

		// Try decoding as a single ComponentConfig first.
		var comp ComponentConfig
		if err := valNode.Decode(&comp); err == nil && comp.Repository != "" {
			comp.Targets = validateTargets(name, comp.Targets)
			if len(comp.Targets) == 0 {
				continue
			}
			f.Standalone[name] = comp
			continue
		}

		// Otherwise treat it as a group: map[string]ComponentConfig.
		var group map[string]ComponentConfig
		if err := valNode.Decode(&group); err != nil {
			return fmt.Errorf("key %q is neither a component (no repository) nor a valid group: %w", name, err)
		}
		validatedGroup := make(map[string]ComponentConfig, len(group))
		for componentName, cfg := range group {
			cfg.Targets = validateTargets(componentName, cfg.Targets)
			if len(cfg.Targets) == 0 {
				continue
			}
			validatedGroup[componentName] = cfg
		}
		if len(validatedGroup) == 0 {
			continue
		}
		f.Groups[name] = validatedGroup
	}

	return nil
}

// validateTargets keeps only known build targets from the onboard list.
// Unsupported targets are logged and skipped. A component with no valid
// targets after filtering returns an empty slice and the caller should
// drop the component.
func validateTargets(name string, raw []string) []string {
	var resolved []string
	for _, target := range raw {
		target = strings.TrimSpace(target)
		if _, ok := contents.IsValidTarget(target); ok {
			resolved = append(resolved, target)
			continue
		}
		log.Printf("⚠️  Ignoring unsupported onboard target %q for %s\n", target, name)
	}
	if len(resolved) == 0 {
		log.Printf("⚠️  Skipping %s: no valid targets in onboard.yml\n", name)
	}
	return resolved
}
