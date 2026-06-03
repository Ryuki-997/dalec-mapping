package naming

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/tags"
)

// Naming carries every piece of identifying or derived data for a single
// component+tag combination through the pipeline. It is organised in three
// sections so the data flow is explicit:
//
//   - Embedded ComponentConfig — the immutable YAML fields parsed from the
//     partner's onboard.yml (Repository, Targets, License, ...).
//   - Runtime — values populated by Phase 1 when an onboard file is walked
//     per-component (OnboardDir, SpecImageName, SpecRepository, GroupName).
//   - Generated — values produced by Construct (and WithPRID) from the
//     embedded + runtime sections (DisplayName, FolderPath, BranchName, ...).
type Naming struct {
	// ─── Embedded: YAML config from onboard.yml ─────────────────────────────
	onboarding.ComponentConfig

	// ─── Runtime: populated by Phase 1 per-component walk ───────────────────

	// OnboardDir is the component's directory in the spec repo
	// (e.g. "specs/containernetworking/azure-cns").
	OnboardDir string

	// SpecImageName is the component's image name as it appears in the
	// onboard.yml mapping key (e.g. "azure-cns").
	SpecImageName string

	// SpecRepository is the partner name used in specfile content
	// (e.g. "containernetworking"). Empty for solo standalone components.
	SpecRepository string

	// GroupName is the group key from onboard.yml; empty for standalone components.
	GroupName string

	// ─── Generated: filled by Construct / WithPRID ──────────────────────────

	DisplayName     string // GroupName if grouped, else SpecImageName
	VersionRevision string // e.g. "0.0.1-1"
	FolderPath      string // OnboardDir with "specs/" prefix stripped
	SpecFilePath    string // e.g. "specs/aks-node-controller/aks-node-controller-0.0.1-1-specfile.yml"
	BranchName      string // e.g. "dalec/containernetworking/azure-ipam/0.0.1-1/20260505-72c644"
	PRTitle         string // e.g. "[Dalec][20260505-72c644] aks-node-controller @ 0.0.1-1"
}

// Construct fills the generated section in-place from the embedded
// ComponentConfig and the runtime section using the resolved tag. BranchName
// and PRTitle remain empty until WithPRID is called.
//
// Spec file paths and version labels use the numeric semver (no "v" prefix)
// to match the remote spec repo's storage convention.
func (n *Naming) Construct(tagSet tags.Set) {
	n.VersionRevision = fmt.Sprintf("%s-%d", tagSet.Version, tagSet.Revision)

	n.DisplayName = n.SpecImageName
	if n.GroupName != "" {
		n.DisplayName = n.GroupName
	}

	n.FolderPath = n.deriveFolderPath()
	n.SpecFilePath = fmt.Sprintf("%s/%s-%s-specfile.yml", n.OnboardDir, n.SpecImageName, n.VersionRevision)
}

// WithPRID returns a copy of the Naming with BranchName and PRTitle populated
// from the given prID.
func (n Naming) WithPRID(prID string) Naming {
	n.BranchName = fmt.Sprintf("dalec/%s/%s/%s", n.FolderPath, n.VersionRevision, prID)
	n.PRTitle = fmt.Sprintf("[Dalec][%s] %s @ %s", prID, n.DisplayName, n.VersionRevision)
	return n
}

// deriveFolderPath computes the branch folder path from OnboardDir by stripping
// the "specs/" prefix. For grouped components, the trailing component name is
// removed so the branch represents the group-level folder.
//
// Examples:
//
//	standalone single:  OnboardDir="specs/aks-node-controller"            → "aks-node-controller"
//	standalone multi:   OnboardDir="specs/containernetworking/azure-ipam" → "containernetworking/azure-ipam"
//	grouped component:  OnboardDir="specs/containernetworking/azure-cns"  → "containernetworking"
func (n *Naming) deriveFolderPath() string {
	folderPath := strings.TrimPrefix(n.OnboardDir, "specs/")
	if n.GroupName != "" {
		folderPath = strings.TrimSuffix(folderPath, "/"+n.SpecImageName)
	}
	return folderPath
}

// GeneratePRID returns a unique run identifier in the form YYYYMMDD-xxxxxx
// where xxxxxx is 6 random hex characters.
func GeneratePRID() string {
	date := time.Now().UTC().Format("20060102")
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		nanos := time.Now().UnixNano()
		randomBytes = []byte{byte(nanos), byte(nanos >> 8), byte(nanos >> 16)}
	}
	return date + "-" + hex.EncodeToString(randomBytes)
}
