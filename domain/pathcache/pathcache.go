package pathcache

// Cache is the set of every file path under the spec repo's tracked tree.
// Populated once at the start of Phase 1 from the GitHub git-tree API and
// read by every subsequent phase to answer "does this remote path exist?"
// without further network calls.
//
// Mirrors the lifecycle of tagcache.Cache: one-per-run global state, written
// once during Phase 1, read-only afterwards.
var Cache map[string]bool

// Init creates an empty path cache. Call once before populating.
func Init() {
	Cache = make(map[string]bool)
}

// Set replaces the cache with the given path index. Used to bulk-load the
// result of specapi.SpecRepoFetchTree.
func Set(paths map[string]bool) {
	Cache = paths
}

// Has reports whether the given remote path exists in the spec repo.
// Returns false when the cache is uninitialized.
func Has(path string) bool {
	return Cache[path]
}
