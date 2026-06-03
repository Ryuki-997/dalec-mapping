package contents

// MakefileInfo holds variables extracted from a Makefile.
type MakefileInfo struct {
	Source          []byte            `yaml:"-"` // Raw Makefile bytes (populated by discover; preserved by parser)
	Variables       map[string]string `yaml:"variables"`
	GoBuildTargets  []string          `yaml:"-"` // package targets from go build commands (e.g. "./cmd/client")
	GoBuildCommands []SpecBinary      `yaml:"-"` // full parsed go build commands (Name, OutputPath, LdFlags, BuildCommand)
}
