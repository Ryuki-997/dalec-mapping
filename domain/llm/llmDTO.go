package llm

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

// TargetSpec holds all per-target configuration for one build target.
// Each entry groups the Dalec target OS, image entrypoint, symlink, and
// application-specific dependencies together.
type TargetSpec struct {
	// TargetOS is the full Dalec target string (e.g. "azlinux3/container", "windowscross/container").
	TargetOS string `yaml:"targetOS"`

	// Entrypoint is the absolute path to the binary inside the container image.
	// Linux targets: typically "/usr/local/bin/<name>".
	// Windows targets: just the binary name with no path prefix (e.g. "azure-cns").
	Entrypoint string `yaml:"entrypoint"`

	// Symlink is a secondary path pointing to Entrypoint (Linux targets only).
	// Typically "/usr/bin/<name>". Leave empty for windowscross.
	Symlink string `yaml:"symlink,omitempty"`

	// Build contains application-specific packages needed at compile time.
	// Do NOT include: msft-golang, gcc, SymCrypt, SymCrypt-OpenSSL, openssl-libs — transformer adds these.
	Build []string `yaml:"build"`

	// Runtime contains packages needed inside the final image at runtime.
	// windowscross: must always be empty — Dalec rejects runtime deps on Windows targets.
	Runtime []string `yaml:"runtime"`
}

// NonDeterministicValues holds agent-extracted values from Dockerfile/Makefile
type NonDeterministicValues struct {
	// Build Artifacts
	Binaries []Binary `yaml:"binaries"`

	// Targets is the ordered list of build targets, each carrying its full per-target configuration:
	// targetOS, entrypoint, symlink, and application-specific build/runtime dependencies.
	Targets []TargetSpec `yaml:"targets"`

	ExternalTools []ExternalTool `yaml:"externalTools"`

	// Validation
	Warnings   []string `yaml:"warnings"`
	Confidence float64  `yaml:"confidence"`
}