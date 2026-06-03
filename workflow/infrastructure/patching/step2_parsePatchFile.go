// ═══════════════════════════════════════════════════════════════════════════════
// Patching Step 2 — Parse & Categorize Scan Results
//
//   Reads Trivy JSON scan output from step 1 and classifies each CVE as either
//   reflective (fixable via Dalec specfile) or non-reflective (requires manual
//   resolution). Reflective CVEs include OS packages and language dependencies
//   managed by Dalec source generators (gomod, cargohome, pip).
//
//   Chunk 1 · TYPES       Vulnerability, PatchCategory, CategorizedVulnerability,
//                          ImagePatchReport, PatchReport
//   Chunk 2 · CLASSIFY    categorizeResult(), reflective type sets
//   Chunk 3 · PARSE       ParseAndCategorize(), BuildPatchReport()
// ═══════════════════════════════════════════════════════════════════════════════

package patching

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// ─── Chunk 1 · TYPES ────────────────────────────────────────────────────────

// PatchCategory classifies a CVE by whether it can be resolved through the
// Dalec specfile (reflective) or requires manual upstream fixes (non-reflective).
type PatchCategory string

const (
	ReflectiveCVE    PatchCategory = "reflective"
	NonReflectiveCVE PatchCategory = "non-reflective"
)

// Vulnerability holds the fields parsed from a single Trivy vulnerability entry.
type Vulnerability struct {
	VulnerabilityID  string
	PkgName          string
	InstalledVersion string
	FixedVersion     string
	Severity         string
	Title            string
}

// CategorizedVulnerability wraps a Vulnerability with its classification and
// the reason it was placed in that category.
type CategorizedVulnerability struct {
	Vulnerability
	Category PatchCategory
	Reason   string
}

// ImagePatchReport holds the categorized CVE results for a single scanned image.
type ImagePatchReport struct {
	ScanResultPath    string
	ReflectiveCVEs    []CategorizedVulnerability
	NonReflectiveCVEs []CategorizedVulnerability
}

// PatchReport aggregates categorized results across all scanned images.
type PatchReport struct {
	Images             []ImagePatchReport
	TotalReflective    int
	TotalNonReflective int
}

// ─── Chunk 2 · CLASSIFY ─────────────────────────────────────────────────────

// reflectiveTypes maps Trivy Result "Type" values to a reason string for CVEs
// that are resolvable through Dalec source generators.
var reflectiveTypes = map[string]string{
	"gobinary": "Go module dependency — bump source commit",
	"gomod":    "Go module dependency — bump source commit",
	"cargo":    "Rust crate — bump source commit with cargohome generator",
	"pip":      "Python package — bump source commit with pip generator",
	"pipenv":   "Python package — bump source commit with pip generator",
	"poetry":   "Python package — bump source commit with pip generator",
}

// categorizeResult determines the PatchCategory and reason for all
// vulnerabilities in a Trivy Result based on its Class and Type fields.
func categorizeResult(class, resultType string) (PatchCategory, string) {
	if class == "os-pkgs" {
		return ReflectiveCVE, "OS package — update specfile dependencies"
	}

	if reason, ok := reflectiveTypes[resultType]; ok {
		return ReflectiveCVE, reason
	}

	return NonReflectiveCVE, "Not managed by Dalec specfile — requires manual resolution"
}

// ─── Chunk 3 · PARSE ────────────────────────────────────────────────────────

// trivyReport mirrors the subset of Trivy's JSON schema that we need for
// classification. Class and Type live at the Result level; all vulnerabilities
// within a single Result share the same Class/Type.
type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Target          string               `json:"Target"`
	Class           string               `json:"Class"`
	Type            string               `json:"Type"`
	Vulnerabilities []trivyVulnerability `json:"Vulnerabilities"`
}

type trivyVulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

