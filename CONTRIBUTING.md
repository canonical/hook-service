# Contributing

## Developing

Please install the `pre-commit` to enforce the code conventions and alignment.

```shell
pip install pre-commit
```

Install the required pre-commit hooks.

```shell
pre-commit install -t commit-msg
```

## OpenSpec Workflow

Use this flow for spec-driven changes. The **spec is the single source of truth**: its `## Purpose` section carries the design rationale (intent, goals, non-goals, key decisions) ADR-style, so we don't keep separate ADR/design files.

1. Propose: `openspec new change <name>` and complete `proposal.md`, `design.md`, `tasks.md`, and delta specs.
2. Implement: complete tasks in `openspec/changes/<name>/tasks.md`.
3. Validate: run `openspec validate --all`. This is the structural/drift check — it fails on malformed specs or deltas.
4. Archive: run `openspec archive <name>`. This updates the main specs (applies deltas into `openspec/specs/`) and moves the change under `openspec/changes/archive/`. There is no separate `sync` command; `archive` is the sync.

Commit active changes under `openspec/changes/` so they're reviewed in their PR. The archive (`openspec/changes/archive/`) is gitignored — its rationale already lives in the spec `## Purpose`.

## Spec Writing Policy (Human + Agent)

Keep specs layered so they are readable by humans and usable by agents:

1. `## Purpose` is the **why**: concise context, goals/non-goals, key decisions, and major trade-offs.
2. `## Requirements` is the **what**: normative, testable behavior using `MUST`/`SHALL`/`SHOULD`/`MAY` with scenario blocks.
3. Do not copy proposal/design/tasks verbatim into specs.
4. Keep implementation planning detail in change artifacts (`design.md`, `tasks.md`), not in canonical specs.

## Vulnerability Checking

All code and dependencies are scanned for known vulnerabilities before building rock images or merging PRs:

```shell
make govulncheck
```

This target runs `govulncheck` directly on all Go packages and dependencies.

## Branch Builds & Release Pipeline

### Branch Builds (`main`)

Pushes to the `main` branch trigger the `gh-publish-branch` workflow job, which publishes container images with `latest` and commit SHA tags to GitHub Packages (GHCR). Published images are subsequently scanned for vulnerabilities by the `scan` job.

### Tagged Releases (Quarantine-Before-Release Gate)

Releases follow a **quarantine-before-release gate** to ensure that no publicly available artifact contains un-triaged CVEs:

1. **Pre-release Creation**: `release-please` creates a GitHub pre-release and tags the commit (`vX.Y.Z`).
2. **Candidate Publishing**: On tag push, CI builds the OCI rock artifact and uploads it to GHCR as a candidate tag (`vX.Y.Z-candidate`) via `gh-publish-candidate`. Neither `stable` nor the unadorned `vX.Y.Z` tag are pushed yet.
3. **Security Gate**: Trivy scans the candidate image with strict enforcement (`exit-code: 1`, `severity: HIGH,CRITICAL`, using `.trivyignore`).
4. **Promotion**: Only when scans pass cleanly does the promotion job run:
   - Uses Skopeo to promote the candidate image directly in the registry to `vX.Y.Z` and `stable`.
   - Uses the GitHub CLI to graduate the pre-release to a standard, stable, latest release.
5. **Continuous Monitoring**: Scheduled weekly scans (`.github/workflows/cves.yaml`) continuously monitor `ghcr.io/canonical/hook-service:stable` for newly disclosed CVEs.

### Troubleshooting & Release Recovery

If the automated `promote` job fails (for instance, due to a transient registry network glitch or temporary authentication timeout) after a release tag has been pushed:

1. The GitHub release created by `release-please` remains quarantined as a pre-release (`prerelease: true`).
2. The candidate image (`vX.Y.Z-candidate`) remains in GHCR without touching `stable` or `vX.Y.Z`.
3. To recover, either re-run the failed `promote` workflow job from the GitHub Actions UI once the transient issue is resolved, or perform manual promotion and graduation:
   ```shell
   # Retag candidate image to release version and stable in GHCR
   skopeo copy --all docker://ghcr.io/canonical/hook-service:vX.Y.Z-candidate docker://ghcr.io/canonical/hook-service:vX.Y.Z
   skopeo copy --all docker://ghcr.io/canonical/hook-service:vX.Y.Z-candidate docker://ghcr.io/canonical/hook-service:stable

   # Graduate GitHub release from pre-release to stable
   gh release edit vX.Y.Z --prerelease=false --latest
   ```
