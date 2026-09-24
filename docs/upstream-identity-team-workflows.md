# Upstream Workflow Changes for `canonical/identity-team`

## Summary & Problem Statement

Currently across Canonical Identity Platform services, `_rock-gh-publish.yaml` pushes released rock images directly to `ghcr.io/<repo>:stable` and `ghcr.io/<repo>:<ref_name>` as soon as the release tag is created. The vulnerability scanning workflow (`_rock-scan.yaml`) runs **after** publication, and does not configure `exit-code: '1'`.

This leads to:
1. **Public Vulnerability Window**: Images with un-triaged CVEs become publicly reachable at `stable` and `vX.Y.Z` before scans finish.
2. **Missing CI Enforcement**: If `_rock-scan.yaml` detects a critical CVE, the workflow finishes with green/neutral status instead of failing and blocking release.

To eliminate this vulnerability window, the release workflow implements a **quarantine-before-release gate**:
- The release tag publishes only a candidate tag: `<ref_name>-candidate`.
- The candidate tag is scanned with strict error gating (`exit-code: '1'`, `severity: 'HIGH,CRITICAL'`).
- Only once scans pass is the candidate tag promoted to `<ref_name>` and `stable` via registry-side `skopeo copy`, and the GitHub Release transitioned from pre-release to stable.

This document describes the exact changes proposed for `canonical/identity-team`'s reusable workflows to natively support this pattern across all identity services.

---

## 1. Changes to `_rock-gh-publish.yaml`

### Proposed Workflow Inputs
```yaml
inputs:
  rock:
    type: string
    required: false
    default: ""
    description: "Specific rock artifact to download. If empty, all rocks in the run will be downloaded and published."
  candidate:
    type: boolean
    required: false
    default: false
    description: "When true on a tag push, publish as <ref_name>-candidate instead of stable/<ref_name>."
```

### Proposed Logic in Step `Upload ROCK to ghcr.io`
```bash
if [ "${{ github.ref_type }}" = "branch" ]; then
  versions=(latest "${{ github.sha }}")
  image_tag="${{ github.sha }}"
elif [ "${{ inputs.candidate }}" = "true" ]; then
  versions=("${{ github.ref_name }}-candidate" "${{ github.sha }}")
  image_tag="${{ github.ref_name }}-candidate"
else
  versions=(stable "${{ github.ref_name }}")
  image_tag="${{ github.ref_name }}"
fi
```

---

## 2. Changes to `_rock-scan.yaml`

### Proposed Workflow Inputs
```yaml
inputs:
  image:
    type: string
    required: true
    description: "Container image reference to scan."
  exit-code:
    type: string
    required: false
    default: "0"
    description: "Exit code when vulnerabilities are found ('1' to fail on CVEs, '0' to report only)."
  severity:
    type: string
    required: false
    default: "HIGH,CRITICAL"
    description: "Severities of CVEs to evaluate."
  ignore-unfixed:
    type: boolean
    required: false
    default: true
    description: "Ignore unpatched CVEs without available upstream fixes."
  trivyignores:
    type: string
    required: false
    default: ".trivyignore"
    description: "Path to .trivyignore file."
```

### Proposed Step in `scan` Job
```yaml
      - name: Scan image with Trivy
        uses: aquasecurity/trivy-action@0.36.0
        with:
          image-ref: ${{ inputs.image }}
          format: 'sarif'
          output: 'trivy-results.sarif'
          severity: ${{ inputs.severity }}
          exit-code: ${{ inputs.exit-code }}
          ignore-unfixed: ${{ inputs.ignore-unfixed }}
          trivyignores: ${{ inputs.trivyignores }}
```

---

## 3. New Reusable Workflow: `_rock-promote.yaml`

Creates `canonical/identity-team/.github/workflows/_rock-promote.yaml`:

```yaml
name: image promotion
run-name: Promote candidate image ${{ inputs.candidate-image }} to stable

on:
  workflow_call:
    inputs:
      candidate-image:
        type: string
        required: true
        description: "Candidate container image URL (e.g. ghcr.io/org/repo:v1.0.0-candidate)."
      release-tag:
        type: string
        required: true
        description: "Release tag name (e.g. v1.0.0)."
      promote-stable:
        type: boolean
        required: false
        default: true
        description: "Whether to also tag image as stable."

jobs:
  promote:
    permissions:
      packages: write
      contents: write
    runs-on: ubuntu-latest
    steps:
      - name: Install Rockcraft for Skopeo
        run: sudo snap install --classic --channel latest/stable rockcraft

      - name: Log in to GHCR
        uses: docker/login-action@650006c6eb7dba73a995cc03b0b2d7f5ca915bee # v4.2.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Promote image tags via Skopeo
        run: |
          source_image="docker://${{ inputs.candidate-image }}"
          target_tags=("${{ inputs.release-tag }}")
          if [ "${{ inputs.promote-stable }}" = "true" ]; then
            target_tags+=("stable")
          fi

          for tag in "${target_tags[@]}"; do
            echo "Promoting to ghcr.io/${{ github.repository }}:${tag}..."
            sudo rockcraft.skopeo --insecure-policy copy \
              --src-creds "${{ github.actor }}:${{ secrets.GITHUB_TOKEN }}" \
              --dest-creds "${{ github.actor }}:${{ secrets.GITHUB_TOKEN }}" \
              "$source_image" \
              "docker://ghcr.io/${{ github.repository }}:${tag}"
          done

      - name: Graduate GitHub Release from pre-release to stable
        run: |
          gh release edit "${{ inputs.release-tag }}" --prerelease=false --latest
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

---

## Migration Plan

1. **Step 1 (Upstream PR)**: PR submitted to `canonical/identity-team`: [PR #150](https://github.com/canonical/identity-team/pull/150) on branch `feat/rock-candidate-publish-promote` (merged in release `v1.17.0`).
2. **Step 2 (Demonstration & Verification)**: `hook-service` consumes the reusable workflows directly from `canonical/identity-team` pinned to release `v1.17.0` in `.github/workflows/ci.yaml` and `.github/workflows/cves.yaml`.
3. **Step 3 (Adoption)**: Reusable workflows in `canonical/identity-team` are now pinned to `v1.17.0` across `hook-service` and ready for adoption across sibling services (`secure-token-service`, `identity-platform-login-ui`, etc.).