// ParseAndCategorize reads a Trivy JSON file and categorizes every
// vulnerability as reflective or non-reflective. Duplicates (same
// VulnerabilityID + PkgName) within one image are collapsed.
func ParseAndCategorize(scanResultPath string) (*ImagePatchReport, error) {
	data, err := os.ReadFile(scanResultPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read scan results %s: %w", scanResultPath, err)
	}

	var report trivyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse scan results %s: %w", scanResultPath, err)
	}

	seen := make(map[string]bool)
	imageReport := &ImagePatchReport{ScanResultPath: scanResultPath}

	for _, result := range report.Results {
		if len(result.Vulnerabilities) == 0 {
			continue
		}

		category, reason := categorizeResult(result.Class, result.Type)

		for _, vuln := range result.Vulnerabilities {
			dedupeKey := vuln.VulnerabilityID + "|" + vuln.PkgName
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true

			categorized := CategorizedVulnerability{
				Vulnerability: Vulnerability{
					VulnerabilityID:  vuln.VulnerabilityID,
					PkgName:          vuln.PkgName,
					InstalledVersion: vuln.InstalledVersion,
					FixedVersion:     vuln.FixedVersion,
					Severity:         vuln.Severity,
					Title:            vuln.Title,
				},
				Category: category,
				Reason:   reason,
			}

			if category == ReflectiveCVE {
				imageReport.ReflectiveCVEs = append(imageReport.ReflectiveCVEs, categorized)
			} else {
				imageReport.NonReflectiveCVEs = append(imageReport.NonReflectiveCVEs, categorized)
			}
		}
	}

	return imageReport, nil
}

// BuildPatchReport iterates all scan result paths from step 1, categorizes
// each, and returns an aggregate PatchReport.
func BuildPatchReport(scanResults []string) (*PatchReport, error) {
	patchReport := &PatchReport{}

	for _, scanPath := range scanResults {
		imageReport, err := ParseAndCategorize(scanPath)
		if err != nil {
			log.Printf("⚠️  Failed to parse %s: %v\n", scanPath, err)
			continue
		}

		patchReport.Images = append(patchReport.Images, *imageReport)
		patchReport.TotalReflective += len(imageReport.ReflectiveCVEs)
		patchReport.TotalNonReflective += len(imageReport.NonReflectiveCVEs)

		logImageReport(imageReport)
	}

	log.Printf("Patch report complete: %d reflective, %d non-reflective across %d images\n",
		patchReport.TotalReflective, patchReport.TotalNonReflective, len(patchReport.Images))

	return patchReport, nil
}

// logImageReport prints a categorized summary for a single image.
func logImageReport(report *ImagePatchReport) {
	log.Printf("  ── %s ──\n", report.ScanResultPath)

	if len(report.ReflectiveCVEs) == 0 && len(report.NonReflectiveCVEs) == 0 {
		log.Printf("    ✅ No vulnerabilities\n")
		return
	}

	if len(report.ReflectiveCVEs) > 0 {
		log.Printf("    Reflective CVEs (%d):\n", len(report.ReflectiveCVEs))
		for _, cve := range report.ReflectiveCVEs {
			fixedVersion := cve.FixedVersion
			if fixedVersion == "" {
				fixedVersion = "(no fix)"
			}
			log.Printf("      [%s] %s — %s %s -> %s | %s\n",
				cve.Severity, cve.VulnerabilityID, cve.PkgName, cve.InstalledVersion, fixedVersion, cve.Reason)
		}
	}

	if len(report.NonReflectiveCVEs) > 0 {
		log.Printf("    Non-reflective CVEs (%d):\n", len(report.NonReflectiveCVEs))
		for _, cve := range report.NonReflectiveCVEs {
			fixedVersion := cve.FixedVersion
			if fixedVersion == "" {
				fixedVersion = "(no fix)"
			}
			log.Printf("      [%s] %s — %s %s -> %s | %s\n",
				cve.Severity, cve.VulnerabilityID, cve.PkgName, cve.InstalledVersion, fixedVersion, cve.Reason)
		}
	}
}
