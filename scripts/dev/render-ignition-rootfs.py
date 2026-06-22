#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
#
# Render the files declared in an Ignition config into a directory tree, so the
# files RudderVirt OS injects via server.bu can be overlaid into the Layer-2 test
# container (`make test-container`). This reads the *generated* Ignition — the same
# artifact embedded in the ISO — so the test environment never drifts from what
# actually ships.
#
# Inline content (server.bu `inline:`, `local:` files, and `trees:`) is written
# verbatim. Remote content (`source: https://...`, e.g. the k3s binary or the
# kubevirt manifests) is skipped by default because it is large and network-bound;
# set FETCH_REMOTE=1 to download it too.
#
# Usage: render-ignition-rootfs.py <ignition.json> <output-dir>

import base64
import json
import os
import sys
import urllib.parse
import urllib.request
from pathlib import Path


def decode_data_url(url: str) -> bytes:
    # data:[<mediatype>][;base64],<data>
    header, _, data = url.partition(",")
    if ";base64" in header:
        return base64.b64decode(data)
    return urllib.parse.unquote_to_bytes(data)


def main() -> int:
    if len(sys.argv) != 3:
        print(f"usage: {sys.argv[0]} <ignition.json> <output-dir>", file=sys.stderr)
        return 2

    ign = json.loads(Path(sys.argv[1]).read_text())
    out_dir = Path(sys.argv[2])
    fetch_remote = os.environ.get("FETCH_REMOTE") == "1"

    storage = ign.get("storage", {})

    for d in storage.get("directories", []):
        (out_dir / d["path"].lstrip("/")).mkdir(parents=True, exist_ok=True)

    written, skipped = 0, []
    for f in storage.get("files", []):
        source = (f.get("contents") or {}).get("source")
        if not source:
            continue
        if source.startswith("data:"):
            data = decode_data_url(source)
        elif source.startswith(("http://", "https://")):
            if not fetch_remote:
                skipped.append(f["path"])
                continue
            with urllib.request.urlopen(source) as r:  # trusted release URLs from server.bu
                data = r.read()
        else:
            skipped.append(f["path"])
            continue
        dest = out_dir / f["path"].lstrip("/")
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(data)
        mode = f.get("mode")
        if mode is not None:
            os.chmod(dest, mode)  # Ignition stores mode as the decimal of the octal perms
        written += 1

    for link in storage.get("links", []):
        dest = out_dir / link["path"].lstrip("/")
        dest.parent.mkdir(parents=True, exist_ok=True)
        if dest.exists() or dest.is_symlink():
            dest.unlink()
        os.symlink(link["target"], dest)

    print(f"[render] wrote {written} file(s) into {out_dir}")
    if skipped:
        print(
            f"[render] skipped {len(skipped)} remote file(s) "
            f"(set FETCH_REMOTE=1 to include): " + ", ".join(sorted(skipped)),
            file=sys.stderr,
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
