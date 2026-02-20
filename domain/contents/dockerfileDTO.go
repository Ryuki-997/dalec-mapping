package contents

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