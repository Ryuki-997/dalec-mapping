package ado

// ─── Chunk 4 · TAGS ────────────────────────────────────────────────────────

import (
	"fmt"
	"strings"
)

// FetchAllADOTags lists all tags and their commit SHAs from the remote
// ADO repository using git ls-remote. Returns a map of tagName → commitSHA.
func FetchAllADOTags(repoURL string) (map[string]string, error) {
	baseURL, _ := SplitADOComponent(repoURL)
	out, err := gitOut("", "ls-remote", "--tags", adoAuthURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %s: %w", repoURL, err)
	}

	// ls-remote output:
	//   <sha>\trefs/tags/<name>       — lightweight tag or tag object
	//   <sha>\trefs/tags/<name>^{}    — peeled commit (annotated tags only)
	type entry struct{ plain, peeled string }
	byName := map[string]*entry{}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "refs/tags/") {
			continue
		}
		sha := parts[0]
		name := strings.TrimPrefix(parts[1], "refs/tags/")
		peeled := strings.HasSuffix(name, "^{}")
		name = strings.TrimSuffix(name, "^{}")
		if byName[name] == nil {
			byName[name] = &entry{}
		}
		if peeled {
			byName[name].peeled = sha
		} else {
			byName[name].plain = sha
		}
	}

	tags := make(map[string]string, len(byName))
	for name, e := range byName {
		commit := e.peeled
		if commit == "" {
			commit = e.plain
		}
		tags[name] = commit
	}
	return tags, nil
}
