package onboarding

import "dalec-mapping/domain/tags"

// OnboardingComponent is the immutable YAML representation of a single component
// inside an onboard.yml file. It carries only fields the partner declares
// in YAML — no runtime state, no cached file bytes, no derived names.
//
// Group identity lives on the enclosing workplan.WorkItemGroup
// (WorkItemGroup.GroupName), not here, so each component appears in exactly
// one place. Set by workplan.Decode from the inner YAML key (or, for
// standalone components, from the same key as the enclosing group).
//
// NOTE: The onboard.yml may contain an optional "mar" section with publishing
// metadata (contactEmail, logoUrl, displayName, description, discoveryPortalReadme).
// That section is intentionally excluded here — it is consumed by ADO pipelines
// for MAR (Microsoft Artifact Registry) publishing and has no relevance to
// specfile generation. yaml.v3 silently discards it during Decode.
type OnboardingComponent struct {
	// Name is the component's own onboard.yml key (the inner key in a group,
	// or the only key for a standalone component). Always non-empty after
	// workplan.Decode.
	Name string `yaml:"-"`

	Repository    string        `yaml:"repository"`
	TagPatterns   tags.Patterns `yaml:"tags"`
	Targets       []string      `yaml:"targets"`
	DockerfileDir string        `yaml:"dockerfile"`
	MakefileDir   string        `yaml:"makefile"`
	License       string        `yaml:"license,omitempty"`
}
