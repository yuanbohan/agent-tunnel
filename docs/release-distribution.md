# Tunnel and Relay Release Distribution

This repo stays private and remains the source of truth. Public downloads live in `yuanbohan/tunnel`, which is treated as a distribution surface only.

## Public Repo Access

`yuanbohan/tunnel` is public-read by default and private-write by default. Other GitHub users cannot push unless you add them as collaborators. The release workflow publishes through `TUNNEL_DIST_REPO_TOKEN`, so the workflow only needs the same repo write permission that you already granted to that token.

Recommended setup:

- Keep the public repo owned by your personal account or org.
- Do not add outside collaborators unless you want them to push.
- If you enable branch protection on `main`, make sure the release workflow still has a path to update `install.sh` and `latest.json`. A strict "pull requests only" rule on `main` will block this publish flow.

Published `tunnel` binaries also embed their own release identity. Official release packaging sets both the requested version and an internal `official-release` distribution marker. Native self-update uses that embedded marker to decide whether the current binary is on the official release channel. Rollback target metadata is still stored locally in `~/.tunnel/updater.json`.

The private-repo `Release` workflow is manually dispatched and requires the maintainer to choose the product being released. For Tunnel, it packages the explicitly requested plain version and publishes that version in `latest.json`. It does not publish a `relay` binary.

Relay is distributed separately as Docker images through GitHub Container Registry. The same `Release` workflow builds `cmd/relay` once when the maintainer selects Relay, then publishes that build artifact as both `ghcr.io/yuanbohan/agent-tunnel-relay:<version>` and `ghcr.io/yuanbohan/agent-tunnel-stun:<version>`.

Source tags are product-prefixed to avoid ambiguity in the private repository:

- Tunnel source tag: `tunnel-vX.Y.Z`
- Relay source tag: `relay-vX.Y.Z`

Published product versions remain prefix-free:

- Tunnel public release, archives, installer, and `tunnel --version`: `vX.Y.Z`
- Relay/STUN GHCR image tags, labels, `/api/version`, and `relay version`: `vX.Y.Z`

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
2. Run `Release`.
3. Select `tunnel`.
4. Enter a plain version such as `v0.1.2`.
5. The workflow resolves source tag `tunnel-v0.1.2`, runs `go test ./...`, release smoke tests, packages four release archives, and verifies the packaged binary.
6. After Tunnel-specific validation succeeds, the workflow creates or validates source tag `tunnel-v0.1.2` and then publishes the public release as `v0.1.2`.

Published outputs:

- one GitHub Release in `yuanbohan/tunnel`
- four `tunnel_<version>_<os>_<arch>.tar.gz` assets
- one `checksums.txt`
- refreshed `install.sh`
- refreshed `latest.json`

The published installer and native `tunnel update` path may print non-blocking tmux readiness guidance after a successful Tunnel install/update. They must not invoke package managers or auto-install tmux.

Those same public assets are the only source used by native `tunnel update` and `tunnel rollback`. The CLI does not shell out to `install.sh`; it consumes the published `latest.json`, release archives, and `checksums.txt` directly.

### Relay Image

1. Open the private repo Actions tab.
2. Run `Release`.
3. Select `relay`.
4. Enter a plain version such as `v0.1.2`.
5. The workflow resolves source tag `relay-v0.1.2` and runs `go test ./...`.
6. The workflow builds `Dockerfile.relay` with release ldflags:
   - `Version=<plain version>`
   - `DistributionMarker=official-release`
   - `GitCommit=<sha>`
   - `GitBranch=<source tag>`
   - `BuildTime=<timestamp>`
7. The workflow runs `relay version` inside the Relay image and requires the first line to report the plain version and the `branch:` line to report the resolved source tag. It also verifies the STUN image tag reports the same version, exposes `relay stun serve`, keeps the Relay image default command as `relay serve`, and advertises both `8586/tcp` and `3478/udp`.
8. After Relay-specific validation succeeds, the workflow creates or validates source tag `relay-v0.1.2`.
9. The workflow pushes both `ghcr.io/yuanbohan/agent-tunnel-relay:<plain version>` and `ghcr.io/yuanbohan/agent-tunnel-stun:<plain version>`.

Compose deployments pin `RELAY_IMAGE_TAG` and `STUN_IMAGE_TAG` to desired semver tags from those service-specific image names. They should not track a mutable `latest` tag.

## Commit Messages

The public repo receives clear commit messages:

- `docs: bootstrap public tunnel distribution repo`
- `release: publish tunnel vX.Y.Z`

## Failed Release Recovery

The publish script refuses to reuse an existing release tag. If a run fails after creating a draft release, delete the draft release in `yuanbohan/tunnel` and rerun the workflow with the same version.

If the workflow fails before `latest.json` is updated, the previous default install target remains unchanged. That is the intended failure mode.

If the workflow fails before product-specific validation completes, no source tag is pushed.

For Tunnel or Relay, if a workflow rerun finds the resolved source tag already pointing at the same commit, it reuses that source tag. If the source tag points at another commit, the workflow fails before publishing.
