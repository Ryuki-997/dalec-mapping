# Architecture — Domain Struct Workflow

This document describes how data flows through the domain types across the
three pipeline phases. Every struct lives under [domain/](domain/) in a single,
focused package; orchestration code in [workflow/orchestration/](workflow/orchestration/)
threads `*workplan.WorkComponent` through explicit function arguments rather than
mutating package globals.

## Pipeline Phases

```bash
Phase 1  Resolve   (inputPath)                     → []workplan.WorkGroup
Phase 2  Generate  []workplan.WorkGroup            → mutates component.Result on each WorkComponent
Phase 3  Publish   []workplan.WorkGroup            → []PublishOutcome
```

Wiring lives in [main.go](main.go); phase entry points are in
[phase1_resolve.go](workflow/orchestration/phase1_resolve.go),
[phase2_generate.go](workflow/orchestration/phase2_generate.go), and
[phase3_publish.go](workflow/orchestration/phase3_publish.go). The same
`[]workplan.WorkGroup` slice threads all three phases — Phase 2 writes
`buildresult.BuildResult` onto each `WorkComponent` in place; Phase 3 reads
it back, no separate batching layer.

## Domain Packages

| Package | Purpose | Key Types |
|---|---|---|
| [domain/onboarding](domain/onboarding/) | YAML schema of partner `onboard.yml` files | `OnboardFile`, `OnboardingComponent` |
| [domain/tags](domain/tags/) | Tag pattern matching and resolved tag data | `Patterns`, `Set` |
| [domain/naming](domain/naming/) | Identity + derived names for a component+tag | `Naming` |
| [domain/repository](domain/repository/) | Source repo metadata + generator detection | `RepoInfo`, `SourceGenerator` |
| [domain/contents](domain/contents/) | Parsed Dockerfile/Makefile + spec model | `DockerfileInfo`, `MakefileInfo`, `DockerSpec`, `BuildTarget` |
| [domain/workplan](domain/workplan/) | Unit of work for the pipeline | `WorkComponent`, `WorkGroup`, `BuildFilesInfo` |
| [domain/tagcache](domain/tagcache/) | Global repo → tag → SHA cache | `Cache`, `Init`, `Lookup`, `LookupCommit` |
| [domain/pathcache](domain/pathcache/) | Global spec-repo path index + path builders | `Cache`, `Init`, `Has`, `BuildDockerfilePath`, `BuildMakefilePath` |
| [domain/buildresult](domain/buildresult/) | Phase 2 per-item output | `BuildResult`, `Outcome` |

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

    tagcache
    pathcache
```

All edges point from leaf data toward composite types. No cycles. `tagcache`
and `pathcache` are leaves the pipeline phases read into directly; both are
process-global maps populated once in Phase 1 (see §1).

## `workplan.WorkComponent` Lifecycle

`workplan.WorkComponent` is the single unit threaded through the pipeline. It has
two identity fields populated in order during Phase 1, plus a `BuildFiles`
struct that fills incrementally during Phase 2:

```go
type WorkComponent struct {
    Naming     naming.Naming   // (1) onboard.yml + path-derived + generated names
    Tag        tags.Set        // (2) one actionable tag matched against the repo
    BuildFiles BuildFilesInfo  // (3) populated in Phase 2 (Dockerfile/Makefile/Spec/RepoInfo)
    Result     buildresult.BuildResult // (4) written in place by Phase 2
}
```

Functions take `*WorkComponent` so each phase can write to the fields it owns
without copying derived data. The only ambient package globals are the two
caches `tagcache.Cache` and `pathcache.Cache` — both written exclusively in
Phase 1 (see §1) and read-only afterwards.

## §1 — Component Readiness & Global Caches

```text
orchestration.Resolve(inputPath)
└─ specrepo.FetchComponents(inputPath)
   ├─ tagcache.Init()                ─── clears tagcache.Cache
   ├─ pathcache.Init()               ─── clears pathcache.Cache
   ├─ specapi.SpecRepoFetchTree()    ─── BULK fill pathcache (once)
   └─ buildGroups → expandGroups
      └─ partnerrepo.ResolveTagCache
         └─ fetchComponentTags(URL)  ─── LAZY fill tagcache (per repo)

┌─────────────────────────────────────────────────────────────────┐
│ pathcache.Cache                                                 │
│   map[path]bool                                                 │
│   Bulk-filled ONCE from the spec-repo tree.                     │
│   Readers:  Has, BuildDockerfilePath, BuildMakefilePath         │
├─────────────────────────────────────────────────────────────────┤
│ tagcache.Cache                                                  │
│   map[repoURL]map[tag]commitSHA                                 │
│   Per-repo LAZY fill on first component referencing that repo.  │
│   Readers:  Lookup, LookupCommit                                │
└─────────────────────────────────────────────────────────────────┘

