#!/usr/bin/env python3
"""Throwaway local acceptance for generation and actual offline image parsers.

Requires freshly built regional-local/*:validation images. Synthetic receiver
keystores are intentionally unusable. No services, ports or chain calls start.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

from generate import Compiler
from preflight import preflight
from test_generate import fixture


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def image(tag):
    return subprocess.check_output(["docker", "image", "inspect", tag, "--format", "{{.Id}}"], text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, help="new evidence directory; defaults to temporary directory")
    args = parser.parse_args()
    root = args.output or Path(tempfile.mkdtemp(prefix="regional-deployment-validation."))
    root.mkdir(exist_ok=args.output is None)
    root = root.resolve()
    spec = fixture(root / "material")
    security = subprocess.check_output(["docker", "info", "--format", "{{json .SecurityOptions}}"], text=True)
    owner = "0:0" if "rootless" in security else str(os.getuid()) + ":" + str(os.getgid())
    mapping = {"controller": "pool-controller", "broker": "capability-broker", "receiver": "payment-daemon", "observer": "protocol-daemon", "reconciler": "pool-reconciler", "executor": "pool-payout-executor", "portal": "member-portal", "agent": "pool-member-agent"}
    spec["images"] = {key: image("regional-local/livepeer-" + name + ":validation") for key, name in mapping.items()}
    spec["images"]["proxy"] = image("nginx:1.28-alpine")
    spec["images"]["volume-init"] = image("busybox:1.37")
    repo = Path(__file__).resolve().parents[3]
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
    digest = hashlib.sha256()
    tracked = subprocess.check_output(["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=repo).split(b"\0")
    for raw in sorted(set(tracked)):
        if not raw or raw.startswith(b".beads/"):
            continue
        path = repo / os.fsdecode(raw)
        if path.is_file():
            digest.update(raw + b"\0" + path.read_bytes() + b"\0")
    spec["provenance"] = {"git_commit": commit, "tree_sha256": digest.hexdigest(), "validation_evidence": str(root), "local_only": True}
    # Initialize real durable controller IDs offline, never from labels/domains.
    for region in ("eu", "us"):
        folder = root / (region + "-identity")
        folder.mkdir()
        cmd = ["docker", "run", "--rm", "--network=none", "--user=" + owner, "-v", str(folder) + ":/data", spec["images"]["controller"], "init-identity", "--data-dir=/data"]
        result = json.loads(subprocess.check_output(cmd, text=True))
        spec["regions"][region]["pool_id"] = result["pool_id"]
        run(cmd + ["--expect-pool-id=" + result["pool_id"]])
    material = root / "material"
    issuer = material / "issuer.json"
    trust = material / "trust.json"
    issuer.unlink()
    trust.unlink()
    run(["docker", "run", "--rm", "--network=none", "--user=" + owner, "-v", str(material) + ":/material", spec["images"]["portal"], "--generate-key=/material/issuer.json", "--key-id=local-validation", "--issuer=" + spec["portal"]["url"], "--trust-output=/material/trust.json"])
    domains = [spec["portal"]["url"], spec["ownership"]["url"]]
    for region in spec["regions"].values():
        domains += [region["member_url"], region["admin_url"]]
    for broker in spec["brokers"].values():
        domains += [broker["public_url"], broker["admin_url"]]
    # Test CA/cert covers synthetic .invalid origins; no ACME/DNS automation.
    run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-subj", "/CN=regional-local-test", "-addext", "subjectAltName=" + ",".join("DNS:" + d.removeprefix("https://") for d in domains), "-keyout", str(material / "tls.key"), "-out", str(material / "ca.crt")], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    (material / "tls.key").chmod(0o600)
    (root / "spec.json").write_text(json.dumps(spec, indent=2) + "\n")
    output = Compiler(spec, root / "generated").compile()
    # Exercise actual runtime UID permissions on throwaway files, then return
    # ownership for evidence inspection. No named volumes or live files touched.
    paths = []
    for host in spec["hosts"]:
        paths += ["/fixture/" + host + "/config", "/fixture/" + host + "/secrets"]
    helper = ["docker", "run", "--rm", "--network=none", "--user=0", "-v", str(output) + ":/fixture", spec["images"]["volume-init"], "chown", "-R"]
    try:
        run(helper + ["65532:65532"] + paths)
        preflight(output)
    finally:
        run(helper + [owner] + paths)
    (root / "result.json").write_text(json.dumps({"status": "passed", "scope": "local offline configuration parsers and Compose; no deployment, chain calls or usable financial keys", "images": spec["images"], "provenance": spec["provenance"]}, indent=2) + "\n")
    print("Local validation evidence:", root)


if __name__ == "__main__":
    main()
