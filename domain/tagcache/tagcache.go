package tagcache

import "fmt"

// Cache maps repoURL → tagName → commitSHA.
// Populated once during step 1 tag fetching, then read by later steps
// (e.g. step 3 bump-commit) to avoid redundant API calls.
var Cache map[string]map[string]string

// Init creates an empty tag cache. Call once before step 1.
func Init() {
	Cache = make(map[string]map[string]string)
}

// Lookup returns the cached commit SHA for the given repo URL and tag.
// Returns an error if the repo or tag is not in the cache.
func Lookup(repoURL, fullTag string) (string, error) {
	repoTags, ok := Cache[repoURL]
	if !ok {
		return "", fmt.Errorf("no cached tags for repo %s", repoURL)
	}
	commitSHA, ok := repoTags[fullTag]
	if !ok {
		return "", fmt.Errorf("tag %s not found in cache for repo %s", fullTag, repoURL)
	}
	return commitSHA, nil
}

// LookupCommit returns the cached commit SHA for the given tag without
// requiring the caller to know which partner repo published it. Walks every
// cached repo and returns the first match. Used by Phase 2 helpers (e.g.
// spec.BumpVersion / spec.DetectRevisionBump) which operate on a *WorkComponent
// in isolation and therefore have no direct reference to the owning
// WorkGroup.
//
// Collisions across repos are a partner configuration problem (two repos
// publishing the same tag literal in the same run); the first hit wins.
func LookupCommit(fullTag string) (string, error) {
	for repoURL, repoTags := range Cache {
		commitSHA, ok := repoTags[fullTag]
		if !ok {
			continue
		}
		_ = repoURL
		return commitSHA, nil
	}
	return "", fmt.Errorf("tag %s not found in any cached repo", fullTag)
}
