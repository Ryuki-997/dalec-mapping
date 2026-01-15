package transformer

import (
	"dalec/parser"
	"fmt"
	"os"
	"strings"
)

func populateArgs(defaultSpec *DefaultSpec, makefileInfo *parser.MakefileInfo) map[string]interface{} {
	args := make(map[string]interface{})
	args["REVISION"] = defaultSpec.Revision
	args["VERSION"] = defaultSpec.Version
	args["COMMIT"] = defaultSpec.LatestCommit

	selfHandledArgs := map[string]bool{
		"ARCH":       true,
		"OS":         true,
		"OS_VERSION": true,
		"VERSION":    true,
	}

	for k := range defaultSpec.Args {
		if selfHandledArgs[k] {
			continue
		}

		value := makefileInfo.Variables[k]

		for nestedIndex := strings.Index(value, "$("); nestedIndex != -1; nestedIndex = strings.Index(value, "$(") {
			endIndex := strings.Index(value[nestedIndex:], ")")
			if endIndex == -1 {
				fmt.Printf("Broken makefile variable reference in arg %s: %s\n", k, value)
				os.Exit(1)
			}

			nestedKey := value[nestedIndex+2 : nestedIndex+endIndex]
			nestedValue, exists := makefileInfo.Variables[nestedKey]
			if !exists {
				fmt.Printf("Undefined makefile variable %s referenced in arg %s\n", nestedKey, k)
				os.Exit(1)
			}

			value = strings.ReplaceAll(value, "$("+nestedKey+")", nestedValue)
			fmt.Printf("Value after nested replacement: %s\n", value)
		}

		args[k] = value
		fmt.Printf("key: %s, value: %v\n", k, args[k])
	}

	return args
}

func populateMetadata(defaultSpec *DefaultSpec, spec DalecSpec) {

	spec["name"] = strings.ToLower(defaultSpec.Repo)
	spec["packager"] = "Azure Container Upstream"
	spec["vendor"] = "Microsoft Corporation"
	spec["license"] = defaultSpec.License
	spec["website"] = defaultSpec.GitURL
	spec["description"] = defaultSpec.Description
	spec["version"] = "${VERSION}"
	spec["revision"] = "${REVISION}"
}
