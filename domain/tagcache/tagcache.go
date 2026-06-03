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
