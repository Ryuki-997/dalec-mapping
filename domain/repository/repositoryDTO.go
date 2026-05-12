package repository

type SourceGenerator string

const (
	GoModGenerator     SourceGenerator = "gomod"
	CargoHomeGenerator SourceGenerator = "cargohome"
	PipGenerator       SourceGenerator = "pip"
)

// RepoInfo contains metadata about a GitHub repository
type RepoInfo struct {
	Owner         string
	Repo          string
	Branch        string
	ComponentPath string // component subdirectory within the repo (e.g. "test/node-problem-detector")
	ComponentName string // leaf name of the component (e.g. "node-problem-detector")
	GitURL        string
	Description   string
	Version       string
	License       string
	LicenseFile   string // path to the license file within the repo (e.g. "LICENSE", "docs/NOTICE")
	LatestCommit  string
	GoVersion     string // Go toolchain version detected from the Dockerfile (e.g. "1.24")
	Generator     SourceGenerator
}
