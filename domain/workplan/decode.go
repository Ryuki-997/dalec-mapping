package workplan

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/onboarding"

	"gopkg.in/yaml.v3"
)

// Decode parses a partner onboard.yml file into a list of WorkItemGroups.
// Each top-level YAML key becomes one WorkItemGroup with GroupName set to
// that key and PRID left empty. Items contains one *WorkItem per declared
// component, with only WorkItem.Component populated (Naming, Tag,
// BuildFiles, Result are all zero-valued).
//
//   - Standalone components (value has a "repository" field) → group with
//     one WorkItem; the inner component's Name equals the group's Name.
//   - Grouped components (value is a map of name → OnboardingComponent) →
//     group with N WorkItems; each component's Name is its inner YAML key.
//
// Targets are validated inline; components with no supported targets are
// dropped, and groups that end up empty after that filter are skipped.
// Downstream stages (partnerrepo.ResolveTagCache, specrepo.FetchComponents)
// fan out each component WorkItem into per-tag WorkItems, fill Naming, and
// mint PRID + call Naming.Construct.
func Decode(raw []byte) ([]WorkItemGroup, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("failed to unmarshal onboard file: %w", err)
	}

	mapping := unwrapMapping(&root)
	if mapping == nil {
		return nil, fmt.Errorf("onboard file must be a YAML mapping")
	}

	var groups []WorkItemGroup
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		valNode := mapping.Content[i+1]
		groupName := keyNode.Value

		group, err := decodeGroup(groupName, valNode)
		if err != nil {
			return nil, err
		}
		if len(group.Items) == 0 {
			continue
		}
		groups = append(groups, group)
	}

	return groups, nil
}

// unwrapMapping returns the mapping node at the root, walking through the
// optional yaml.DocumentNode wrapper.
func unwrapMapping(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		return unwrapMapping(root.Content[0])
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// decodeGroup turns one top-level (key, value) pair into a WorkItemGroup,
// auto-detecting standalone vs grouped layout.
func decodeGroup(groupName string, valNode *yaml.Node) (WorkItemGroup, error) {
	standalone, ok := decodeStandalone(groupName, valNode)
	if ok {
		return standalone, nil
	}

	return decodeGrouped(groupName, valNode)
}

// decodeStandalone tries to decode valNode as a single OnboardingComponent.
// Returns ok=true only when the value parses AND has a non-empty Repository
// (which is what distinguishes a standalone component from a grouped map).
func decodeStandalone(groupName string, valNode *yaml.Node) (WorkItemGroup, bool) {
	var comp onboarding.OnboardingComponent
	if err := valNode.Decode(&comp); err != nil || comp.Repository == "" {
		return WorkItemGroup{}, false
	}

	comp.Targets = validateTargets(groupName, comp.Targets)
	if len(comp.Targets) == 0 {
		return WorkItemGroup{GroupName: groupName}, true
	}
	comp.Name = groupName
	return WorkItemGroup{
		GroupName: groupName,
		Items:     []*WorkItem{{Component: comp}},
	}, true
}

// decodeGrouped decodes valNode as map[string]OnboardingComponent (the
// grouped layout). Components are emitted in sorted-key order for stable
// output across runs.
func decodeGrouped(groupName string, valNode *yaml.Node) (WorkItemGroup, error) {
	var raw map[string]onboarding.OnboardingComponent
	if err := valNode.Decode(&raw); err != nil {
		return WorkItemGroup{}, fmt.Errorf("key %q is neither a component (no repository) nor a valid group: %w", groupName, err)
	}

	componentNames := make([]string, 0, len(raw))
	for name := range raw {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)

	group := WorkItemGroup{GroupName: groupName}
	for _, componentName := range componentNames {
		cfg := raw[componentName]
		cfg.Targets = validateTargets(componentName, cfg.Targets)
		if len(cfg.Targets) == 0 {
			continue
		}
		cfg.Name = componentName
		group.Items = append(group.Items, &WorkItem{Component: cfg})
	}
	return group, nil
}

// validateTargets keeps only known build targets from the onboard list.
// Unsupported targets are logged and skipped. A component with no valid
// targets after filtering returns an empty slice and the caller drops the
// component.
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
