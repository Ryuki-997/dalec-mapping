package llm

// CleanedValuesCache stores the cleaned LdFlags value across ClearEnvVariables calls.
type CleanedValuesCache struct {
	LdFlags string
}