package contents

// ─── Dockerfile AST types ────────────────────────────────────────────────────

// DockerfileInfo contains parsed information from a Dockerfile.
type DockerfileInfo struct {
	Stages []Stage           // Multi-stage build stages
	Args   map[string]string // Global ARG declarations
	Labels map[string]string // LABEL metadata
}

// Stage represents a build stage in a multi-stage Dockerfile.
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

// RawInstruction represents a raw Dockerfile instruction for source extraction.
type RawInstruction struct {
	Type  string            // Instruction type (ADD, COPY, RUN, etc.)
	Args  []string          // Instruction arguments
	Flags map[string]string // Instruction flags (--from, --platform, etc.)
}

// CopyInstruction represents a COPY or ADD instruction.
type CopyInstruction struct {
	Type   string   // "COPY" or "ADD"
	From   string   // Source stage (--from=<stage>)
	Source []string // Source paths
	Dest   string   // Destination path
}

// ─── Makefile types ──────────────────────────────────────────────────────────

// MakefileInfo holds variables extracted from a Makefile.
type MakefileInfo struct {
	Variables      map[string]string `yaml:"variables"`
	GoBuildTargets []string          `yaml:"-"` // package targets from go build commands (e.g. "./cmd/client")
	GoBuildCommands []SpecBinary     `yaml:"-"` // full parsed go build commands (Name, OutputPath, LdFlags, BuildCommand)
}

// ─── Dockerfile spec types (result of static AST analysis) ──────────────────

// DockerfileSpec is the spec-facing result of statically parsing a Dockerfile.
// It captures everything the transformer needs to generate a Dalec spec:
// the binaries being built, any intermediate pipeline steps, and per-target
// image configuration (entrypoint, symlink, extra deps).
type DockerfileSpec struct {
	// Binaries lists every `go build` binary produced by the builder stage.
	// The first entry is the primary binary; additional entries are secondaries.
	Binaries []SpecBinary

	// PipelineSteps are ordered shell commands from intermediate stages that
	// run after the primary binaries are compiled (e.g. file embedding, compression).
	PipelineSteps []string

	// Targets holds per-OS image and dependency configuration derived from the
	// final and intermediate Dockerfile stages.
	Targets []SpecTarget
}

// SpecBinary describes a single binary built by the Dockerfile builder stage.
type SpecBinary struct {
	// Name is the binary file name (e.g. "azure-cns", "azure-ipam").
	Name string

	// BuildCommand is the full `go build ...` command as it appears in the Dockerfile.
	BuildCommand string

	// OutputPath is the -o destination path (e.g. "/go/bin/azure-cns").
	OutputPath string

	// LdFlags is the value of the -ldflags argument, if present.
	LdFlags string
}

// SpecTarget holds per-OS image and dependency overrides for one build target.
type SpecTarget struct {
	// OS is the Dalec target OS prefix (e.g. "azlinux3", "windowscross").
	OS string

	// Entrypoint is the absolute binary path inside the container image.
	// Linux: e.g. "/usr/local/bin/azure-cns".
	// Windows: bare binary name — transformer prefixes the full system path.
	Entrypoint string

	// Symlink is the secondary installed path pointing to Entrypoint (Linux only).
	// e.g. "/usr/bin/azure-cns". Empty for Windows targets.
	Symlink string

	// BuildDeps lists additional packages required at compile time.
	// Standard deps (msft-golang, SymCrypt, etc.) are handled by the transformer.
	BuildDeps []string

	// RuntimeDeps lists additional packages required in the final image at runtime.
	// Must be empty for Windows targets.
	RuntimeDeps []string
}
