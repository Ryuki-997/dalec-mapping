package parser

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
