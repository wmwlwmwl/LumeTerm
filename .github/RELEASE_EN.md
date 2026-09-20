# Desktop release

[简体中文](RELEASE.md)

Push a `v*` tag and Actions ([release.yml](workflows/release.yml)) will: generate notes → build all platforms → publish when assets are complete.

## Before release

1. `main` is clean; features to ship are merged
2. Version matches in four places (`x.y.z` or `x.y.z.w`):
   - `frontend/package.json` → `version`
   - `frontend/package-lock.json` → root and `packages[""].version` (after editing `package.json`, run `npm install --package-lock-only` in `frontend`)
   - `frontend/src/config.ts` → `APP_VERSION`
   - `wails.json` → `info.productVersion`
   
   > CI package version comes from `wails.json` / `APP_VERSION`. The lock root version is not baked into installers, but keep it aligned with `package.json` to avoid dirty diffs / awkward `npm ci` later.  
   > `npm version` accepts **strict semver only** (`x.y.z`). For four-part versions like `1.2.2.1`, edit by hand + `--package-lock-only`; do not use `npm version`.
3. Write clear, user-facing commit subjects (notes are auto-built from previous `v*` tag → current tag; merges and “release v…” commits are skipped)

## Release

```bash
# 1. Commit version bumps and code
git add -A
git commit -m "feat: 发行 v1.2.3"

# 2. Tag and push (triggers packaging)
git tag v1.2.3
git push origin main
git push origin v1.2.3
```

Or tell Claude: **发行 1.2.3** (same bump / commit / tag / push flow).

## After release

1. Actions: https://github.com/wmwlwmwl/LumeTerm/actions  
2. Release page: title `LumeTerm v…`, changelog, 14 assets (with `.sha256`)  
3. Missing assets fail the job and **stay draft** — incomplete sets never become latest

## Re-run manually

Actions → Build and Release → Run workflow → enter an existing tag (e.g. `v1.2.3`).

Note: `create-draft` sets that tag’s Release back to **draft** while re-uploading. Avoid re-running a release already public unless a short unpublish is acceptable.

## Where notes come from

| Section | Source |
|---------|--------|
| Changelog | Commit subjects from previous `v*` tag → current tag |
| Downloads / install-uninstall | [release-notes-footer.md](release-notes-footer.md) (`__VERSION__` substituted) |

Edit the footer for fixed copy only; do not paste bullets into the workflow.

## Don’t

- Tag before the code is pushed  
- Mismatch the four version fields  
- Force a half-failed draft to latest  
- Bump unrelated deps only to silence warnings  
