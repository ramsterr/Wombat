#!/usr/bin/env python3
"""Update the wombat Homebrew formula in a tap after a release.

Used by .github/workflows/release.yml and .github/workflows/tap.yml. It reads
checksums.txt from the published release, renders the formula, and commits it
to the tap repository with git.

    update-tap.py --tap ramsterr/homebrew-wombat --version 0.2.3 --checksums checksums.txt

The token comes from HOMEBREW_TAP_TOKEN (a fine-grained PAT with contents:
write on the tap repository) or GITHUB_TOKEN.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile

FORMULA_PATH = "Formula/wombat.rb"

TEMPLATE = '''class Wombat < Formula
  desc "A lightweight coding agent for the terminal"
  homepage "https://github.com/ramsterr/Wombat"
  version "{version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/ramsterr/Wombat/releases/download/v{version}/wombat_{version}_darwin_arm64.tar.gz"
      sha256 "{darwin_arm64}"
    else
      url "https://github.com/ramsterr/Wombat/releases/download/v{version}/wombat_{version}_darwin_amd64.tar.gz"
      sha256 "{darwin_amd64}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/ramsterr/Wombat/releases/download/v{version}/wombat_{version}_linux_arm64.tar.gz"
      sha256 "{linux_arm64}"
    else
      url "https://github.com/ramsterr/Wombat/releases/download/v{version}/wombat_{version}_linux_amd64.tar.gz"
      sha256 "{linux_amd64}"
    end
  end

  def install
    bin.install "wombat"
  end

  test do
    assert_match "wombat {version}", shell_output("#{{bin}}/wombat version")
  end
end
'''


def run(args: list[str], cwd: str | None = None) -> str:
    result = subprocess.run(
        args,
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout


def basic_auth(token: str) -> str:
    import base64

    return base64.b64encode(f"x-access-token:{token}".encode()).decode()


def parse_checksums(path: str) -> dict[str, str]:
    sums: dict[str, str] = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            parts = line.split()
            if len(parts) < 2:
                continue
            sums[parts[-1].lstrip("*")] = parts[0]
    return sums


def required_sums(sums: dict[str, str], version: str) -> dict[str, str]:
    wanted = {}
    for goos in ("darwin", "linux"):
        for goarch in ("amd64", "arm64"):
            name = f"wombat_{version}_{goos}_{goarch}.tar.gz"
            if name not in sums:
                raise SystemExit(f"missing checksum for {name}")
            wanted[f"{goos}_{goarch}"] = sums[name]
    return wanted


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tap", required=True, help="owner/tap repository")
    parser.add_argument("--version", required=True)
    parser.add_argument("--checksums", required=True)
    parser.add_argument("--message", default="")
    args = parser.parse_args()

    token = (os.environ.get("HOMEBREW_TAP_TOKEN") or os.environ.get("GITHUB_TOKEN") or "").strip()
    if not token:
        raise SystemExit("HOMEBREW_TAP_TOKEN is not set in this repository")

    sums = required_sums(parse_checksums(args.checksums), args.version)
    formula = TEMPLATE.format(version=args.version, **sums)

    owner, repo = args.tap.split("/", 1)
    workdir = tempfile.mkdtemp(prefix="wombat-tap-")
    try:
        base = os.environ.get("TAP_REPO_URL", "").strip()
        # Pass the token as an Authorization header instead of embedding it in
        # the URL: a stray newline or an escaped character in the secret would
        # otherwise break authentication.
        auth = ["-c", f"http.extraHeader=Authorization: Basic {basic_auth(token)}"]
        if base:
            url = base
        else:
            url = f"https://github.com/{owner}/{repo}.git"
        print(f"checking access to {owner}/{repo}")
        try:
            run(["git", "-c", f"http.extraHeader=Authorization: Basic {basic_auth(token)}",
                 "ls-remote", "--heads", url])
        except subprocess.CalledProcessError as error:
            sys.stderr.write(error.stderr or "")
            raise SystemExit(
                f"cannot read {owner}/{repo} with HOMEBREW_TAP_TOKEN. The token needs "
                "read access to the tap repository; check that it exists, has not "
                "expired, and was pasted without extra characters"
            ) from error
        print(f"cloning {owner}/{repo}")
        run(["git", *auth, "clone", "--depth", "1", url, workdir])
        run(["git", "config", "user.name", "wombat-release-bot"], cwd=workdir)
        run(["git", "config", "user.email", "wombat@localhost"], cwd=workdir)

        target = os.path.join(workdir, FORMULA_PATH)
        os.makedirs(os.path.dirname(target), exist_ok=True)
        previous = ""
        if os.path.exists(target):
            with open(target, encoding="utf-8") as handle:
                previous = handle.read()
        if previous == formula:
            print(f"{owner}/{repo}/{FORMULA_PATH} is already at {args.version}")
            return 0
        with open(target, "w", encoding="utf-8") as handle:
            handle.write(formula)

        run(["git", *auth, "add", FORMULA_PATH], cwd=workdir)
        run(
            ["git", "commit", "-m", args.message or f"wombat {args.version}"],
            cwd=workdir,
        )
        try:
            run(["git", *auth, "push", "origin", "HEAD"], cwd=workdir)
        except subprocess.CalledProcessError as error:
            sys.stderr.write(error.stderr or "")
            raise SystemExit(
                f"cannot push to {owner}/{repo}. The token needs write access "
                "(fine-grained PAT: Contents -> Read and write on the tap repository)"
            ) from error
        print(f"updated {owner}/{repo}/{FORMULA_PATH} to {args.version}")
    except subprocess.CalledProcessError as error:
        sys.stderr.write(error.stdout or "")
        sys.stderr.write(error.stderr or "")
        raise SystemExit(f"git failed: {error}") from error
    finally:
        shutil.rmtree(workdir, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
