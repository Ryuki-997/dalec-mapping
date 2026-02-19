package global

// CleanedValuesCache stores cleaned versions of OutputPath and LdFlags
type CleanedValuesCache struct {
	OutputPath string
	LdFlags    string
}

type OnboardingInfo struct {
	Repository  string   `yaml:"repository"`
	Tag         string   `yaml:"tag"`
	Dockerfile []string `yaml:"dockerfiles"`
	Makefile   []string `yaml:"makefiles"`
}

// ExternalTool represents external binaries downloaded via curl/wget
type ExternalTool struct {
	Name              string `yaml:"name"`
	DownloadURL       string `yaml:"downloadURL"`
	NeedsSeparateSpec bool   `yaml:"needsSeparateSpec"`
}

// Binary represents additional binaries built alongside the primary binary
type Binary struct {
	Name         string `yaml:"name"`
	OutputPath   string `yaml:"outputPath"`
	BuildCommand string `yaml:"buildCommand"`
	LdFlags      string `yaml:"ldFlags"`
}

// NonDeterministicValues holds agent-extracted values from Dockerfile/Makefile
type NonDeterministicValues struct {
	// Build Artifacts
	BinaryName       string   `yaml:"binaryName"`
	BinaryOutputPath string   `yaml:"binaryOutputPath"`
	Binaries         []Binary `yaml:"binaries"`

	// Image Configuration
	Entrypoint string `yaml:"entrypoint"`
	Symlink    string `yaml:"symlink"`

	// Dependencies
	BuildDeps     []string       `yaml:"buildDeps"`
	RuntimeDeps   []string       `yaml:"runtimeDeps"`
	ExternalTools []ExternalTool `yaml:"externalTools"`

	// Validation
	Warnings   []string `yaml:"warnings"`
	Confidence float64  `yaml:"confidence"`
}

type MakefileInfo struct {
	Variables map[string]string
	Targets   map[string][]string
}

// DockerfileInfo contains parsed information from a Dockerfile
type DockerfileInfo struct {
	Stages []Stage           // Multi-stage build stages
	Args   map[string]string // Global ARG declarations
	Labels map[string]string // LABEL metadata
}

// Stage represents a build stage in a multi-stage Dockerfile
type Stage struct {
	Instruction  string            // "FROM"
	Name         string            // Stage name from "AS <name>"
	From         string            // Base image
	Platform     string            // Platform from --platform flag
	Args         map[string]string // ARG in this stage
	Env          map[string]string // ENV variables
	Workdir      string            // WORKDIR path
	Runs         []string          // RUN commands
	Copies       []CopyInstruction // COPY/ADD instructions
	Entrypoint   []string          // ENTRYPOINT
	Cmd          []string          // CMD
	Expose       []string          // EXPOSE ports
	Instructions []RawInstruction  // Raw instructions for detailed parsing
}

// RawInstruction represents a raw Dockerfile instruction for source extraction
type RawInstruction struct {
	Type  string            // Instruction type (ADD, COPY, RUN, etc.)
	Args  []string          // Instruction arguments
	Flags map[string]string // Instruction flags (--from, --platform, etc.)
}

// CopyInstruction represents a COPY or ADD instruction
type CopyInstruction struct {
	Type   string   // "COPY" or "ADD"
	From   string   // Source stage (--from=<stage>)
	Source []string // Source paths
	Dest   string   // Destination path
}

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

type InstructionContents struct {
	Dockerfiles []string `yaml:"dockerfiles"`
	Makefiles   []string `yaml:"makefiles"`
}

type CRUDRequest string

const (
	GET    CRUDRequest = "GET"
	POST   CRUDRequest = "POST"
	PUT    CRUDRequest = "PUT"
	DELETE CRUDRequest = "DELETE"
)

type GithubRequest struct {
	URL     string
	Method  CRUDRequest
	Payload interface{}
}