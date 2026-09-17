#!/usr/bin/env python3
"""Run Compose and five module parsers offline. Never mount financial databases."""
import argparse
import json
from pathlib import Path
import subprocess


def check(root):
    root = root.resolve()
    subprocess.run(["docker", "compose", "-f", str(root / "compose.yaml"), "--profile", "*",
                    "config", "--quiet"], check=True)
    compose = json.loads((root / "compose.yaml").read_text())
    evidence = {}
    for name, service in compose["services"].items():
        if name in {"us-controller", "us-reconciler", "us-executor"}:
            command = ["validate-config", "--config=/etc/livepeer/config.json"]
        elif name == "us-transcode-broker":
            command = ["config", "validate", "--config=/etc/livepeer/config.json"]
        elif name == "member-portal":
            command = ["--validate-config", "--config=/etc/livepeer/config.json"]
        else:
            continue
        # Freeze the local image for this check even if its tag moves meanwhile.
        image = json.loads(subprocess.check_output(["docker", "image", "inspect", service["image"]]))[0]
        args = ["docker", "run", "--rm", "--pull=never", "--network=none", "--read-only", "--user=65532:65532"]
        for mount in service["volumes"]:
            if mount.startswith("./"):
                source, target, mode = mount.split(":")
                args += ["--mount", f"type=bind,src={root / source},dst={target},readonly"]
        print(f"Checking {name}", flush=True)
        subprocess.run(args + [image["Id"]] + command, check=True)
        evidence[name] = {"image_id": image["Id"], "repo_digests": image.get("RepoDigests", []),
                          "result": "offline parser passed"}
    print(json.dumps(evidence, indent=2))
    print("Offline checks passed. Receiver/observer/ownership runtime, decrypted wallets, ingress, "
          "source completeness, hardware, chain and payouts are not validated by these checks.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("deployment", type=Path)
    check(parser.parse_args().deployment)
