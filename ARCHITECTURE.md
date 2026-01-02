# Architecture Documentation

## Overview

This project converts Dockerfiles to Dalec specification files by combining multiple information sources: GitHub repository metadata, Dockerfile build stages, and optional previous spec files for validation and change detection.

## System Design

### Input Information Packets

The system fetches and processes three distinct information packets:

#### 1. GitHub Repository URL (Required)
- **Input Method**: Positional command-line argument (not a flag)
- **Format**: `owner/repo` or full GitHub URL
- **Purpose**: Primary source of metadata including:
  - Latest commit hash
  - Repository description
  - License information
  - Source URL
  - Homepage/website

#### 2. Dockerfile Path (Optional)
- **Input Method**: `-dockerfile` flag
- **Default**: `./Dockerfile` in current directory
- **Purpose**: Extract build configuration and arguments:
  - Multi-stage build stages
  - Build arguments (ARG directives)
  - Base images and dependencies
  - Build commands and steps
  - Entry points and exposed ports
- **Processing**:
  - Uses Docker Buildkit parser for AST-based parsing
  - Extracts stage names, platforms, and dependencies
  - Identifies build-time arguments and environment variables
  - Handles multi-architecture builds (--platform flags)

#### 3. Previous Dalec Spec File Path (Optional)
- **Input Method**: `-spec` flag
- **Purpose**: Verify previous generation was properly formatted & fill previously generated values unless specifically identified. 
     - Repository updates (commit hash changes)
     - Dockerfile modifications
     - Manual spec adjustments
     - Dependency updates
- **Processing**:
  - Parses existing YAML spec file
  - Validates required fields are present
  - Generates diff report showing changes
- **Use Cases**:
  - Incremental updates to existing specs
  - Auditing changes over time
  - Preventing accidental overwrites of manual modifications

### Processing Pipeline

```
┌─────────────────┐
│  CLI Arguments  │
│  - repo (pos)   │
│  - dockerfile   │
│  - spec         │
│  - output       │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│     Information Gathering Phase         │
├─────────────────────────────────────────┤
│  1. Parse GitHub repo path              │
│  2. Fetch GitHub metadata (parallel)    │
│  3. Parse Dockerfile (if provided)      │
│  4. Load previous spec (if provided)    │
└────────┬────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│     Validation Phase                    │
├─────────────────────────────────────────┤
│  - Validate previous spec structure     │
│  - Check commit hash for changes        │
│  - Verify Dockerfile parse success      │
│  - Report missing/invalid data          │
└────────┬────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│     Transformation Phase                │
├─────────────────────────────────────────┤
│  - Create flexible map-based DalecSpec  │
│  - Merge GitHub metadata                │
│  - Extract Dockerfile stages/args       │
│  - Apply defaults for unknown values    │
│  - Preserve manual modifications        │
└────────┬────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│     Output Phase                        │
├─────────────────────────────────────────┤
│  - Serialize to YAML format             │
│  - Write to output path (or default)    │
│  - Generate version metadata            │
│  - Optional: Store in versioned dir     │
└────────┬────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│     Reporting                           │
├─────────────────────────────────────────┤
│  - Show auto-filled fields              │
│  - List manual fields required          │
│  - Report changes from previous spec    │
│  - Display validation results           │
└─────────────────────────────────────────┘
```

### Default Value Strategy

When information is unavailable, the system applies intelligent defaults:

| Field | Default Value | Fallback Strategy |
|-------|---------------|-------------------|
| `description` | Empty string | User must manually fill if GitHub unavailable |
| `license` | Empty string | User must manually fill if not in repo |
| `commit` | "unknown" | Critical field - warn user to update |
| `website` | GitHub repo URL | Safe default using repo as homepage |
| `sources` | GitHub archive URL | Standard pattern: `https://github.com/{owner}/{repo}/archive/{commit}.tar.gz` |
| `build.steps` | Empty array | Extracted from Dockerfile or left for manual entry |
| `targets` | Default target | Basic structure, customize per platform |

### Versioned Output (Proposed)

To track evolution of generated specs over time:

```
output/
├── latest.yml                    # Symlink to most recent
├── azure-cns-20250102-084da35.yml   # Timestamped + commit hash
├── azure-cns-20250101-072bef12.yml
└── .metadata/
    ├── changes.json              # Change log between versions
    └── validation.json           # Validation results per version
```

**Benefits**:
- Version history for audit trail
- Easy rollback to previous specs
- Comparison between generations
- Track which changes came from repo vs manual edits

## Command-Line Interface

### Current Usage

```bash
# Required: repository (positional argument)
dalec-mapping owner/repo

# With optional Dockerfile
dalec-mapping owner/repo -dockerfile path/to/Dockerfile

# With previous spec for validation
dalec-mapping owner/repo -spec previous-spec.yml

# Full example with all options
dalec-mapping Ryuki-997/HelloWorld \
  -dockerfile custom/Dockerfile \
  -spec previous/output.yml \
  -output generated/new-spec.yml \
  -v
```

### Flags

- **Positional**: `repository` - GitHub repository in `owner/repo` format (REQUIRED)
- `-dockerfile string` - Path to Dockerfile (default: "./Dockerfile")
- `-spec string` - Path to previous spec file for validation and comparison
- `-output string` - Output path for generated spec (default: "output.yml")
- `-v` - Verbose mode for detailed logging

## Future Roadmap
### 1. Spec Validation Tool
```bash
# Validate spec file structure
dalec-mapping validate spec.yml

# Compare two spec files
dalec-mapping diff old-spec.yml new-spec.yml
```

**Features**:
- Schema validation against Dalec spec requirements
- Field completeness checks
- Semantic validation (e.g., valid commit hashes, reachable URLs)
- Drift detection from upstream repository

#### 2. CLI Commands
```bash
# Update specific field
dalec-mapping set spec.yml build.env.VERSION 2.0.0

# Add new source
dalec-mapping add-source spec.yml mysource https://example.com/file.tar.gz

# Remove field
dalec-mapping unset spec.yml build.steps[2]

# Bulk update from JSON
dalec-mapping update spec.yml --patch changes.json
```

#### 3. Programmatic API
```go
import "github.com/dalec-mapping/api"

// Load spec
spec, err := api.LoadSpec("spec.yml")

// Modify using fluent API
spec.Set("build.env.VERSION", "2.0.0").
     AddSource("mysource", "https://example.com/file.tar.gz").
     RemoveField("build.steps[2]")

// Save with validation
err = spec.Save("spec.yml")
```

- Automated spec updates in CI/CD pipelines
- Scriptable modifications for bulk operations
- Type-safe programmatic access
- Validation at modification time

### 4. Multi-Repository Support
- GitLab integration
- Bitbucket support
- Generic Git repository handling (self-hosted)
- Monorepo support (multiple Dockerfiles)

### 5. Advanced Compliance Testing & Suggestions
- Apply standard testing for compliance
- Suggest dependencies based on Dockerfile analysis
- Auto-detect test commands

**Last Updated**: January 2, 2026  
**Version**: 1.0  
**Status**: Active Development
