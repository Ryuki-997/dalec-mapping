# Architecture — Domain Struct Workflow

This document describes how data flows through the domain types across the
three pipeline phases. Every struct lives under [domain/](domain/) in a single,
focused package; orchestration code in [workflow/orchestration/](workflow/orchestration/)
threads `*workplan.WorkItem` through explicit function arguments rather than
mutating package globals.

## Pipeline Phases

```bash
Phase 1  Resolve   ()                              → workplan.WorkPlan
Phase 2  Generate  workplan.WorkPlan               → []buildresult.BuildResult
Phase 3  Publish   []buildresult.BuildResult       → []prbatch.PRBatch → []PublishOutcome
```

Wiring lives in [main.go](main.go); phase entry points are in
[phase1_resolve.go](workflow/orchestration/phase1_resolve.go),
[phase2_generate.go](workflow/orchestration/phase2_generate.go), and
[phase3_publish.go](workflow/orchestration/phase3_publish.go).

## Domain Packages

| Package | Purpose | Key Types |
|---|---|---|
| [domain/onboarding](domain/onboarding/) | YAML schema of partner `onboard.yml` files | `OnboardFile`, `OnboardingComponent` |
| [domain/tags](domain/tags/) | Tag pattern matching and resolved tag data | `Patterns`, `Set` |
| [domain/naming](domain/naming/) | Identity + derived names for a component+tag | `Naming` |
| [domain/repository](domain/repository/) | Source repo metadata + generator detection | `RepoInfo`, `SourceGenerator` |
| [domain/contents](domain/contents/) | Parsed Dockerfile/Makefile + spec model | `DockerfileInfo`, `MakefileInfo`, `DockerSpec`, `BuildTarget` |
| [domain/workplan](domain/workplan/) | Unit of work for the pipeline | `WorkItem`, `WorkPlan`, `BuildFilesInfo` |
| [domain/tagcache](domain/tagcache/) | Global repo → tag → SHA cache | `Cache`, `Init`, `Lookup` |
| [domain/buildresult](domain/buildresult/) | Phase 2 per-item output | `BuildResult`, `Outcome` |
| [domain/prbatch](domain/prbatch/) | Phase 3 PR grouping | `PRGroupKey`, `BatchComponent`, `PRBatch` |

## Dependency Graph

```bash
flowchart TD
    onboarding --> naming
    tags --> naming
    tags --> workplan
    naming --> workplan
    repository --> workplan
    contents --> workplan

    workplan --> buildresult
    buildresult --> prbatch
    naming --> prbatch

    tagcache
```

All edges point from leaf data toward composite types. No cycles. `tagcache`
is a leaf the pipeline phases read into directly.

## `workplan.WorkItem` Lifecycle

`workplan.WorkItem` is the single unit threaded through the pipeline. It has
two identity fields populated in order during Phase 1, plus a `BuildFiles`
struct that fills incrementally during Phase 2:

```go
type WorkItem struct {
    Naming     naming.Naming   // (1) onboard.yml + path-derived + generated names
    Tag        tags.Set        // (2) one actionable tag matched against the repo
    BuildFiles BuildFilesInfo  // (3) populated in Phase 2 (Dockerfile/Makefile/Spec/RepoInfo)
}
```

Functions take `*WorkItem` so each phase can write to the fields it owns
without copying derived data. There are no ambient package globals for the
in-flight item; the only shared global is `tagcache.Cache`, which is read-only
after Phase 1.

## Struct Workflow by Phase

### Phase 1 — Resolve

Input: an onboard input path (file or directory under the spec repo).
Output: `workplan.WorkPlan{Items, ExistingPaths}`.

Driver: [`workflow/services/specrepo.FetchComponents`](workflow/services/specrepo/onboard.go)
→ [`workflow/services/partnerrepo.ResolveTagCache`](workflow/services/partnerrepo/tagcache.go).

1. **Parse onboard files** → `onboarding.OnboardFile` (auto-detects standalone
   components vs groups, validates `Targets`).
2. **Walk each `onboarding.OnboardingComponent`** → seed a `naming.Naming` with the
   embedded YAML config plus path-derived runtime fields (`OnboardDir`,
   `SpecImageName`, `SpecRepository`, `GroupName`). The single-standalone rule
   collapses the partner-prefix when an onboard file has exactly one component
   whose name matches the partner folder.
3. **Fetch and cache tags** per `Repository` → populate
   `tagcache.Cache[repoURL][tagName] = commitSHA`. The same repo is fetched at
   most once per pipeline run; subsequent components from that repo reuse the
   cached map.
4. **Match `tags.Patterns` against repo tags** → one `tags.Set` per actionable
   tag (`Full`, `Stripped`, `Version`, `Revision`). `Revision` is the *next*
   revision to create (latest+1 when a prior revision exists, else 1).
5. **Call `Naming.Construct(tagSet)`** → fills `DisplayName`, `VersionRevision`,
   `FolderPath`, `SpecFilePath`. `BranchName` / `PRTitle` stay empty until a
   PRID is assigned in Phase 3.
6. **Emit one `workplan.WorkItem`** per `(Naming, tags.Set)`. `BuildFiles`
   stays zero-valued.
7. **Wrap items + existing remote paths** into `workplan.WorkPlan`.

### Phase 2 — Generate

Input: `workplan.WorkPlan`.
Output: `[]buildresult.BuildResult` (one per item; never nil).

Driver: [`Generate` / `GenerateOne`](workflow/orchestration/phase2_generate.go).
For each `workplan.WorkItem`, `resolveAction(item, existingPaths)` picks one of
four actions, then `dispatchAction` runs it:

