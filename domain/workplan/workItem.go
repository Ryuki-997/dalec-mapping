package workplan

import (
	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/repository"
	"dalec-mapping/domain/tags"
)

// WorkGroup is one pull request's worth of work: a single resolved tag
// applied to every component declared under the same group key in
// onboard.yml. Phase 1 fans each decoded onboard group across its matched
// tags, emitting one runtime WorkGroup per tag (each with its own PRID and
// its own list of per-component WorkComponents). All shared group-level metadata
// (Repository, TagPatterns, Targets, License, Reviewers) lives here exactly
// once; per-component helpers read it via component.ParentGroup.<field> rather
// than duplicating it onto every component.
//
// The resolved tag itself does NOT live here — it is per-component (component.Tag),
// consistent with the fact that a tag is a property of the component being
// built, not of the PR boundary. Siblings under one runtime WorkGroup
// happen to share the same Tag value as a consequence of fan-out, but that
// is not modeled here.
//
// Components is the authoritative iteration target for Phase 2 and Phase 3 —
// one *WorkComponent per component, all sharing this group's tag. On the
// transient decoded shape returned by workplan.Decode, Components holds
// skeleton WorkComponents (only Name/DockerfileDir/MakefileDir populated, no
// Tag/Naming/Group); Phase 1 fan-out clones them into per-tag runtime
// WorkGroups with fully-populated components.
type WorkGroup struct {
	// Identity
	GroupName string `yaml:"-"`
	PRID      string `yaml:"-"`

	// Group-level metadata (decoded once from onboard.yml, shared by every
	// component/component in this group).
	Repository  string        `yaml:"repository"`
	TagPatterns tags.Patterns `yaml:"tags"`
	Targets     []string      `yaml:"targets"`
	License     string        `yaml:"license,omitempty"`
	Reviewers   []string      `yaml:"reviewers,omitempty"`

	// Per-component work units. Skeletons after workplan.Decode (Name +
	// DockerfileDir + MakefileDir only); fully populated by Phase 1.
	Components []*WorkComponent `yaml:"-"`
}

// WorkComponent is the per-component unit processed by the pipeline within a
// single per-tag WorkGroup. It also carries the component identity that
// used to live on a separate WorkComponent type — Name, DockerfileDir, and
// MakefileDir are populated by workplan.Decode and are immutable
// afterwards.
//
//   - Identity (Name/DockerfileDir/MakefileDir + Naming, Tag, Revision,
//     BuildFiles) is populated by Phase 1 and is read-only afterwards.
//     Phase 1 calls Naming.Construct exactly once per component with the
//     enclosing group's PRID so every Generated field — including
//     BranchName/PRTitle — is final at the end of Phase 1.
//   - Result is the only field Phase 2 writes; it carries the BuildResult
//     produced for this component. Phase 3 and observers read Result and treat
//     every other field as immutable.
//
// ParentGroup is the back-pointer to the enclosing runtime WorkGroup.
// Downstream helpers read shared metadata via component.ParentGroup.Repository,
// component.ParentGroup.Targets, component.ParentGroup.License rather than
// duplicating those values onto every component.
//
// NOTE: The onboard.yml may contain an optional "mar" section with
// publishing metadata (contactEmail, logoUrl, displayName, description,
// discoveryPortalReadme). That section is intentionally excluded here —
// it is consumed by ADO pipelines for MAR (Microsoft Artifact Registry)
// publishing and has no relevance to specfile generation. yaml.v3
// silently discards it during Decode.
type WorkComponent struct {
	// Component identity (populated by workplan.Decode). Name is the
	// component's onboard.yml key (the inner key in `components:` for a
	// grouped layout, or the group's own key for a standalone layout).
	Name          string `yaml:"-"`
	DockerfileDir string `yaml:"dockerfile"`
	MakefileDir   string `yaml:"makefile"`

	// Back-pointer to the runtime WorkGroup. Zero on decoded skeletons; set
	// by Phase 1 fan-out.
	ParentGroup *WorkGroup `yaml:"-"`

	// Per-component runtime data (populated by Phase 1 fan-out).
	Naming     naming.Naming  `yaml:"-"`
	Tag        tags.TagSet    `yaml:"-"`
	Revision   int            `yaml:"-"`
	BuildFiles BuildFilesInfo `yaml:"-"`

	Result buildresult.BuildResult `yaml:"-"`
}

// BuildFilesInfo holds everything derived from the partner repo at a specific
// tag: raw source bytes, the parsed Dockerfile/Makefile metadata, upstream
// repository information, and the static build values extracted from the
// Dockerfile. Populated incrementally by Phase 2 sub-steps.
type BuildFilesInfo struct {
	Dockerfile contents.DockerfileInfo // parsed Dockerfile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Makefile   contents.MakefileInfo   // parsed Makefile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Spec       contents.DockerSpec     // populated once by spec.GenerateSpec via parser.ExtractStaticBuildValues; read-only afterwards
	RepoInfo   repository.RepoInfo     // populated once by spec.GenerateSpec via buildRepoInfo; read-only afterwards
}
