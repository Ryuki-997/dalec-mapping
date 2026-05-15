package onboarding

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
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
//   - onboardDir: parent directory of the onboard.yml (e.g. "specs/containernetworking")
//   - specRepository: partner name used in specfile content (e.g. "containernetworking")
func (f *OnboardFile) Flatten(onboardDir, specRepository string) []ComponentConfig {
	var results []ComponentConfig

	// When the onboard file has a single standalone component whose name
	// matches the repository folder, clear SpecRepository so paths and
	// spec output use just the image name (no redundant prefix).
	singleStandalone := len(f.Standalone) == 1 && len(f.Groups) == 0

	for name, cfg := range f.Standalone {
		cfg.SpecImageName = name
		if singleStandalone && name == specRepository {
			cfg.SpecRepository = ""
			cfg.OnboardDir = onboardDir
		} else {
			cfg.SpecRepository = specRepository
			cfg.OnboardDir = fmt.Sprintf("%s/%s", onboardDir, name)
		}
		results = append(results, cfg)
	}

	for groupName, group := range f.Groups {
		for name, cfg := range group {
			cfg.SpecImageName = name
			cfg.SpecRepository = specRepository
			cfg.OnboardDir = fmt.Sprintf("%s/%s", onboardDir, name)
			cfg.GroupName = groupName
			results = append(results, cfg)
		}
	}

	return results
}

// ─── Tag representations ─────────────────────────────────────────────────────

// TagSet holds all derived representations of a single resolved tag.
type TagSet struct {
	// Pattern is the customer-provided regex pattern that matched this tag (e.g. "azure-ipam/v0\\.4\\..*").
	Pattern string

	// Full is the resolved tag string from GitHub (e.g. "azure-ipam/v0.4.0").
	Full string

	// Stripped is the short semver form with v prefix (e.g. "v0.4.0").
	Stripped string

	// Version is the pure numeric semver without v prefix (e.g. "0.4.0").
	Version string

	// Revision is the next spec revision number for this tag (e.g. 1, 2, 3).
	Revision int
}

// NewTagSet constructs a TagSet from a full tag, its matching pattern, and stripped form.
// Derives the version (X.Y.Z) by trimming the v prefix from the stripped tag.
func NewTagSet(fullTag, pattern, strippedTag string, revision int) TagSet {
	version := strings.TrimPrefix(strippedTag, "v")
	return TagSet{
		Pattern:  pattern,
		Full:     fullTag,
		Stripped: strippedTag,
		Version:  version,
		Revision: revision,
	}
}

// ComponentState pairs a component config with its resolved tag.
// Used as the shared input to naming resolution and pipeline iteration.
type ComponentState struct {
	Onboard *ComponentConfig
	Tag     TagSet
}

// ComponentConfig represents a single component both in the YAML onboard file
// and at runtime throughout the pipeline.
type ComponentConfig struct {
	Repository    string     `yaml:"repository"`
	TagPatterns   []string   `yaml:"tags"`
	Targets       []string   `yaml:"targets"`
	Reviewers     []string `yaml:"reviewers,omitempty"`
	DockerfileDir string   `yaml:"dockerfile"`
	MakefileDir   string     `yaml:"makefile"`
	License       string   `yaml:"license,omitempty"`

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
