package transformer

import (
	"fmt"
	"regexp"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"
)

// extractBuildSection assembles the top-level `build:` map for a Dalec spec.
// Returns the build map and the set of ${VAR} names referenced inside it,
// so the caller can forward them as top-level args.
func extractBuildSection(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) (map[string]interface{}, map[string]bool) {
	build := make(map[string]interface{})

	env := buildEnv(nonDeterministicValues)
	build["env"] = env

	steps, scanText := buildSteps(defaultSpec, nonDeterministicValues)
	build["steps"] = steps

	referencedVars := scanVarReferences(scanText, env)

	// Promote any referenced Makefile variable into env so the spec arg is wired through.
	for varName := range referencedVars {
		if _, alreadySet := env[varName]; alreadySet {
			continue
		}
		if makefileInfo != nil {
			if _, exists := makefileInfo.Variables[varName]; exists {
				env[varName] = fmt.Sprintf("${%s}", varName)
			}
		}
	}

	return build, referencedVars
}

// buildEnv constructs the env map for the build section.
// Standard vars are always included; LDFLAGS is added when any binary declares ldflags.
func buildEnv(nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	env := map[string]interface{}{
		"GOPROXY":      "direct",
		"GOEXPERIMENT": "systemcrypto",
		"CGO_ENABLED": "1", // required by GOEXPERIMENT=systemcrypto (FIPS)
		"VERSION":     "${VERSION}",
		"GOOS":        "${TARGETOS}",
		"GOARCH":      "${TARGETARCH}",
	}

	if nonDeterministicValues != nil {
		for _, aux := range nonDeterministicValues.Binaries {
			if aux.LdFlags != "" {
				env["LDFLAGS"] = aux.LdFlags
				break
			}
		}
	}
	return env
}

// buildSteps converts NonDeterministicValues binaries into Dalec `steps` entries.
// Each binary becomes one step. Also returns the combined command text for var scanning.
// baseDir is always the repo source name (the root of the cloned source). The LLM-provided
// build command's `cd` paths are always relative to the repo root, not the Dockerfile subdir.
func buildSteps(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) ([]map[string]interface{}, string) {
	baseDir := defaultSpec.Repo

	rawSteps := rawBuildCommands(nonDeterministicValues)

	// Fallback: no commands extracted — emit a minimal go build step.
	if len(rawSteps) == 0 {
		fallback := fmt.Sprintf("cd %s\ngo build -o /go/bin/%s ./main.go", baseDir, defaultSpec.Repo)
		return []map[string]interface{}{{"command": fallback}}, fallback
	}

	var steps []map[string]interface{}
	var allText strings.Builder
	for _, block := range rawSteps {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		stepCmd := assembleStep(block, baseDir)
		steps = append(steps, map[string]interface{}{"command": stepCmd})
		allText.WriteString(stepCmd)
		allText.WriteString("\n")
	}
	return steps, allText.String()
}

// assembleStep prepends `cd baseDir` to a block, hoisting any mid-block `cd X &&` into
// an explicit `cd baseDir/X` line so the step stays as one command block.
func assembleStep(block, baseDir string) string {
	lines := strings.Split(block, "\n")
	lastLine := lines[len(lines)-1]
	subdir, rest := extractCdDir(lastLine)
	if subdir != "" {
		preamble := strings.Join(lines[:len(lines)-1], "\n")
		if preamble != "" {
			return fmt.Sprintf("%s\ncd %s/%s\n%s", preamble, baseDir, subdir, rest)
		}
		return fmt.Sprintf("cd %s/%s\n%s", baseDir, subdir, rest)
	}
	return fmt.Sprintf("cd %s\n%s", baseDir, block)
}

