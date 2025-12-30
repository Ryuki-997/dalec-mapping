package main

import (
	"fmt"
	"os"

	"dalec-mapping/parser"
)

func main() {
	// Parse Dockerfile
	dockerfileInfo, err := parser.ParseDockerfile("Dockerfile")
	if err != nil {
		fmt.Printf("Error parsing Dockerfile: %v\n", err)
		os.Exit(1)
	}

	// Print parsed information
	parser.PrintDockerfileInfo(dockerfileInfo)

	// TODO: Transform to Dalec spec
	// TODO: Write to test.yml
}
