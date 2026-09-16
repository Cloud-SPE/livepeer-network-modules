#!/usr/bin/env python3
"""Seal a stopped-writer staging tree or verify an archive before manual restore.

This tool never stops/starts services, extracts archives, creates volumes or
contacts a chain. Physical fencing is an explicit operator responsibility.
"""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile

from generate import require

INDEX = "archive-manifest.json"


def bindings(manifest):
    topology = manifest["topology"]
    return {"deployment_id": topology["deployment_id"], "chain_id": topology["chain_id"],
            "payee": topology["payee"].lower(),
            "pools": {name: {"pool_id": r["pool_id"], "payout_wallet": r["payout_wallet"].lower()} for name, r in topology["regions"].items()},
            "sources": {name: {"pool": b["pool"], "source_id": b["source_id"], "receiver_wallet": b["receiver_wallet"].lower()} for name, b in topology["brokers"].items()},
            "volumes": sorted(item["volume"] for item in manifest["durable_volumes"])}


def validate_fence(fence, manifest):
    require(set(fence) == {"incident_id", "operator", "fenced_at", "fenced_hosts", "evidence"}, "explicit fencing record required")
    require(all(isinstance(fence[k], str) and fence[k].strip() for k in ("incident_id", "operator", "evidence")), "fencing record lacks evidence/operator")
    when = dt.datetime.fromisoformat(fence["fenced_at"].replace("Z", "+00:00"))
    require(when.tzinfo is not None and when <= dt.datetime.now(dt.timezone.utc), "invalid fencing timestamp")
    require(set(fence["fenced_hosts"]) == set(manifest["topology"]["hosts"]), "fencing record must cover every writer host")


def seal(root, expected):
    root = Path(root)
    manifest = json.loads(Path(expected).read_text())
    fence = json.loads((root / "fence.json").read_text())
    validate_fence(fence, manifest)
    require(not (root / INDEX).exists(), "staging tree already sealed")
    require((root / "deployment-manifest.json").is_file(), "missing deployment manifest")
    require(bindings(json.loads((root / "deployment-manifest.json").read_text())) == bindings(manifest), "staged deployment identity mismatch")
    require(manifest.get("material_files"), "deployment manifest lacks material inventory")
    for name in manifest["material_files"]:
        path = root / name if name.startswith("operator/") else root / "hosts" / name
        require(path.is_file(), "missing configuration or credential: " + name)
    for item in manifest["durable_volumes"]:
        require((root / "volumes" / item["volume"]).is_dir(), "missing durable volume: " + item["volume"])
    for host in manifest["topology"]["hosts"]:
        for folder in ("config", "secrets"):
            require((root / "hosts" / host / folder).is_dir(), "missing host material: " + host + "/" + folder)
        require((root / "hosts" / host / "compose.json").is_file(), "missing host compose: " + host)
    files = {}
    for path in sorted(root.rglob("*")):
        require(not path.is_symlink(), "symlinks are forbidden in a state snapshot")
        if path.is_file():
            with path.open("rb") as stream:
                files[path.relative_to(root).as_posix()] = hashlib.file_digest(stream, "sha256").hexdigest()
        else:
            require(path.is_dir(), "special files/sockets must not be archived")
    index = {"version": 1, "bindings": bindings(manifest), "files": files,
             "directories": sorted(p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_dir()),
             "fence": fence, "created_at": dt.datetime.now(dt.timezone.utc).isoformat()}
    with (root / INDEX).open("x") as stream:
        json.dump(index, stream, indent=2, sort_keys=True)
        stream.write("\n")
    (root / INDEX).chmod(0o600)
    return index


def safe_name(value):
    # GNU/BusyBox tar commonly prefixes paths with ./.
    while value.startswith("./"):
        value = value[2:]
    if value in ("", "."):
        return ""
    path = PurePosixPath(value)
    require(not path.is_absolute() and ".." not in path.parts and "\\" not in value,
            "unsafe archive path")
    return path.as_posix().rstrip("/")


def verify(archive, expected, expected_sha256):
    manifest = json.loads(Path(expected).read_text())
    with Path(archive).open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    require(digest == expected_sha256, "archive checksum differs from trusted backup record")
    with tarfile.open(archive, "r:*") as source:
        members = {}
        directories = set()
        for item in source:
            name = safe_name(item.name)
            require(item.isfile() or item.isdir(), "archive contains links or special files")
            if not name:
                require(item.isdir(), "invalid archive root")
                continue
            require(name not in members and name not in directories, "duplicate archive entry")
            if item.isdir():
                directories.add(name)
            else:
                members[name] = item
        require(INDEX in members and members[INDEX].size <= 32 * 1024 * 1024, "missing or oversized archive manifest")
        index = json.load(source.extractfile(members.pop(INDEX)))
        require(index["version"] == 1 and index["bindings"] == bindings(manifest), "pool/source/wallet/storage identity mismatch")
        validate_fence(index["fence"], manifest)
        require(set(index["files"]) == set(members) and set(index["directories"]) == directories, "missing or unexpected archive entries")
        for name, item in members.items():
            with source.extractfile(item) as stream:
                actual = hashlib.file_digest(stream, "sha256").hexdigest()
            require(actual == index["files"][name], "changed archived file: " + name)
    return index


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("seal", "verify"))
    parser.add_argument("path", type=Path)
    parser.add_argument("--deployment-manifest", required=True, type=Path)
    parser.add_argument("--sha256", help="trusted archive checksum, required for verify")
    args = parser.parse_args()
    if args.action == "seal":
        result = seal(args.path, args.deployment_manifest)
        print("Sealed stopped-writer staging tree; archive and store its checksum separately.")
    else:
        require(args.sha256, "--sha256 is required for archive verification")
        result = verify(args.path, args.deployment_manifest, args.sha256)
        print("Archive verified; manual restore still requires fresh old-writer fencing and chain reconciliation.")