Read-only consumers in Phase 2 / 3:
  §2 buildComponentsForTag → semver.FindLatestRevision
  §3 fetchSnapshot          (no-network fast path)
  §3 semver.FindTemplateVersion
  §3 spec.DetectRevisionBump / BumpRevision / BumpVersion
  §4 spec.GenerateSpec → buildRepoInfo (tag → SHA lookup)
  §5 collectSiblingFiles    (skip commit when already remote)
```

Phase 1 is the only phase that **writes**. Everything downstream is read-only.

- `tagcache.Init()` and `pathcache.Init()` zero the package-level maps at the
  start of every run so a long-lived process (or a test harness) starts clean.
- `specapi.SpecRepoFetchTree()` walks the entire spec repo via the GitHub
  trees API once and calls `pathcache.Set(path)` for every entry. After that
  step, every "does this remote file exist?" question is answered locally.
- Tag fetching is lazy on a per-repo basis. The first decoded WorkGroup
  pointing at `repoURL` triggers `fetchComponentTags(repoURL)`, which calls
  `semver.FetchRepoTags(repoURL)` and stores the resulting `map[tag]sha`
  under that URL. Sibling components from the same partner repo skip the
  fetch and reuse the cached map.
- `tagcache.LookupCommit(fullTag)` is a convenience that walks **every**
  cached repo and returns the first match. It is used by Phase 2 helpers
  that operate on an isolated `*WorkComponent` without a `Group` reference;
  the assumption is no two partner repos publish the same tag literal in a
  single run.

This dual-cache foundation is what lets every later phase decide actions and
skip writes without making fresh network calls in the hot path.

## §2 — Onboard → Group → Component Fan-Out

```text
specs/cni/onboard.yml
    │
    ▼
workplan.Decode(rawBytes)
  detects grouped vs standalone layout
  emits 1+ decoded WorkGroup(s)
    │
    ▼
┌───────────────────────────────────────────────┐
│ decoded WorkGroup (skeleton)                  │
│   GroupName   = "azure-cni"                   │
│   Repository, TagPatterns, License, Reviewers │
│   Components  = [skel1, skel2, …]             │
│   PRID, Tag, Naming.Generated  ← zero-valued  │
└───────────────────────────────────────────────┘
    │
    ▼
partnerrepo.ResolveTagCache(decodedGroup)
  fetchComponentTags(group.Repository)
  resolveMatchedTagNames(group, repoTags)
  N matched tag names  →  N runtime WorkGroups
    │
    ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│ WorkGroup #1 │  │ WorkGroup #2 │  │ WorkGroup #N │
│ Tag=v1.0.0   │  │ Tag=v1.1.0   │  │ Tag=v1.N.0   │
│ PRID=a1b2c3  │  │ PRID=d4e5f6  │  │ PRID=xxxxxx  │
│              │  │              │  │              │
│ Components:  │  │ Components:  │  │ Components:  │
│  WorkComp#1  │  │  WorkComp#1  │  │  WorkComp#1  │
│  …           │  │  …           │  │  …           │
│  WorkComp#M  │  │  WorkComp#M  │  │  WorkComp#M  │
└──────────────┘  └──────────────┘  └──────────────┘

