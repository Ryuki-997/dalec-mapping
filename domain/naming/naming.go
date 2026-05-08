package naming

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"dalec-mapping/domain/onboarding"
)

// Naming holds all derived name/path values for a single component+tag combination.
// Populated once after grouping and consumed by downstream steps (PR creation, logging).
type Naming struct {
	DisplayName     string // GroupName if grouped, else SpecImageName
	VersionRevision string // e.g. "0.0.1-1"
	FolderPath      string // OnboardDir with "specs/" prefix stripped (e.g. "containernetworking/azure-ipam")
	SpecFilePath    string // e.g. "specs/aks-node-controller/aks-node-controller-0.0.1-1-specfile.yml"
	BranchName      string // e.g. "dalec/containernetworking/azure-ipam/0.0.1-1/20260505-72c644"
	PRTitle         string // e.g. "[Dalec][20260505-72c644] aks-node-controller @ 0.0.1-1"
}

// Resolve computes all naming outputs from one or more component states sharing
// the same PR. Pass a single state for standalone components, or multiple states
// for grouped components. All states share the same DisplayName, BranchName,
// and PRTitle; SpecFilePath is derived per component individually.
func Resolve(states []onboarding.ComponentState, prID string) Naming {
	first := states[0]
	onboard := first.Onboard
	tagSet := first.Tag

	version := strings.TrimPrefix(tagSet.Version, "v")
	versionRevision := fmt.Sprintf("%s-%d", version, tagSet.Revision)

	displayName := onboard.SpecImageName
	if onboard.GroupName != "" {
		displayName = onboard.GroupName
	}

	folderPath := deriveFolderPath(onboard)
	specFilePath := fmt.Sprintf("%s/%s-%s-specfile.yml", onboard.SpecDir(), onboard.SpecImageName, versionRevision)
	branchName := fmt.Sprintf("dalec/%s/%s/%s", folderPath, versionRevision, prID)
	prTitle := fmt.Sprintf("[Dalec][%s] %s @ %s", prID, displayName, versionRevision)

	return Naming{
		DisplayName:     displayName,
		VersionRevision: versionRevision,
		FolderPath:      folderPath,
		SpecFilePath:    specFilePath,
		BranchName:      branchName,
		PRTitle:         prTitle,
	}
}

// deriveFolderPath computes the branch folder path from OnboardDir by stripping
// the "specs/" prefix. For grouped components, the trailing component name is
// removed so the branch represents the group-level folder.
//
// Examples:
//
//	standalone single:  OnboardDir="specs/aks-node-controller"           → "aks-node-controller"
//	standalone multi:   OnboardDir="specs/containernetworking/azure-ipam" → "containernetworking/azure-ipam"
//	grouped component:  OnboardDir="specs/containernetworking/azure-cns"  → "containernetworking"
func deriveFolderPath(onboard *onboarding.ComponentConfig) string {
	folderPath := strings.TrimPrefix(onboard.OnboardDir, "specs/")
	if onboard.GroupName != "" {
		folderPath = strings.TrimSuffix(folderPath, "/"+onboard.SpecImageName)
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
