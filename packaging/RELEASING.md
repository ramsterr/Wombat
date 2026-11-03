# Releasing wombat

Wombat is installed and updated from `main` with `update.sh`. That script builds
the Go binary and the opentui frontend, then installs both to `~/.local/bin` and
`~/.wombat`. Do not publish a release as the normal install path: release
archives contain only the Go binary, not the UI assets, so they open the removed
classic interface.

## Cutting an optional release

Tags still build binaries for people who want a versioned artifact:

```sh
# 1. make sure main is green and your tree is clean
git checkout main && git pull

# 2. update CHANGELOG.md: move [Unreleased] to a new version section

# 3. tag and push
git tag v0.3.0
git push origin v0.3.0
```

The `Release` workflow then:

1. builds binaries for `linux`, `darwin`, and `windows` on `amd64` and
   `arm64` with `CGO_ENABLED=0`,
2. stamps the version, commit, and build date into the binary,
3. publishes `wombat_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) plus
   `checksums.txt` as a GitHub release,
4. moves the rolling `stable` tag and updates `VERSION.txt`,
5. updates the Homebrew formula when `HOMEBREW_TAP_TOKEN` is set.

`install.sh` and `wombat upgrade` no longer download these assets. They build
current `main` so every install gets the same UI.

If a release workflow run fails halfway, delete the GitHub release for that
tag and re-push the tag; goreleaser refuses to overwrite an existing release.

## Verifying a release locally

```sh
goreleaser release --snapshot --clean --skip=publish   # builds all targets
goreleaser check                                       # validates .goreleaser.yaml
```

The asset naming is shared by `internal/release` and the Homebrew formula:
`wombat_<version>_<os>_<arch>.<tar.gz|zip>` plus a `checksums.txt` manifest in
sha256 format.

## How users install and update

```sh
curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/update.sh | sh
```

or, from an existing install:

```sh
wombat upgrade
```

`wombat doctor` shows the UI, Node, and renderer status. Release archives and
`go install` are not a supported way to run the new UI because they do not carry
the frontend bundle.