Each WorkComponent's Naming.Construct(tag, revision, group, PRID)
fills DisplayName, VersionRevision, FolderPath, SpecFilePath,
BranchName, PRTitle — atomically once the WorkGroup knows its PRID.
```

The fan-out math: **1 onboard.yml × G decoded groups × N matched tags × M
declared components = G·N·M runtime WorkComponents** organised into G·N
runtime WorkGroups.

- Two onboard layouts are accepted, distinguished by the presence of a
  `components:` mapping under the group key. The **grouped** layout
  enumerates component build files explicitly; the **standalone** layout
  inlines a single component's `dockerfile:`/`makefile:` at group level. In
  both cases `Naming.OnboardDir` is set per component and `DeriveAtomic()`
  derives `SpecRepository` + `SpecImageName` from that path.
- `buildRuntimeGroup` mints **one PRID per runtime WorkGroup** via
  `naming.GeneratePRID()` (`YYYYMMDD-xxxxxx`). Every cloned component under
  the same runtime WorkGroup shares that PRID — this is what collapses the
  whole group into one feature branch and one PR in §5.
- Revision assignment is **per-component**, not per-group:
  `semver.FindLatestRevision(baseNaming, tag.Version)` walks the
  `pathcache.Cache` for existing same-version specs under that component's
  `SpecRepository`/`SpecImageName`. Two components with the same tag may get
  different next revisions if their spec-repo histories differ.
- `Naming.Construct(tag, revision, groupName, prID)` is the single call that
  fills every generated field — `DisplayName`, `VersionRevision`,
  `FolderPath`, `SpecFilePath`, `BranchName`, `PRTitle` — atomically once
  the runtime WorkGroup knows its PRID. There is no separate Phase 3
  `WithPRID(prID)` step; Naming is fully populated by the end of Phase 1.

## §3 — Action Resolution Decision Tree

```text
resolveAction(*WorkComponent)
│
├── Revision > 1 ?
│   │
│   ├── YES  →  spec.DetectRevisionBump
│   │             compare prior spec's COMMIT
│   │             vs tagcache.LookupCommit(Tag.Full)
│   │             │
│   │             ├── unchanged  →  actionSkip          [Outcome: Skipped]
│   │             └── changed    →  actionBumpRevision  [Outcome: BumpRevision]
│   │
│   └── NO  (Revision == 1)
│       │
│       └── partnerrepo.DiscoverBuildFiles(component)
│             fetch partner Dockerfile/Makefile @ Tag.Full,
│             write to BuildFiles.{Dockerfile,Makefile}.Source
│             │
│             └── semver.FindTemplateVersion(component)
│                   scan pathcache for prior same-major snapshots
│                   │
│                   ├── none  →  actionGenerate  [Outcome: Generated/Failed]
│                   │
│                   └── found →  fetchSnapshot(df, mk)
│                                  pathcache.Has? spec-repo fetch : nil
│                                  │
│                                  └── buildFilesMatch?
│                                        byte-equal sources, trim trailing \n
│                                        │
│                                        ├── no   →  actionGenerate     [Outcome: Generated/Failed]
│                                        └── yes  →  actionBumpVersion  [Outcome: BumpVersion]
```

`resolveAction` returns `(pipelineAction, templateMinor string)`.
`templateMinor` is populated only for `actionBumpVersion` — it is passed
through `dispatchAction → spec.BumpVersion` as a function parameter and is
**not** stored on the component (a deliberate refactor away from carrying
ephemeral per-phase metadata on the long-lived domain struct).

- **`Revision > 1` branch** assumes a spec at `Revision-1` already exists in
  the spec repo. `spec.DetectRevisionBump` reads that prior spec's
  `args.COMMIT` and compares against `tagcache.LookupCommit(Tag.Full)`. Same
  commit → nothing to do (`actionSkip`). Different commit → republish the
  same version with the new commit (`actionBumpRevision`, copies the
  existing spec and only rewrites `args.COMMIT`).
- **`Revision == 1` branch** (no prior spec for this exact version) always
  fetches the partner-repo `Dockerfile` / `Makefile` first via
  `DiscoverBuildFiles`, then asks `FindTemplateVersion` whether any prior
  minor under the same major has a `BuildFiles/` snapshot recorded in
  `pathcache`. No snapshot → straight to `actionGenerate`. Snapshot found →
  fetch it through `fetchSnapshot`, which is a pure pathcache-gated wrapper
  around `specapi.SpecRepoFetchFile` (returns `nil` without any network call
  when the path is not in the cache).
- **`buildFilesMatch`** byte-compares the fresh partner-repo files against
  the template snapshot after trimming trailing newlines. A match means the
  source build instructions did not change between minors, so the template
  spec can simply be copied with `args.VERSION` + `args.COMMIT` updated
  (`actionBumpVersion`). Any difference forces `actionGenerate`, which
  re-runs the full parse-and-transform pipeline (see §4).

The action and `templateMinor` are then handed to `dispatchAction`, which
delegates to one of `spec.BumpRevision`, `spec.BumpVersion`, or
`spec.GenerateSpec` and wraps the return in a `buildresult.BuildResult`
stored on `component.Result`. `IsPublishable()` is true for
`OutcomeBumpVersion`, `OutcomeBumpRevision`, and `OutcomeGenerated`; false
for `OutcomeSkipped` and `OutcomeFailed`.

## §4 — Spec Generation Pipeline

```text
spec.GenerateSpec(component)
    │
    ▼
