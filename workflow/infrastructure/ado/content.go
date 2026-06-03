package ado

// ─── Chunk 5 · FILE CONTENT ─────────────────────────────────────────────────

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"
)

// FetchADOFileContent fetches a single file at the given tag from the ADO
// repository using a temporary partial clone (--filter=blob:none).
// The clone fetches only the tree objects; git show lazily fetches the one
// blob needed. When the exact path fails (e.g. the path points to a directory
// or is one level too shallow), it falls back to git ls-tree to discover the
// target filename under the parent directory.
func FetchADOFileContent(repoURL, filePath, tag string) ([]byte, error) {
	baseURL, _ := SplitADOComponent(repoURL)
	filePath = strings.TrimPrefix(filePath, "/")

	tmpDir, err := os.MkdirTemp("", "ado-clone-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if _, err := gitOut("", "clone",
		"--filter=blob:none",
		"--no-checkout",
		"--depth=1",
		"--branch="+tag,
		adoAuthURL(baseURL),
		tmpDir,
	); err != nil {
		return nil, fmt.Errorf("failed to clone %s at %s: %w", baseURL, tag, err)
	}

	content, err := gitOutBytes(tmpDir, "show", "HEAD:"+filePath)
	if err == nil {
		return content, nil
	}

	// Exact path failed — search the parent directory for the target filename
	// (handles repos that nest files one level deeper, e.g. docker/<component>/Dockerfile).
	dir := path.Dir(filePath)
	fileName := path.Base(filePath)
	resolvedPath, findErr := findFileInTree(tmpDir, dir, fileName)
	if findErr != nil {
		return nil, err
	}
	log.Printf("  Resolved %s → %s\n", filePath, resolvedPath)
	return gitOutBytes(tmpDir, "show", "HEAD:"+resolvedPath)
}

// findFileInTree searches a directory tree for a file by name using git ls-tree.
// Returns the first path whose base name matches fileName.
func findFileInTree(repoDir, dir, fileName string) (string, error) {
	out, err := gitOut(repoDir, "ls-tree", "-r", "--name-only", "HEAD", dir)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if path.Base(line) == fileName {
			return line, nil
		}
	}
	return "", fmt.Errorf("file %s not found under %s", fileName, dir)
}
