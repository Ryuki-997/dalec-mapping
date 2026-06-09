package workplan

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"dalec-mapping/domain/contents"

	"gopkg.in/yaml.v3"
)

// componentsKey is the YAML key under which grouped onboard entries declare
// their per-component build-file paths. Its presence under a top-level group
// node is what distinguishes the grouped layout from the standalone layout;
// see Decode for details.
const componentsKey = "components"

// Decode parses a partner onboard.yml file into a slice of decoded
// WorkGroups. Each top-level YAML key becomes one WorkGroup. The decoded
// WorkGroup is a transient shape: GroupName, group-level metadata
// (Repository/TagPatterns/Targets/License/Reviewers), and the Components slice
// of skeleton *WorkComponent (Name/DockerfileDir/MakefileDir only) are
// populated; Tag/PRID and per-component Naming/Tag/Revision/Group remain
// zero-valued for the Phase 1 fan-out (partnerrepo.ResolveTagCache) to
// fill — one runtime WorkGroup per resolved tag, with fully-populated
// per-component WorkComponents.
//
// Two onboard shapes are accepted, disambiguated by the presence of a
// "components:" mapping under the group key:
//
//   - Grouped layout (components: present)
//     Group-level metadata sits at the top of the group; an explicit
//     `components:` map enumerates the per-component build files.
//
//   - Standalone layout (components: absent)
//     Group-level metadata sits at the top of the group; `dockerfile:`
//     and `makefile:` are declared inline at the same level. The group's
//     own key is used as the single skeleton component's Name.
//
// Skeleton components are emitted in sorted-key order for stable output. Groups
// whose group-level Targets list resolves empty (no supported targets) are
// dropped here so downstream phases never see them.
func Decode(raw []byte) ([]WorkGroup, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("failed to unmarshal onboard file: %w", err)
	}

	mapping := unwrapMapping(&root)
	if mapping == nil {
		return nil, fmt.Errorf("onboard file must be a YAML mapping")
	}

	var groups []WorkGroup
	for keyIndex := 0; keyIndex+1 < len(mapping.Content); keyIndex += 2 {
		keyNode := mapping.Content[keyIndex]
		valNode := mapping.Content[keyIndex+1]
		groupName := keyNode.Value

		group, err := decodeGroup(groupName, valNode)
		if err != nil {
			return nil, err
		}
		if len(group.Components) == 0 {
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

// decodeGroup turns one top-level (key, value) pair into a WorkGroup. The
// shape is chosen by structural inspection: a nested `components:` mapping
// triggers the grouped path, otherwise the standalone path is used.
func decodeGroup(groupName string, valNode *yaml.Node) (WorkGroup, error) {
	if valNode.Kind != yaml.MappingNode {
		return WorkGroup{}, fmt.Errorf("group %q: expected mapping, got %v", groupName, valNode.Kind)
	}

	// Decode group-level fields (Repository, TagPatterns, Targets, License, Reviewers).
	var group WorkGroup
	if err := valNode.Decode(&group); err != nil {
		return WorkGroup{}, fmt.Errorf("group %q: failed to decode metadata: %w", groupName, err)
	}
	group.GroupName = groupName

	group.Targets = validateTargets(groupName, group.Targets)
	if len(group.Targets) == 0 {
		return WorkGroup{GroupName: groupName}, nil
	}

	componentsNode := findChildMapping(valNode, componentsKey)
	if componentsNode != nil {
		components, err := decodeGroupedComponents(groupName, componentsNode)
		if err != nil {
			return WorkGroup{}, err
		}
		group.Components = components
		return group, nil
	}

	// Standalone layout: decode valNode as a single inline component whose Name
	// equals the group's name. Group-level keys decoded above are silently
	// ignored here because WorkComponent does not declare those fields.
	var standalone WorkComponent
	if err := valNode.Decode(&standalone); err != nil {
		return WorkGroup{}, fmt.Errorf("group %q: failed to decode standalone component: %w", groupName, err)
	}
	standalone.Name = groupName
	group.Components = []*WorkComponent{&standalone}
	return group, nil
}

// findChildMapping returns the value node of the first child whose key
// equals childKey and whose value is a mapping node, or nil when no such
// child exists. Used to detect the optional `components:` map.
func findChildMapping(mapping *yaml.Node, childKey string) *yaml.Node {
	for keyIndex := 0; keyIndex+1 < len(mapping.Content); keyIndex += 2 {
		if mapping.Content[keyIndex].Value != childKey {
			continue
		}
		valNode := mapping.Content[keyIndex+1]
		if valNode.Kind != yaml.MappingNode {
			continue
		}
		return valNode
	}
	return nil
}

// decodeGroupedComponents decodes the `components:` map into a sorted slice of
// skeleton *WorkComponent. Each component carries its key as Name; entries with
// invalid YAML fail the whole group.
func decodeGroupedComponents(groupName string, componentsNode *yaml.Node) ([]*WorkComponent, error) {
	var raw map[string]WorkComponent
	if err := componentsNode.Decode(&raw); err != nil {
		return nil, fmt.Errorf("group %q: failed to decode components map: %w", groupName, err)
	}

	componentNames := make([]string, 0, len(raw))
	for name := range raw {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)

	components := make([]*WorkComponent, 0, len(componentNames))
	for _, componentName := range componentNames {
		skeleton := raw[componentName]
		skeleton.Name = componentName
		copyOfSkeleton := skeleton
		components = append(components, &copyOfSkeleton)
	}
	return components, nil
}

// validateTargets keeps only known build targets from the onboard list.
// Unsupported targets are logged and skipped. A group with no valid targets
// after filtering returns an empty slice and the caller drops the group.
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