┌────────────────────────────────────────────────────────────────────────┐
│ Stage 1 — parseAndExtract                                              │
│                                                                        │
│ parser.ParseOptionalFileInfo(component, dockerfileBytes, makefileBytes)│
│   → component.BuildFiles.Dockerfile.Stages[]                           │
│       (FROM / RUN / COPY / WORKDIR / ENTRYPOINT / CMD …)               │
│   → component.BuildFiles.Makefile.Targets[]                            │
│       (name, deps, recipe commands)                                    │
│ parser.ExtractStaticBuildValues(dockerfile)                            │
│   → component.BuildFiles.Spec                                          │
│       (binaries, pipelineSteps, entrypoint, symlinks, finalLinuxBase)  │
└────────────────────────────────────┬───────────────────────────────────┘
                                     ▼
┌────────────────────────────────────────────────────────────────────────┐
│ Stage 2 — buildRepoInfo                                                │
│                                                                        │
│ github.FetchRepoInfo(repoURL)        owner, defaultBranch,             │
│   OR ado.FetchADORepoInfo(repoURL)   cloneURL, license                 │
│ resolveLicense(configured, fetched)  configured > fetched              │
│ tagcache.Lookup(repoURL, Tag.Full)   → LatestCommit SHA                │
│ parser.DetectGoToolchainPin(...)     → GoVersion (Go image only)       │
│ ado.DetectADOGenerator(...)          (ADO repos only)                  │
│                                                                        │
│   → component.BuildFiles.RepoInfo populated                            │
└────────────────────────────────────┬───────────────────────────────────┘
                                     ▼
┌────────────────────────────────────────────────────────────────────────┐
│ Stage 3 — buildSpec                                                    │
│                                                                        │
│ transformer.TransformToDalec(component)  → dalec.Spec                  │
│   (sources, dependencies, targets, licenses, symlinks)                 │
│ parser.EncodeDalecSpec(spec)             → []byte (YAML)               │
└────────────────────────────────────┬───────────────────────────────────┘
                                     ▼
                  component.Result.SpecContent = []byte
```

The generation pipeline is a strict three-stage fold: every stage adds new
fields to `component.BuildFiles` and never mutates earlier ones.

- **Stage 1 (parse + extract)** is pure CPU — no network. Dockerfile and
  Makefile bytes were already fetched by `DiscoverBuildFiles` back in §3.
  `parser.ParseOptionalFileInfo` produces the structured `Stages[]` /
  `Targets[]`; `parser.ExtractStaticBuildValues` walks the stages and
  distills the binary list, pipeline steps, entrypoint, symlinks, and the
  final Linux base image into `BuildFiles.Spec`.
- **Stage 2 (buildRepoInfo)** is where the upstream provider matters. ADO
  repos route through `ado.FetchADORepoInfo` + `ado.DetectADOGenerator` to
  capture the pipeline generator metadata; everything else routes through
  `github.FetchRepoInfo`. License resolution prefers the operator-supplied
  `License` field on the onboard group over whatever the upstream API
  reports; `"proprietary"` is the final fallback. The commit SHA is read
  from `tagcache.Cache` (already populated in §1), so this stage performs
  one or two HTTP calls per component for the metadata only.
- **Stage 3 (buildSpec)** is again pure CPU. `transformer.TransformToDalec`
  composes a `dalec.Spec` Go value from all the upstream pieces, and
  `parser.EncodeDalecSpec` marshals it to YAML. The result is the spec file
  bytes ultimately committed to the spec repo in §5.

`spec.BumpVersion` and `spec.BumpRevision` short-circuit this pipeline:
`BumpVersion` copies the template spec located via the `templateMinor`
parameter and only updates `args.VERSION` + `args.COMMIT`; `BumpRevision`
copies the immediately-prior same-version spec and updates only
`args.COMMIT`. Both still produce a `BuildResult{SpecContent: ...}` so
Phase 3 treats them uniformly.

## §5 — Per-Group PR Publish

```text
orchestration.Publish(groups)
  FOR EACH WorkGroup in groups:
    │
    ▼
