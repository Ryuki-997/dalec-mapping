package naming

import (
	"fmt"
	"strings"

	"dalec-mapping/domain/tags"
)

// Naming carries every piece of identifying or derived data for a single
// component+tag combination through the pipeline. It is organised in three
// sections so the data flow is explicit:
//
//   - Runtime — the sole input populated externally during Phase 1 when an
//     onboard file is walked per-component (OnboardDir).
//   - Atomic — the smallest derived naming units (SpecRepository,
//     SpecImageName). Populated by DeriveAtomic from OnboardDir. These are
//     the canonical names used by logs and by code that needs partner /
//     component identity without the rest of the generated set.
//   - Generated — every value produced by a single Construct call from the
//     atomic section + the resolved tag + the group's name + the group's PRID.
type Naming struct {
	// ─── Runtime: populated by Phase 1 per-component walk ───────────────────

	// OnboardDir is the component's directory in the spec repo
	// (e.g. "specs/containernetworking/azure-cns").
	OnboardDir string

	// ─── Atomic: smallest derived naming units (populated by DeriveAtomic) ──
	// e.g. "containernetworking" / "azure-cns" for OnboardDir
	// "specs/containernetworking/azure-cns".

	// SpecRepository is the partner path under the first "specs/" segment
	// in OnboardDir with the leaf component segment stripped (e.g.
	// "containernetworking" for OnboardDir
	// "specs/containernetworking/azure-cns", or "aks/fleet" for
	// "specs/aks/fleet/fleet-networking"). For single-segment partner
	// folders this equals the SpecImageName. Empty when OnboardDir lacks
	// a "specs/" anchor — callers should treat that as a skip signal.
	SpecRepository string

	// SpecImageName is the component's leaf folder name
	// (e.g. "azure-cns"). Matches the onboard.yml mapping key.
	SpecImageName string

	// ─── Generated: filled by Construct ─────────────────────────────────────

	DisplayName     string // GroupName supplied to Construct (equals SpecImageName for standalone components)
	VersionRevision string // e.g. "0.0.1-1"
	SpecFileName    string // e.g. "aks-node-controller-0.0.1-1-specfile.yml" (<SpecImageName>-<VersionRevision>-specfile.yml)
	FolderPath      string // OnboardDir with "specs/" prefix stripped (and component leaf stripped for grouped components)
	SpecFilePath    string // e.g. "specs/aks-node-controller/aks-node-controller-0.0.1-1-specfile.yml"
	BranchName      string // e.g. "dalec/containernetworking/azure-ipam/0.0.1-1/20260505-72c644"
	PRTitle         string // e.g. "[Dalec][20260505-72c644] aks-node-controller @ 0.0.1-1"
}

// DeriveAtomic fills the atomic section in-place from OnboardDir, anchored
// on the first "specs/" segment. SpecImageName is the last path segment
// after that anchor; SpecRepository is the remainder (everything between
// the anchor and the leaf, joined with "/"). For OnboardDirs with a single
// segment after the anchor, SpecRepository equals SpecImageName. When the
// anchor is missing, both fields are left empty so the caller can skip the
// component. Safe to call repeatedly.
func (n *Naming) DeriveAtomic() {
	const anchor = "specs/"
	anchorIdx := strings.Index(n.OnboardDir, anchor)
	if anchorIdx < 0 {
		n.SpecRepository = ""
		n.SpecImageName = ""
		return
	}

	relative := n.OnboardDir[anchorIdx+len(anchor):]
	segments := strings.Split(relative, "/")
	specImageName := segments[len(segments)-1]
	specRepository := specImageName
	if len(segments) >= 2 {
		specRepository = strings.Join(segments[:len(segments)-1], "/")
	}

	n.SpecRepository = specRepository
	n.SpecImageName = specImageName
}

// Construct fills the entire generated section in-place from the atomic
// section, the resolved tag, the work component's revision, the component's
// group name, and the group's PRID. groupName is the onboard.yml group
// key for grouped components, or the component name for standalone
// components — it is always non-empty. prID is minted once per WorkGroup
// in Phase 1 and shared by every component in that group, so BranchName/PRTitle
// collapse onto one pull request.
//
// Called once per component from Phase 2's resolveAction, after the action
// branch is decided and the component's Revision is finalized. Phase 1
// leaves every Generated field empty; Phase 3 reads them but never mutates.
//
// Spec file paths and version labels use the numeric semver (no "v" prefix)
// to match the remote spec repo's storage convention.
func (n *Naming) Construct(tag tags.TagSet, revision int, groupName, prID string) {
	n.VersionRevision = fmt.Sprintf("%s-%d", tag.Version, revision)
	n.DisplayName = groupName
	n.SpecFileName = fmt.Sprintf("%s-%s-specfile.yml", n.SpecImageName, n.VersionRevision)
	n.FolderPath = n.deriveFolderPath(groupName)
	n.SpecFilePath = fmt.Sprintf("%s/%s", n.OnboardDir, n.SpecFileName)
	n.BranchName = fmt.Sprintf("dalec/%s/%s/%s", n.FolderPath, n.VersionRevision, prID)
	n.PRTitle = fmt.Sprintf("[Dalec][%s] %s @ %s", prID, n.DisplayName, n.VersionRevision)
}

// deriveFolderPath computes the branch folder path from OnboardDir by stripping
// the "specs/" prefix. For grouped components (groupName != SpecImageName),
// the trailing component name is removed so the branch represents the
// group-level folder.
//
// Examples:
//
//	standalone single:  OnboardDir="specs/aks-node-controller"            → "aks-node-controller"
//	standalone multi:   OnboardDir="specs/containernetworking/azure-ipam" → "containernetworking/azure-ipam"
//	grouped component:  OnboardDir="specs/containernetworking/azure-cns"  → "containernetworking"
func (n *Naming) deriveFolderPath(groupName string) string {
	folderPath := strings.TrimPrefix(n.OnboardDir, "specs/")
	if groupName != n.SpecImageName {
		folderPath = strings.TrimSuffix(folderPath, "/"+n.SpecImageName)
	}
	return folderPath
}

