package onboarding

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ReviewMode controls how generated specs are delivered.
type ReviewMode string

const (
	ManualReview ReviewMode = "ManualReview" // Generate spec → test → create PR for approval (default)
	AutoReview   ReviewMode = "AutoReview"   // Generate spec → test → push directly to remote (no PR)
)

// ─── YAML-level types (partner-level onboard.yml) ────────────────────────────

// OnboardFile is the top-level structure of a partner's onboard.yml.
// Top-level keys are either:
//   - Standalone components: the value has a "repository" field
//   - Groups: the value is a map of component names → ComponentConfig
//
// The format auto-detects based on whether "repository" appears in the value.
type OnboardFile struct {
	Standalone map[string]ComponentConfig            // top-level entries with "repository"
	Groups     map[string]map[string]ComponentConfig // top-level entries that are groups of components
}

// UnmarshalYAML implements custom unmarshalling to auto-detect standalone
// components vs groups based on whether "repository" is present.
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
			f.Standalone[name] = comp
			continue
		}

		// Otherwise treat it as a group: map[string]ComponentConfig.
		var group map[string]ComponentConfig
		if err := valNode.Decode(&group); err != nil {
			return fmt.Errorf("key %q is neither a component (no repository) nor a valid group: %w", name, err)
		}
		f.Groups[name] = group
	}

	return nil
}

// Flatten converts an OnboardFile into a slice of ComponentConfig structs
// ready for the pipeline.
//   - onboardParentDir: parent directory of the onboard.yml (e.g. "specs/containernetworking")
//   - specRepository: partner name used in specfile content (e.g. "containernetworking")
func (f *OnboardFile) Flatten(onboardParentDir, specRepository string) []ComponentConfig {
	var results []ComponentConfig

	// When the onboard file has a single standalone component whose name
	// matches the repository folder, clear SpecRepository so paths and
	// spec output use just the image name (no redundant prefix).
	singleStandalone := len(f.Standalone) == 1 && len(f.Groups) == 0

	for name, cfg := range f.Standalone {
		cfg.SpecImageName = name
		if singleStandalone && name == specRepository {
			cfg.SpecRepository = ""
			cfg.OnboardDir = onboardParentDir
		} else {
			cfg.SpecRepository = specRepository
			cfg.OnboardDir = fmt.Sprintf("%s/%s", onboardParentDir, name)
		}
		results = append(results, cfg)
	}

	for groupName, group := range f.Groups {
		for name, cfg := range group {
			cfg.SpecImageName = name
			cfg.SpecRepository = specRepository
			cfg.OnboardDir = fmt.Sprintf("%s/%s", onboardParentDir, name)
			cfg.GroupName = groupName
			results = append(results, cfg)
		}
	}

	return results
}

// ComponentConfig represents a single component both in the YAML onboard file
// and at runtime throughout the pipeline.
type ComponentConfig struct {
	Repository    string     `yaml:"repository"`
	Tag           []string   `yaml:"tags"`
	Targets       []string   `yaml:"targets"`
	Reviewers     []string   `yaml:"reviewers,omitempty"`
	ReviewMode    ReviewMode `yaml:"reviewMode,omitempty"`
	DockerfileDir string     `yaml:"dockerfile"`
	MakefileDir   string     `yaml:"makefile"`

	// Runtime fields (set during Flatten / pipeline, not from YAML).
	OnboardDir     string `yaml:"-"` // component directory derived from onboard.yml location (e.g. "specs/containernetworking/azure-cns")
	SpecImageName  string `yaml:"-"`
	SpecRepository string `yaml:"-"` // partner name for specfile content (e.g. "containernetworking")
	GroupName      string `yaml:"-"` // group name from onboard.yml (empty for standalone components)

	// Dockerfile/Makefile content (cached from onboard repo in step1, then overwritten with fresh content in step2).
	DockerfileContent []byte `yaml:"-"`
	MakefileContent   []byte `yaml:"-"`
}

// SpecDir returns the component's directory path in the onboard repo.
// Derived from the onboard.yml location, not hardcoded.
func (c *ComponentConfig) SpecDir() string {
	return c.OnboardDir
}

// SpecLeaf returns the leaf path segment: "specRepo/specImage" or just "specImage".
func (c *ComponentConfig) SpecLeaf() string {
	if c.SpecRepository != "" {
		return fmt.Sprintf("%s/%s", c.SpecRepository, c.SpecImageName)
	}
	return c.SpecImageName
}