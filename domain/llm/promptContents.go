package llm

type InstructionContents struct {
	Dockerfile []byte `yaml:"dockerfile"`
	Makefile   []byte `yaml:"makefile"`
}