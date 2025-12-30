package main

import (
	"os"
)

func main() {
	// open dockerfile
	f, err := os.Open("Dockerfile")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	// write spec file
	specFile, err := os.Create("test.yml")
	if err != nil {
		panic(err)
	}
	defer specFile.Close()
}
