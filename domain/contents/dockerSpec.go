package contents

// DockerSpec is the spec-facing result of statically parsing a Dockerfile.
// It captures everything the transformer needs to generate a Dalec spec:
// the binaries being built, any intermediate pipeline steps, and the resolved
// entrypoint and symlink paths.
type DockerSpec struct {
	// Binaries lists every `go build` binary produced by the builder stage.
	// The first entry is the primary binary; additional entries are secondaries.
	Binaries []SpecBinary

	// PipelineSteps are ordered shell commands from intermediate stages that
	// run after the primary binaries are compiled (e.g. file embedding, compression).
	PipelineSteps []string

	// Entrypoint is the resolved absolute binary path inside the container image
	// (e.g. "/usr/local/bin/azure-cns"), derived from the final Dockerfile stage.
	Entrypoint string

	// Symlink is the secondary installed path pointing to Entrypoint
	// (e.g. "/usr/bin/azure-cns"), derived from COPY destinations. May be empty.
	Symlink string
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
