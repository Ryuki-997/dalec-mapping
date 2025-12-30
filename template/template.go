package template

/* TODO:


1, Auto-populate remaining fields (packager, vendor, license, version, revision, repository)
2. repository, per-target

*/

type Template struct {
	Name        string // must be lowercase
	RepoName    string // repository name - can have uppercase letters
	Website     string
	Description string

	Args   Args
	Build  Build
	Source Source
	Deps   []Dependency
	Image  Image
}

type Args struct {
	Version         float64
	Commit          string
	TargetArch      string
	TargetOS        string
	TargetOSVersion string
}

type Build struct {
	Targets []string
}

type Source struct {
	URL string
}

type Dependency struct{}

type Image struct {
	Entrypoint string
}