// rawBuildCommands cleans each binary's fields and returns one command string per binary.
// Output is always /go/bin/<canonicalName>${BIN_SUFFIX} — the canonical name is derived
// from the primary linux entrypoint when it differs from the LLM binary name (e.g.
// "dropgz" when binaries[0].Name is "azure-ipam"). BIN_SUFFIX is injected so the same
// step works for both Linux (BIN_SUFFIX="") and windowscross (BIN_SUFFIX=".exe").
func rawBuildCommands(nonDeterministicValues *llm.NonDeterministicValues) []string {
	if nonDeterministicValues == nil {
		return nil
	}

	// Canonical binary name from the primary linux symlinks key (the real installed binary).
	// lt.Symlink is the Dalec symlinks map key = the target path = where artifacts land.
	epBase := ""
	if lt := findPrimaryLinuxTarget(nonDeterministicValues.Targets); lt != nil {
		epBase = canonicalBase(lt.Symlink)
	}

	var cmds []string
	binOutRe := regexp.MustCompile(`-o (/go/bin/[^${}\s]+)`)

	for i := range nonDeterministicValues.Binaries {
		aux := &nonDeterministicValues.Binaries[i]
		if aux.Name == "" {
			continue
		}

		github.ClearEnvVariables("LdFlags", &aux.LdFlags)
		github.ClearEnvVariables("BuildCommand", &aux.BuildCommand)

		var cmd string
		if aux.BuildCommand != "" {
			cmd = injectBinSuffix(aux.BuildCommand, binOutRe)
		} else if aux.LdFlags != "" {
			// No explicit build command — synthesise one from ldflags + output path.
			out := "/go/bin/" + aux.Name
			cmd = injectBinSuffix(
				fmt.Sprintf("go build -ldflags \"%s\" -o %s", aux.LdFlags, out),
				binOutRe,
			)
		}

		// When the entrypoint reveals a canonical name different from the LLM binary name
		// (e.g. the build should produce "dropgz" but the LLM recorded "azure-ipam"),
		// rename the -o output path so it matches the declared artifacts.binaries entry.
		if cmd != "" && epBase != "" && epBase != aux.Name {
			cmd = strings.ReplaceAll(cmd,
				"/go/bin/"+aux.Name+"${BIN_SUFFIX}",
				"/go/bin/"+epBase+"${BIN_SUFFIX}",
			)
		}

		if cmd != "" {
			cmds = append(cmds, cmd)
			fmt.Printf("Build step: %v\n", cmd)
		}
	}
	return cmds
}

// injectBinSuffix rewrites `-o /go/bin/<name>` → `-o /go/bin/<name>${BIN_SUFFIX}`
// and prepends a preamble that sets BIN_SUFFIX and, when GOOS=windows, exports CC
// pointing to the MinGW x86_64-w64-mingw32-clang wrapper (windows/amd64 only).
func injectBinSuffix(cmd string, re *regexp.Regexp) string {
	loc := re.FindStringSubmatchIndex(cmd)
	if loc == nil {
		return cmd
	}
	cmd = cmd[:loc[3]] + "${BIN_SUFFIX}" + cmd[loc[3]:]
	preamble := `BIN_SUFFIX=""
if [ "${GOOS}" = "windows" ]; then
  BIN_SUFFIX=".exe"
  export CC=` + MingwBinDir + `/x86_64-w64-mingw32-clang
fi`
	return preamble + "\n" + cmd
}

// scanVarReferences finds all ${VAR}/(VAR) references in command text and env values.
func scanVarReferences(cmdText string, env map[string]interface{}) map[string]bool {
	varRefRe := regexp.MustCompile(`\$[{(]([A-Za-z_][A-Za-z0-9_]*)[})]`)
	refs := make(map[string]bool)

	for _, text := range []string{cmdText, fmt.Sprintf("%v", env["LDFLAGS"])} {
		for _, m := range varRefRe.FindAllStringSubmatch(text, -1) {
			refs[m[1]] = true
		}
	}
	return refs
}

// extractOutputFlag extracts the path passed to -o in a go build command.
func extractOutputFlag(cmd string) string {
	re := regexp.MustCompile(`\s-o\s+(\S+)`)
	if m := re.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	return ""
}

// extractCdDir parses a single line of the form "cd X && <rest>".
// Returns (X, rest) when matched, or ("", original line) otherwise.
func extractCdDir(line string) (subdir, stripped string) {
	line = strings.TrimSpace(line)
	if m := regexp.MustCompile(`^cd\s+(\S+)\s*&&\s*(.+)$`).FindStringSubmatch(line); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return "", line
}

