package transformer

import "path/filepath"

// extractLinuxSymlinks returns the symlinks post-install map for a Linux target.
// symlinkPath (/usr/bin/<name>) → entrypoint (/usr/local/bin/<name>) is the primary symlink.
// entrypoint (/usr/local/bin/<name>) → /<name> creates a root-level alias so containers
// invoking /<name> directly (e.g. Kubernetes command overrides) find the binary.
func extractLinuxSymlinks(symlinkPath, entrypoint string) map[string]interface{} {
	alias := "/" + filepath.Base(symlinkPath)
	return map[string]interface{}{
		symlinkPath: map[string]interface{}{
			"path": entrypoint,
		},
		entrypoint: map[string]interface{}{
			"path": alias,
		},
	}
}

// extractWindowsSymlinks returns the symlinks post-install map for the windowscross target.
// A root-level alias (/<name>.exe) points back to the System32 entrypoint path.
func extractWindowsSymlinks(entrypoint, binaryName string) map[string]interface{} {
	alias := "/" + filepath.Base(binaryName) + ".exe"
	return map[string]interface{}{
		entrypoint: map[string]interface{}{
			"path": alias,
		},
	}
}
