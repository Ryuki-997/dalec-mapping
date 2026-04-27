package transformer

import "path/filepath"

// extractLinuxSymlinks returns the symlinks post-install map for a Linux target.
// symlinkPath (/usr/bin/<name>) points to entrypoint (/usr/local/bin/<name>).
// A root-level alias (/<name>) also points to the entrypoint so that containers
// invoking /<name> directly (e.g. Kubernetes command overrides) find the binary.
func extractLinuxSymlinks(symlinkPath, entrypoint string) map[string]interface{} {
	alias := "/" + filepath.Base(symlinkPath)
	return map[string]interface{}{
		symlinkPath: map[string]interface{}{
			"path": entrypoint,
		},
		alias: map[string]interface{}{
			"path": entrypoint,
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
