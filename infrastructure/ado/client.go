package ado

import (
	"fmt"
	"os/exec"
	"strings"
)

// TagInfo holds a tag name and its associated commit SHA.
type TagInfo struct {
	Name   string
	Commit string
}

// FetchAllTags queries a remote ADO repository for all tags using git ls-remote.
// In ADO every tag is guaranteed to be tied to a commit, so all tags are valid.
// For annotated tags the dereferenced commit (^{}) is used as the commit SHA.
func FetchAllTags(repoURL string) ([]TagInfo, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", repoURL)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote --tags failed for %s: %w", repoURL, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	// ls-remote output:
	//   <sha>\trefs/tags/v1.0.0           ← lightweight or annotated tag object
	//   <sha>\trefs/tags/v1.0.0^{}        ← dereferenced commit of annotated tag
	// For annotated tags we prefer the ^{} line (actual commit).
	tagCommits := make(map[string]string) // tag name → commit SHA
	var order []string                    // preserve insertion order

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		sha, ref := parts[0], parts[1]

		if strings.HasSuffix(ref, "^{}") {
			// Dereferenced annotated tag — overwrite with the real commit SHA
			name := strings.TrimPrefix(strings.TrimSuffix(ref, "^{}"), "refs/tags/")
			tagCommits[name] = sha
		} else {
			name := strings.TrimPrefix(ref, "refs/tags/")
			if _, exists := tagCommits[name]; !exists {
				tagCommits[name] = sha
				order = append(order, name)
			}
		}
	}

	tags := make([]TagInfo, 0, len(order))
	for _, name := range order {
		tags = append(tags, TagInfo{Name: name, Commit: tagCommits[name]})
	}
	return tags, nil
}
