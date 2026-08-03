# Release Workflow

How code moves from a feature branch to a published GitHub Release + Docker
Hub image, and what each CI workflow in `.github/workflows/` does.

See also: [Architecture & Limitations](ARCHITECTURE.md) · [Development & Style Guide](DEVELOPMENT.md)

## 1. Branch model

```
feature/*, fix-*, add-*, bugfix-*, ...   (short-lived topic branches)
        │  PR
        ▼
      develop  ───────────────────────────────────►  rc/x.y.z ──► PR ──► main
        │                                                              ▲
        │ (urgent prod fix, skips develop)                             │
        └──────────────────────── hotfix/* ──────────────────────────┘
```

* **Topic branches** (any name, e.g. `add-tool-get-manual-launches`,
  `fix-tms-tool-update-test-case`) — day-to-day work, PR into `develop`.
* **`develop`** — integration branch. Every merge triggers a "dev" Docker
  image build (see §3.2).
* **`rc/*`** — release-candidate branches cut from `develop` to stabilize a
  release. Pushing to `rc/*` triggers an RC Docker image build (§3.3).
* **`hotfix/*`** — urgent fixes that need to reach production without waiting
  for the next normal release cycle; can be branched directly and merged to
  `main` (and should also be merged back into `develop`). Pushing to
  `hotfix/*` also triggers the RC-style image build.
* **`main`** — production branch. **PRs into `main` are only accepted from
  `develop` or `hotfix/*`** — this is enforced by CI, not just convention
  (§2).

### 1.1 Enforced PR-source-branch rule

`.github/workflows/validate-pr-branch.yml` runs on every PR targeting `main`
and **fails the check** (plus posts an explanatory PR comment) if the source
branch is neither `develop` nor `hotfix/*`. If you accidentally open a PR to
`main` from a random topic branch, close it and re-target/re-branch — don't
try to bypass the check.

## 2. What CI checks on every push/PR

`.github/workflows/build.yml` runs on push/PR to `main` and `develop`:

1. `golangci-lint` (pinned version — keep in sync with
   `Taskfile.yaml`'s `GOLANGCI_LINT_VERSION`; both currently `v2.11.4`).
2. `task test` (`go test ./...`).
3. `task app:build` (compiles the binary).

`.github/workflows/helm-lint.yaml` runs on push/PR to `main`, `develop`,
`rc/**`, `hotfix/**` **when `helm-charts/**` changes** — `helm lint` +
`helm template --debug` on the chart.

There is no separate "integration test" gate in CI today
(`task test:integration` is a manual/local step) — run it yourself before
merging changes that touch request/response shaping, auth, or pagination.

## 3. Docker image builds (continuous, not just at release time)

All three feed into `reportportal/reportportal-mcp-server` on the internal
ECR registry via a shared reusable workflow
(`reportportal/.github/.github/workflows/build-docker-image.yaml@main`, in a
separate org-level repo — not part of this codebase but shared org-wide across ReportPortal
repos).

| Workflow | Trigger | Tag(s) produced |
|---|---|---|
| `build-dev-image.yml` | push to `develop` (ignoring `.github/**`, `README.md`) | `develop-<run_number>`, `develop-latest` |
| `build-rc-image.yaml` | push to `rc/*` or `hotfix/*` | `<branch>-<run_number>` (slashes → dashes), `latest`; also parses a semver out of the branch name (e.g. `rc/1.4.0` → version `1.4.0`) via `release-mode: true` |
| `build-feature-image.yaml` | PR opened/synced/reopened targeting `develop`, or manual `workflow_dispatch` | `<head_ref>-<run_number>` (or the manually supplied `image-tag`) |

These are for **testing a branch's Docker image before an official release** —
they do not touch Docker Hub or create GitHub Releases.

## 4. Publishing an official release

Official releases are **manually triggered** — there is no automatic
"tag on merge to main" flow.

1. Make sure `main` (or the `rc/*`/`hotfix/*` branch about to be merged into
   it) is green: CI build passing, `task checks` clean locally, Helm lint
   passing if the chart changed.
2. Merge the `rc/*` (or `hotfix/*`) branch into `main` via PR (source-branch
   check in §1.1 applies).
3. From the GitHub Actions tab, manually run **`.github/workflows/release.yml`**
   ("Release") via `workflow_dispatch`, supplying the `version` input.
   - **Version format**: plain SemVer, **no `v` prefix** — check existing
     tags with `git tag --sort=-creatordate` (e.g. `1.3.3`, `1.3.2`, ...);
     match that convention.
4. The workflow then, in order:
   1. Checks out full history (`fetch-depth: 0`, required by GoReleaser for
      changelog generation).
   2. **Creates and pushes the Git tag** for `version` — fails fast if the tag
      already exists (no accidental re-release of the same version).
   3. Sets up Go, QEMU, and Docker Buildx.
   4. Logs into Docker Hub using `secrets.REGESTRY_USERNAME` /
      `secrets.REGESTRY_PASSWORD` (note: the secret names have a typo,
      "REGESTRY" — that's intentional/existing, don't silently rename without
      updating the secret in repo settings too).
   5. Runs **GoReleaser** (`.goreleaser.yaml`) via `goreleaser/goreleaser-action`,
      which:
      - Cross-compiles the binary for `linux/darwin/windows` × `amd64/arm64`
        (`CGO_ENABLED=0`), embedding `Version`/`Commit`/`Date` into
        `internal/config` via `-ldflags`.
      - Packages archives (`.zip` for Windows, default tarball otherwise)
        including `LICENSE` and `README.md`.
      - Publishes a **GitHub Release** with a GitHub-native changelog
        (`changelog.use: github-native`) and the built archives as assets.
   6. Builds and pushes a **multi-arch Docker image**
      (`linux/amd64,linux/arm64`) from `release.dockerfile` to Docker Hub as
      `reportportal/mcp-server:<version>` **and** `reportportal/mcp-server:latest`,
      with OCI/Artifact Hub labels (maintainers, source URL, revision, etc.)
      baked in.

### 4.1 What NOT to do

* Don't hand-craft a Git tag and push it yourself expecting a release to
  happen — tagging alone does nothing; the `release.yml` workflow is what
  drives both the tag creation *and* the build/publish steps together. If you
  push a tag out-of-band, the workflow's "tag already exists" guard will
  block a subsequent run for that version.
* Don't bypass the branch-source check to get a hotfix into `main` faster —
  use a `hotfix/*` branch name, which is explicitly allowed.
* Don't forget to merge `hotfix/*` fixes back into `develop` after they land
  on `main`, or the fix will be lost on the next regular `develop` → `rc/*` →
  `main` cycle.

## 5. Versioning & compatibility notes

* Version is plain SemVer (`MAJOR.MINOR.PATCH`, optionally with a pre-release
  suffix like the historical `1.0.0-beta1`), no leading `v`.
* **1.x** supports ReportPortal 25.1+ (service-api ≥ 5.14.0), no TMS tools.
* **2.x** (in development on `develop` at the time of writing) adds TMS tools
  and requires ReportPortal 26.1+ with TMS enabled. When cutting the first 2.x
  release, double check the README's compatibility section and the "For
  developers" prerequisites are updated to match (see
  [Architecture limitation #14](ARCHITECTURE.md#6-known-limitations--gotchas)
  re: keeping version-related docs in sync).
* The README's top banner explicitly tells readers to check the `main`
  branch's README for the "current" tool list, since `develop`'s README may
  describe in-progress/unreleased functionality — keep that banner and the
  version-gating notes in the tool tables (e.g. "Available from MCP server
  version 2.x") accurate when merging TMS-related or other version-gated
  features.

## 6. Post-release checklist

After a release publishes successfully:

1. Verify the GitHub Release page has the expected assets (binaries for all
   6 OS/arch combos + checksums) and changelog.
2. Verify `docker pull reportportal/mcp-server:<version>` and
   `:latest` both resolve to the new image (check the image's
   `org.opencontainers.image.revision` label matches the release commit SHA).
3. Smoke-test the new image (`task docker:run`, or point a real AI assistant
   config at it) using the [Verifying Your Setup](../README.md#verifying-your-setup)
   steps in the README.
4. If the Helm chart's `image.tag` default needs bumping for this release,
   update `helm-charts/reportportal-mcp-server/values.yaml` in a follow-up PR.
5. Merge `main` back into `develop` if the release branch had any last-minute
   fixes that only landed on `main`/`rc/*`/`hotfix/*`.
