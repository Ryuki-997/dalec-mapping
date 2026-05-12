package repository

// ═══════════════════════════════════════════════════════════════════════════════
// licenseDetector.go — SPDX license detection from file content.
//
// Matches license text against well-known phrases to return the SPDX
// identifier. Ordered most-specific-first to avoid false positives.
// Identifiers follow SPDX License List v3.28.0 (https://spdx.org/licenses/).
// ═══════════════════════════════════════════════════════════════════════════════

import "strings"

// DetectSPDXFromContent inspects the text of a license file and returns the
// matching SPDX identifier. Returns "proprietary" when no known license is
// detected.
func DetectSPDXFromContent(content []byte) string {
	text := strings.ToLower(string(content))

	// ── Copyleft / Reciprocal (most specific first) ──────────────────────

	if contains(text, "gnu affero general public license") && contains(text, "version 3") {
		return "AGPL-3.0-only"
	}

	if contains(text, "gnu lesser general public license") && contains(text, "version 3") {
		return "LGPL-3.0-only"
	}

	if contains(text, "gnu lesser general public license") && contains(text, "version 2.1") {
		return "LGPL-2.1-only"
	}

	if contains(text, "gnu general public license") && contains(text, "version 3") {
		return "GPL-3.0-only"
	}

	if contains(text, "gnu general public license") && contains(text, "version 2") {
		return "GPL-2.0-only"
	}

	if contains(text, "mozilla public license") && contains(text, "version 2.0") {
		return "MPL-2.0"
	}

	if contains(text, "eclipse public license") && (contains(text, "v 2.0") || contains(text, "version 2.0")) {
		return "EPL-2.0"
	}

	if contains(text, "common development and distribution license") && contains(text, "version 1.0") {
		return "CDDL-1.0"
	}

	// ── Permissive ───────────────────────────────────────────────────────

	if contains(text, "apache license") && contains(text, "version 2.0") {
		return "Apache-2.0"
	}

	if contains(text, "permission is hereby granted, free of charge") {
		return "MIT"
	}

	if contains(text, "redistribution and use in source and binary forms") {
		if contains(text, "neither the name") {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	}

	if contains(text, "permission to use, copy, modify, and/or distribute this software") {
		return "ISC"
	}

	if contains(text, "boost software license") && contains(text, "version 1.0") {
		return "BSL-1.0"
	}

	if contains(text, "not misrepresented as being the original software") {
		return "Zlib"
	}

	if contains(text, "this is free and unencumbered software released into the public domain") {
		return "Unlicense"
	}

	if contains(text, "creative commons") && contains(text, "cc0") && contains(text, "public domain") {
		return "CC0-1.0"
	}

	if contains(text, "creative commons attribution 4.0 international") {
		return "CC-BY-4.0"
	}

	return "proprietary"
}

func contains(text, phrase string) bool {
	return strings.Contains(text, phrase)
}
