package contents

import (
	"runtime"
	"strings"
)

// BuildTarget represents a Dalec build target in OS/output format.
type BuildTarget string

const (
	AzLinux3Rpm           BuildTarget = "azlinux3/rpm"
	AzLinux3Container     BuildTarget = "azlinux3/container"
	NobleDeb              BuildTarget = "noble/deb"
	JammyDeb              BuildTarget = "jammy/deb"
	FocalDeb              BuildTarget = "focal/deb"
	BionicDeb             BuildTarget = "bionic/deb"
	BookwormDeb           BuildTarget = "bookworm/deb"
	WindowsCrossContainer BuildTarget = "windowscross/container"
)

// AllTargets lists every valid build target.
var AllTargets = []BuildTarget{
	AzLinux3Container,
	AzLinux3Rpm,
	BookwormDeb,
	NobleDeb,
	JammyDeb,
	FocalDeb,
	BionicDeb,
	WindowsCrossContainer,
}

// IsValidTarget checks if a string is a known build target.
func IsValidTarget(s string) (BuildTarget, bool) {
	for _, t := range AllTargets {
		if string(t) == s {
			return t, true
		}
	}
	return "", false
}

// OS returns the OS portion of the target (e.g. "azlinux3" from "azlinux3/container").
func (bt BuildTarget) OS() string {
	if parts := strings.SplitN(string(bt), "/", 2); len(parts) == 2 {
		return parts[0]
	}
	return string(bt)
}

// Output returns the output type (e.g. "container", "rpm", "deb").
func (bt BuildTarget) Output() string {
	if parts := strings.SplitN(string(bt), "/", 2); len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// IsWindows returns true for windowscross targets.
func (bt BuildTarget) IsWindows() bool {
	return bt.OS() == "windowscross"
}

// Platform returns the docker --platform value for this target.
func (bt BuildTarget) Platform() string {
	if bt.IsWindows() {
		return "windows/amd64"
	}
	return "linux/" + runtime.GOARCH
}