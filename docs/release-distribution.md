# Tunnel and Relay Release Distribution

This repo stays private and remains the source of truth. Public downloads live in `yuanbohan/tunnel`, which is treated as a distribution surface only.

## Public Repo Access

`yuanbohan/tunnel` is public-read by default and private-write by default. Other GitHub users cannot push unless you add them as collaborators. The release workflow publishes through `TUNNEL_DIST_REPO_TOKEN`, so the workflow only needs the same repo write permission that you already granted to that token.

Recommended setup:

- Keep the public repo owned by your personal account or org.
- Do not add outside collaborators unless you want them to push.
- If you enable branch protection on `main`, make sure the release workflow still has a path to update `install.sh` and `latest.json`. A strict "pull requests only" rule on `main` will block this publish flow.

## Compatibility Contract

Tunnel and Relay share one compatibility contract:

- Same compatibility line means guaranteed compatibility.
- A compatibility-line change means compatibility is no longer guaranteed.
- For `v1+`, the compatibility line is the semver major version.
- For pre-`v1`, the compatibility line is `0.minor`, so `v0.1.x` and `v0.2.x` are different lines.

Examples:

- `tunnel v0.1.7` is compatible with `relay v0.1.3`
- `tunnel v1.2.0` is compatible with `relay v1.9.4`
- `tunnel v0.2.0` is not promised compatible with `relay v0.1.9`
- `tunnel v2.0.0` is not promised compatible with `relay v1.8.0`

The public `latest.json` manifest publishes this contract as `compatibility_line`.

Published `tunnel` binaries also embed their own release identity. Official release packaging sets both the requested version and an internal `official-release` distribution marker. Native self-update uses that embedded marker to decide whether the current binary is on the official release channel. Rollback target metadata is still stored locally in `~/.tunnel/updater.json`.

The private-repo `Release Tunnel` workflow enforces that the requested `tunnel` release version stays within the current repo relay compatibility line. It does not publish a `relay` binary; it prevents a `tunnel` release from crossing into a new line until the repo's shared build metadata is updated first.

Relay is distributed separately as a Docker image through GitHub Container Registry. The `Release Relay Image` workflow builds `cmd/relay` into `ghcr.io/yuanbohan/agent-tunnel-relay:<version>` from pushed semver tags.

## Token Setup

The private source repo needs one secret:

- `TUNNEL_DIST_REPO_TOKEN`: a fine-grained personal access token with Contents read/write access to `yuanbohan/tunnel`

The workflow uses that token to:

- push `install.sh` and `latest.json` to the public repo default branch
- create the public GitHub release and upload release assets

Native `tunnel update` and `tunnel rollback` download `checksums.txt` before verifying archive checksums.

## Release Flow

### Tunnel CLI

1. Open the private repo Actions tab.
2. Run `Release Tunnel`.
3. Enter a version such as `v0.1.2`.
4. The workflow runs `go test ./...`, release smoke tests, packages four release archives, and then publishes the public release.

Published outputs:

- one GitHub Release in `yuanbohan/tunnel`
- four `tunnel_<version>_<os>_<arch>.tar.gz` assets
- one `checksums.txt`
- refreshed `install.sh`
- refreshed `latest.json`

Those same public assets are the only source used by native `tunnel update` and `tunnel rollback`. The CLI does not shell out to `install.sh`; it consumes the published `latest.json`, release archives, and `checksums.txt` directly.

### Relay Image

1. Push a semver git tag such as `v0.1.2`.
2. The `Release Relay Image` workflow runs `go test ./...`.
3. The workflow builds `Dockerfile.relay` with release ldflags:
   - `Version=<tag>`
   - `DistributionMarker=official-release`
   - `GitCommit=<sha>`
   - `GitBranch=<tag>`
   - `BuildTime=<timestamp>`
4. The workflow runs `relay version` inside the image and requires the first line to report the tag.
5. The workflow pushes `ghcr.io/yuanbohan/agent-tunnel-relay:<tag>`.

Compose deployments pin `RELAY_IMAGE_TAG` to the desired semver tag. They should not track a mutable `latest` tag.

## Commit Messages

The public repo receives clear commit messages:

- `docs: bootstrap public tunnel distribution repo`
- `release: publish tunnel vX.Y.Z`

## Failed Release Recovery

The publish script refuses to reuse an existing release tag. If a run fails after creating a draft release, delete the draft release in `yuanbohan/tunnel` and rerun the workflow with the same version.

If the workflow fails before `latest.json` is updated, the previous default install target remains unchanged. That is the intended failure mode.
