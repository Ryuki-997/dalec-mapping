// ═══════════════════════════════════════════════════════════════════════════════
// reposrc —
//
//   Cross-provider router for source-repository URL parsing and metadata
//   fetching. Hides the ADO-vs-GitHub branch from callers so service-layer
//   code can stop duplicating the same `if ado.IsADORepo(...)` dispatch.
//
//   Tag fetching is already routed by infrastructure/semver.FetchRepoTags;
//   file-content fetching stays branched in services/partnerrepo because the
//   two providers have meaningfully different fetch logic.
//
//   Functions:
//     SplitComponent()  — split <base>/<componentPath> for either provider
//     FetchRepoInfo()   — fetch RepoInfo + license for either provider
// ═══════════════════════════════════════════════════════════════════════════════

package reposrc

import (
	"dalec-mapping/domain/repository"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
)

// SplitComponent splits a repo URL into its git-addressable base and the
// component subdirectory (if any), choosing the ADO or GitHub splitter by URL.
func SplitComponent(repoURL string) (string, string) {
	if ado.IsADORepo(repoURL) {
		return ado.SplitADOComponent(repoURL)
	}
	return github.SplitGitHubComponent(repoURL)
}

// FetchRepoInfo fetches repository metadata and the detected license string,
// dispatching to the ADO or GitHub fetcher based on the repo URL. Both
// providers return *repository.RepoInfo so the returned value type matches the
// existing call sites (which currently call FetchADORepoInfo / github.FetchRepoInfo
// directly).
func FetchRepoInfo(repoURL string) (*repository.RepoInfo, string, error) {
	if ado.IsADORepo(repoURL) {
		return ado.FetchADORepoInfo(repoURL)
	}
	return github.FetchRepoInfo(repoURL)
}
