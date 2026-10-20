#!/usr/bin/env python3
"""Update the wombat Homebrew formula in a tap after a release.

Used by .github/workflows/release.yml when a HOMEBREW_TAP_TOKEN secret is
configured. Reads checksums.txt from the published release and rewrites the
formula in the tap repository through the GitHub contents API.

    update-tap.py --tap ramsterr/homebrew-tap --version 0.2.0 --checksums checksums.txt
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import re
import sys
import urllib.error
import urllib.request

API = "https://api.github.com"
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


def api(method: str, path: str, token: str, payload: dict | None = None):
    url = f"{API}{path}"
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(url, data=data, method=method)
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("Accept", "application/vnd.github+json")
    request.add_header("User-Agent", "wombat-release-bot")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request) as response:
        body = response.read()
    return json.loads(body) if body else {}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tap", required=True, help="owner/tap repository")
    parser.add_argument("--version", required=True)
    parser.add_argument("--checksums", required=True)
    parser.add_argument("--message", default="")
    args = parser.parse_args()

    token = os.environ.get("HOMEBREW_TAP_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if not token:
        raise SystemExit("HOMEBREW_TAP_TOKEN is not set")

    sums = required_sums(parse_checksums(args.checksums), args.version)
    formula = TEMPLATE.format(version=args.version, **sums)

    owner, repo = args.tap.split("/", 1)
    path = f"/repos/{owner}/{repo}/contents/{FORMULA_PATH}"
    sha = None
    try:
        existing = api("GET", path, token)
        sha = existing.get("sha")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise

    payload = {
        "message": args.message or f"wombat {args.version}",
        "content": base64.b64encode(formula.encode()).decode(),
        "branch": "main",
    }
    if sha:
        payload["sha"] = sha
    api("PUT", path, token, payload)
    print(f"updated {args.tap}/{FORMULA_PATH} to {args.version}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
