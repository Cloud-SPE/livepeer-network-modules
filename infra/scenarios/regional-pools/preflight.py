#!/usr/bin/env python3
"""Validate generated files using pinned images with networking disabled.

No Compose up, financial commands, volume initialization or permission changes.
Run after provisioning file ownership as documented in README.md.
"""
import argparse
import json
from pathlib import Path
import subprocess


def run(args):
    subprocess.run(args, check=True)


def preflight(root):
    root = Path(root).resolve()
    manifest = json.loads((root / "manifest.json").read_text())
    for host in manifest["topology"]["hosts"]:
        folder = root / host
        run(["docker", "compose", "-f", str(folder / "compose.json"), "config", "--quiet"])
        compose = json.loads((folder / "compose.json").read_text())
        for name, service in compose["services"].items():
            if name.endswith("-controller"):
                command = ["validate-config", "--config=/etc/livepeer/config.json"]
            elif name.endswith(("-reconciler", "-executor")):
                command = ["validate-config", "--config=/etc/livepeer/config.json"]
            elif name == "member-portal":
                command = ["--validate-config", "--config=/etc/livepeer/config.json"]
            elif name in manifest["topology"]["brokers"]:
                command = ["config", "validate", "--config=/etc/livepeer/config.json"]
            elif name.endswith("-https"):
                command = ["-t"]
            else:
                # Receiver/observer flag correctness is covered by their component
                # tests. Starting either would resume durable chain operations.
                continue
            args = ["docker", "run", "--rm", "--network=none", "--read-only", "--user=65532:65532"]
            for mount in service.get("tmpfs", []):
                args += ["--tmpfs=" + mount]
            for volume in service["volumes"]:
                if not volume.startswith("./"):
                    continue
                source, target, mode = volume.split(":")
                args += ["--mount", "type=bind,src=" + str(folder / source) + ",dst=" + target + ",readonly"]
            if name.endswith("-https"):
                args += ["--entrypoint=nginx"]
            args += [service["image"]] + command
            print("Offline validation:", host, name, flush=True)
            run(args)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    preflight(parser.parse_args().output)