┌──────────────────────────────────────────────────────────────┐
│ specrepo.CreatePR(group)                                     │
│                                                              │
│  1. collectPublishableComponents(group)                      │
│       filter: component.Result.IsPublishable()               │
│       drops:  Skipped, Failed                                │
│       keeps:  Generated, BumpVersion, BumpRevision           │
│     if publishable is empty → return (no PR opened)          │
│                                                              │
│  2. findExistingPR(publishable[0].Naming.BranchName)         │
│       all siblings share PRID → same branch name             │
│       found & !ForcePR → REUSE existing PR, return           │
│                                                              │
│  3. createFeatureBranch(branchName)                          │
│       fork OnboardBranch tip → refs/heads/branchName         │
│                                                              │
│  4. collectFiles(publishable)                                │
│       FOR EACH component:                                    │
│         + Naming.SpecFilePath ← Result.SpecContent           │
│         → collectSiblingFiles(component):                    │
│             skip if Outcome == BumpRevision                  │
│             skip if BuildFile.Source is empty                │
│             skip if pathcache.Has(snapshotPath)              │
│             else + <snapshotPath>.df / .mk                   │
│                                                              │
│  5. commitAllFiles(branch, message, files)                   │
│       single atomic commit via the Git Data API:             │
│       createBlob × N → createTree → createCommit →           │
│       updateBranchRef                                        │
│                                                              │
│  6. createPullRequest(title, body, branch)                   │
│       title = publishable[0].Naming.PRTitle                  │
│       body  = singular (1 comp) or plural with "Components:" │
│       label = "specfile"                                     │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
PublishOutcome{GroupName, URL, Created, SpecPaths, Err}
```

Phase 3 is strictly one PR per runtime WorkGroup — there is no cross-group
batching layer. Because every component under a WorkGroup shares the same
PRID (minted in §2), they share the same `BranchName` and the same
`PRTitle`, so a single `findExistingPR` query suffices for de-duplication.

- `IsPublishable()` is the only filter. `Skipped` (commit unchanged at this
  revision) and `Failed` (generation error in §4) are dropped silently; if
  every component in a group is non-publishable the entire group produces
  no PR.
- `collectSiblingFiles` is the snapshot-discipline pass. Snapshots live at
  `pathcache.BuildDockerfilePath(naming, tag.MajorMinor)` and the matching
  `BuildMakefilePath`. Three conditions suppress a snapshot commit:
  (1) the outcome was `BumpRevision`, meaning the same minor already has a
  snapshot from the prior revision; (2) the `BuildFile.Source` is empty
  (typically because no Makefile exists upstream); (3) `pathcache.Has`
  reports the snapshot is already in the spec repo from an earlier run.
- The commit is a single atomic Git Data API sequence: one blob per file,
  one tree built off the feature branch's parent tree, one commit, one
  branch ref update. This keeps PRs deterministically diffable even when
  a group spans many components.
- `ForcePR=true` (CLI flag `-force-pr`) skips `findExistingPR` so a fresh
  PR is opened even when an open one exists. Useful for re-runs after
  manual branch surgery.

## The `Naming` Struct in Detail

`naming.Naming` is the spine of the pipeline. Each section has a clearly
defined population point — all of which now happen during Phase 1, so any
downstream phase can safely read any field:

| Section | Source | When populated |
|---|---|---|
| Embedded `onboarding.OnboardingComponent` | YAML | Phase 1 decode |
| Atomic (`OnboardDir`, `SpecImageName`, `SpecRepository`) | `DeriveAtomic()` over `OnboardDir` | Phase 1, in `buildComponentsForTag` |
| Generated (`DisplayName`, `VersionRevision`, `FolderPath`, `SpecFilePath`, `BranchName`, `PRTitle`) | `Construct(tag, revision, groupName, prID)` | Phase 1, end of `buildRuntimeGroup` |

This split keeps the contract explicit: by the time Phase 2 sees a
`WorkComponent`, every field of its `Naming` is final.

## Global State

There are exactly two mutable package-level globals, both maps, both
populated in Phase 1, both read-only afterwards:

| Global | Type | Written by (Phase 1 only) | Read by |
|---|---|---|---|
| `tagcache.Cache` | `map[repoURL]map[tagName]commitSHA` | `tagcache.Init` (clear) → `partnerrepo.fetchComponentTags` (per-repo lazy fill) | §2 `buildComponentsForTag` (via `semver.FindLatestRevision`), §3 `spec.DetectRevisionBump`, §3 `deriveTemplateTag`, §4 `spec.GenerateSpec → buildRepoInfo` |
| `pathcache.Cache` | `map[path]bool` (plus `BuildDockerfilePath` / `BuildMakefilePath` helpers) | `pathcache.Init` (clear) → `specapi.SpecRepoFetchTree` (single bulk fill via `pathcache.Set`) | §2 `semver.FindLatestRevision`, §3 `semver.FindTemplateVersion`, §3 `fetchSnapshot`, §5 `collectSiblingFiles` |

`tagcache.LookupCommit(fullTag)` is the "no group context" walker used by
Phase 2 helpers operating on an isolated `*WorkComponent`; it scans every
cached repo and returns the first match.

Everything else flows through explicit function arguments and return values.
There is no other ambient state; every test can run a single phase against
a hand-built `WorkGroup` value with only the two `Cache.Init()` calls as
fixture.