| Action | Trigger | Outcome |
|---|---|---|
| `actionSkip` | `Tag.Revision > 1` and the prior spec's commit matches the cached commit for `Tag.Full` | `OutcomeSkipped` |
| `actionBumpRevision` | `Tag.Revision > 1` and the commit changed | `OutcomeBumpRevision` |
| `actionBumpVersion` | New version, template spec found, and the new tag's BuildFiles byte-match the template tag's BuildFiles | `OutcomeBumpVersion` |
| `actionGenerate` | Otherwise | `OutcomeGenerated` (or `OutcomeFailed` if generation fails) |

The decision flow for new versions (`Revision == 1`):

1. **Discover build files** —
   [`partnerrepo.DiscoverBuildFiles(item)`](workflow/services/partnerrepo/discover.go)
   fetches the partner-repo Dockerfile and Makefile at `Tag.Full` and writes
   them into `item.BuildFiles.Dockerfile.Source` / `item.BuildFiles.Makefile.Source`.
2. **Find template spec** —
   [`specapi.SpecRepoFindLatestMinorVersion`](workflow/infrastructure/specapi/specrepo.go)
   looks up the latest same-minor-version spec in the spec repo. If none is
   found, jump straight to `actionGenerate`.
3. **Extract template version** — `specapi.SpecRepoExtractTemplateVersion`
   pulls the X.Y.Z version out of the template spec's filename.
4. **Derive template tag** — `deriveTemplateTag(item, templateVersion)`
   reconstructs the partner-repo tag for the template version using the
   workitem's own `Tag.Full`/`Tag.Stripped` as a prefix reference (e.g.
   `azure-ipam/v0.4.0` vs flat `v0.4.0`) and verifies the tag exists in
   `tagcache.Cache`.
5. **Fetch template-tag build files** —
   `partnerrepo.FetchBuildFilesAtTag(item, templateTag)` returns the
   Dockerfile/Makefile bytes at the template tag without mutating the item.
6. **Compare** — `buildFilesMatch` byte-equals both files after trimming
   trailing newlines. Match → `actionBumpVersion`; differ → `actionGenerate`.

Each action runs through [`workflow/services/spec`](workflow/services/spec/):

- **`spec.DetectRevisionBump`** — compares the existing prior-revision spec's
  commit against `tagcache.Lookup(repo, Tag.Full)`.
- **`spec.BumpRevision`** — copies the latest existing same-version spec and
  updates only `args.COMMIT`.
- **`spec.BumpVersion`** — copies the template spec and updates `args.COMMIT`
  and `args.VERSION`.
- **`spec.GenerateSpec`** — populates `BuildFiles.RepoInfo` from GitHub/ADO,
  parses the Dockerfile/Makefile, extracts `BuildFiles.Spec` (binaries,
  pipeline steps, entrypoint, symlink), and runs the transformer to emit a
  fresh DALEC spec YAML.

The result is wrapped in
`buildresult.BuildResult{Item, Outcome, SpecContent, Err}`. `IsPublishable()`
is true for `OutcomeBumpVersion`, `OutcomeBumpRevision`, and `OutcomeGenerated`.

### Phase 3 — Publish

Input: `[]buildresult.BuildResult`.
Output: `[]prbatch.PRBatch` → `[]PublishOutcome`.

Split into a pure grouping pass and an impure publish pass:

1. **`GroupIntoBatches(results, idGen)`** — drops non-publishable results,
   groups the rest by `prbatch.PRGroupKey{GroupName, Tag.Stripped}` in
   insertion order, assigns each batch a fresh `PRID` from the injected
   generator (`naming.GeneratePRID` in production), and stores per-component
   `prbatch.BatchComponent{Result, Naming: result.Item.Naming.WithPRID(prID)}`.
   `WithPRID` is the only place `BranchName` and `PRTitle` get filled.
2. **`Publish(batches)`** — calls
   [`specrepo.CreatePR(batch)`](workflow/services/specrepo/pr.go) for each
   batch. `collectSiblingFiles` writes the spec plus, for non-bump-revision
   outcomes, the BuildFiles snapshot
   `<OnboardDir>/BuildFiles/<SpecImageName>-<Tag.Version>.df|.mk` so later
   runs can compare against it. Snapshot files that already exist remotely
   (per `WorkPlan.ExistingPaths`) are skipped. Returns one `PublishOutcome`
   per batch (failures isolated).

## The `Naming` Struct in Detail

`naming.Naming` is the spine of the pipeline. Each section has a clearly
defined population point:

| Section | Source | When populated |
|---|---|---|
| Embedded `onboarding.OnboardingComponent` | YAML | Phase 1 parse |
| Runtime (`OnboardDir`, `SpecImageName`, `SpecRepository`, `GroupName`) | Walk of `OnboardFile` | Phase 1 per-component |
| Generated (`DisplayName`, `VersionRevision`, `FolderPath`, `SpecFilePath`) | `Construct(tagSet)` | Phase 1 after tag resolution |
| Generated (`BranchName`, `PRTitle`) | `WithPRID(prID)` | Phase 3 during grouping |

This split keeps the contract explicit: callers know exactly which fields are
safe to read in each phase.

## Global State

`tagcache.Cache map[string]map[string]string` is the only mutable global. It
is populated once during Phase 1 via `tagcache.Init()` plus per-repo fetches
in `partnerrepo.ResolveTagCache`, then read by `tagcache.Lookup()` from
`spec.DetectRevisionBump`, `spec.BumpRevision`, `spec.BumpVersion`,
`spec.GenerateSpec`, and `deriveTemplateTag` in Phase 2. Nothing writes to it
after Phase 1.

Everything else flows through explicit function arguments and return values.
