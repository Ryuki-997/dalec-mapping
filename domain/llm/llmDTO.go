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

// TargetDeps holds application-specific build and runtime dependencies for one OS target.
// The transformer hardcodes toolchain (msft-golang) and crypto libs (SymCrypt, openssl-libs) —
// the LLM only provides app-specific packages on top of those.
type TargetDeps struct {
	// Build contains packages needed at compile time beyond the toolchain.
	// Do NOT include: msft-golang, gcc, SymCrypt, SymCrypt-OpenSSL, openssl-libs — transformer adds these.
	Build []string `yaml:"build"`

	// Runtime contains packages needed at runtime inside the final image.
	// Linux targets only — windowscross.Runtime must always be empty (Dalec rejects runtime deps on Windows).
	Runtime []string `yaml:"runtime"`
}

// NonDeterministicValues holds agent-extracted values from Dockerfile/Makefile
type NonDeterministicValues struct {
	// Build Artifacts
	Binaries []Binary `yaml:"binaries"`
	Targets  []string `yaml:"targets"`

	// Image Configuration
	Entrypoint string `yaml:"entrypoint"`
	Symlink    string `yaml:"symlink"`

	// Per-target dependencies keyed by OS name ("azlinux3", "windowscross", "bookworm", etc.).
	// windowscross.Runtime must always be empty — Dalec rejects runtime deps on Windows targets.
	// The transformer auto-adds: msft-golang (build), SymCrypt/openssl-libs (azlinux3 build+runtime).
	// Only emit app-specific packages here (e.g. iptables, ca-certificates, fuse).
	PerTargetDeps map[string]TargetDeps `yaml:"perTargetDeps"`

	ExternalTools []ExternalTool `yaml:"externalTools"`

	// Validation
	Warnings   []string `yaml:"warnings"`
	Confidence float64  `yaml:"confidence"`
}