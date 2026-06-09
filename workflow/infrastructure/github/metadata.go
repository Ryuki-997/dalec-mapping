package github

// ─── Chunk 4 · METADATA ────────────────────────────────────────────────────

import (
	"fmt"
	"strings"

	"dalec-mapping/domain/repository"
)

// fetchRepoMetadata acquires default branch, description, URL, and the
// GitHub-reported SPDX license. The returned license is "" when GitHub has
// none; callers apply their own fallback (e.g. "proprietary").
func fetchRepoMetadata(info *repository.RepoInfo) (string, error) {
	data, err := FetchJSON(RepoAPIPath(info.Owner, info.Repo, ""))
	if err != nil {
		return "", err
	}

	if info.Branch == "" {
		if defaultBranch, ok := data["default_branch"].(string); ok {
			info.Branch = defaultBranch
		} else {
			info.Branch = "main"
		}
	}

	if desc, ok := data["description"].(string); ok {
		info.Description = desc
	} else {
		info.Description = fmt.Sprintf("This is the %s project.", info.Repo)
	}

	if url, ok := data["html_url"].(string); ok && url != "" {
		info.GitURL = url
	}

	if licenseData, ok := data["license"].(map[string]interface{}); ok {
		if spdxID, ok := licenseData["spdx_id"].(string); ok && spdxID != "NOASSERTION" {
			return spdxID, nil
		}
	}
	return "", nil
}

// fetchSourceGenerator detects the project's build system by scanning the repo tree.
func fetchSourceGenerator(info *repository.RepoInfo) error {
	fileGenerators := make(map[string]repository.SourceGenerator, len(repository.FileGeneratorMarkers))
	for _, m := range repository.FileGeneratorMarkers {
		fileGenerators[m.FileName] = m.Generator
	}
	dirGenerators := make(map[string]repository.SourceGenerator, len(repository.DirGeneratorMarkers))
	for _, m := range repository.DirGeneratorMarkers {
		dirGenerators[m.FileName] = m.Generator
	}

	data, err := FetchJSON(RepoAPIPath(info.Owner, info.Repo, "git/trees/%s?recursive=1", info.Branch))
	if err != nil {
		return fmt.Errorf("failed to fetch repository tree: %w", err)
	}

	treeItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected tree response format")
	}

	scanItems := func(prefix string) (repository.SourceGenerator, bool) {
		for _, item := range treeItems {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := itemMap["path"].(string)
			itemType, _ := itemMap["type"].(string)

			if prefix != "" && !strings.HasPrefix(p, prefix+"/") {
				continue
			}

			base := p[strings.LastIndex(p, "/")+1:]

			if gen, ok := fileGenerators[base]; ok && itemType == "blob" {
				return gen, true
			}
			if gen, ok := dirGenerators[base]; ok && itemType == "tree" {
				return gen, true
			}
		}
		return "", false
	}

	if info.ComponentPath != "" {
		if gen, ok := scanItems(info.ComponentPath); ok {
			info.Generator = gen
			return nil
		}

		subdirBase := info.ComponentPath[strings.LastIndex(info.ComponentPath, "/")+1:]
		for _, item := range treeItems {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := itemMap["path"].(string)
			itemType, _ := itemMap["type"].(string)
			if itemType != "tree" {
				continue
			}
			base := p[strings.LastIndex(p, "/")+1:]
			if base != subdirBase {
				continue
			}
			if gen, ok := scanItems(p); ok {
				info.Generator = gen
				return nil
			}
		}
	}

	if gen, ok := scanItems(""); ok {
		info.Generator = gen
		return nil
	}

	return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
}
