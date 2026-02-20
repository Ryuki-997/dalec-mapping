package repository

type SourceGenerator string

const (
	GoModGenerator     SourceGenerator = "gomod"
	CargoHomeGenerator SourceGenerator = "cargohome"
	PipGenerator       SourceGenerator = "pip"
)

// RepoInfo contains metadata about a GitHub repository
type RepoInfo struct {
	Owner        string
	Repo         string
	Branch       string
	GitURL       string
	Description  string
	Version      string
	License      string
	LatestCommit string
	Generator    SourceGenerator
}