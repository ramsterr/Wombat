# Releasing wombat

Wombat publishes binaries as GitHub releases. Tags drive everything: pushing
`v<version>` builds the binaries, publishes the release, moves the rolling
`stable` tag, and refreshes the Homebrew tap when a tap token is configured.

## One-time setup

1. **Repository settings** — nothing is required for the release itself. The
   workflow uses the automatic `GITHUB_TOKEN`, which can create releases in
   this repository.
2. **Optional: Homebrew tap** — create an empty public repository named
   `homebrew-tap` under your account (`ramsterr/homebrew-tap`) with a `main`
   branch, then add a repository secret `HOMEBREW_TAP_TOKEN` (a fine-grained
   PAT with `contents: write` on that tap repository only). The release
   workflow then rewrites `Formula/wombat.rb` in the tap after every release.
   Without the secret the tap is simply not updated.

## Cutting a release

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
4. forces the `stable` tag to the released commit, so
   `go install github.com/ramsterr/Wombat/cmd/wombat@stable` resolves to it,
5. publishes `VERSION.txt` and `install.sh` on the rolling `stable` release
   so the install script always finds the current version,
6. updates the Homebrew formula when `HOMEBREW_TAP_TOKEN` is set.

## Verifying a release locally

```sh
goreleaser release --snapshot --clean --skip=publish   # builds all targets
goreleaser check                                       # validates .goreleaser.yaml
```

The asset naming is the contract shared by `internal/release`, `install.sh`,
and the Homebrew formula: `wombat_<version>_<os>_<arch>.<tar.gz|zip>` plus a
`checksums.txt` manifest in sha256 format.

## How users update

| install method | update command |
|---|---|
| install script / manual | `wombat upgrade` (replaces the binary in place) |
| Homebrew | `brew upgrade ramsterr/tap/wombat` |
| `go install` | `go install github.com/ramsterr/Wombat/cmd/wombat@latest` |

`wombat upgrade` detects the method automatically; override it with
`wombat upgrade --method script|go|brew`. `wombat upgrade --check` only
reports whether a newer version exists, and `wombat doctor` shows the same
status.
