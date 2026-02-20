package parser

type Writer[T any] interface {
	WriteYAML(value T, outputPath string) (string, error)
	ReadYAML(path string) (T, error)
}
