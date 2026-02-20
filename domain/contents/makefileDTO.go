package contents

type MakefileInfo struct {
	Variables map[string]string   `yaml:"variables"`
	Targets   map[string][]string `yaml:"targets"`
}
