package contents

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

// DefaultTargets is used when no targets are specified.
var DefaultTargets = []BuildTarget{
	AzLinux3Container,
	WindowsCrossContainer,
}