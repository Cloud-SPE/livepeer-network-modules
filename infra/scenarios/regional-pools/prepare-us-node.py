#!/usr/bin/env python3
"""Prepare one US transcode node offline from existing keys and credentials.

Never starts services, creates volumes, submits transactions, or overwrites output.
The full-topology compiler remains authoritative for the eventual EU1/US3 layout.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
from urllib.parse import urlsplit


B = "us-transcode-broker"
C = "us-controller"
O = "gpu-ownership"
R = "us-reconciler"
E = "us-executor"
TEMPLATES = ["video-transcode-abr", "video-transcode-vod", "video-transcode-live"]
LINKS = [
    ("operator", C, "pool-admin", "controller"),
    ("operator", B, "pool-admin", B),
    ("operator", O, "ownership-admin", "ownership"),
    ("coordinator", B, "coordinator", B),
    (B, C, "broker", "controller"),
    (B, O, "ownership-reader", "ownership"),
    (C, B, "controller", B),
    (C, B, "revenue-reader", B),
    (C, O, "ownership-controller", "ownership"),
    (R, B, "revenue-reader", B),
    (R, C, "reconciler", "controller"),
    (E, C, "payout-executor", "controller"),
]
IMAGES = {
    C: "pool-controller", O: "pool-controller", B: "capability-broker",
    B + "-receiver": "payment-daemon", "us-observer": "protocol-daemon",
    R: "pool-reconciler", E: "pool-payout-executor", "member-portal": "member-portal",
}


def require(ok, message):
    if not ok:
        raise ValueError(message)


def read(path):
    require(path.is_file() and not path.is_symlink(), f"Missing or unsafe input: {path}")
    require(path.stat().st_size <= 4 * 1024 * 1024, f"Oversized input: {path}")
    return path.read_bytes()


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not isinstance(value, bytes):
        value = (value if isinstance(value, str) else json.dumps(value, indent=2) + "\n").encode()
    with path.open("xb") as stream:
        stream.write(value)
    path.chmod(0o600)


def token_name(caller, target, role):
    return f"{caller}-to-{target}-{role}.token"


def validate(base, spec, rpc_urls):
    expected = {"deployment_id", "pool_id", "payee", "receiver_wallet", "payout_wallet",
                "broker_url", "broker_admin_url", "member_url", "admin_url", "portal_url"}
    require(set(spec) == expected, "Unknown or missing node specification fields")
    require(re.fullmatch(r"[a-z][a-z0-9-]{0,62}", spec["deployment_id"]), "Invalid deployment ID")
    require(re.fullmatch(r"pool_[0-9a-f]{32}", spec["pool_id"]), "Invalid pool ID")
    addresses = [spec[k].lower() for k in ("payee", "receiver_wallet", "payout_wallet")]
    require(len(set(addresses)) == 3 and all(re.fullmatch(r"0x[0-9a-f]{40}", a)
            and int(a, 16) for a in addresses), "Wallet addresses must be valid and distinct")
    urls = [spec[k] for k in expected if k.endswith("_url")]
    require(len(set(urls)) == len(urls), "Service origins must be distinct")
    for value in urls:
        u = urlsplit(value)
        require(u.scheme == "https" and u.hostname and u.netloc == u.hostname
                and not u.path and not u.query and not u.fragment, "Require canonical HTTPS origins")
    require(bool(rpc_urls), "At least one HTTPS RPC URL is required")
    for value in rpc_urls:
        u = urlsplit(value)
        require(u.scheme == "https" and u.hostname and not u.username and not u.fragment,
                "RPC endpoints must use HTTPS without userinfo")
    secret = base / "secrets"
    for role in ("receiver", "payout"):
        wallet = json.loads(read(secret / f"{role}-keystore.json"))
        require(wallet.get("version") == 3 and ("crypto" in wallet or "Crypto" in wallet), "Require V3 keystore")
        require("0x" + wallet["address"].lower().removeprefix("0x") == spec[role + "_wallet"].lower(),
                f"{role} keystore address does not match")
        require(bool(read(secret / f"{role}-password").strip()), f"Empty {role} password")
    for name in ("broker-settlement.key", "broker-sealing.key"):
        require(re.fullmatch(rb"[0-9a-fA-F]{64}", read(secret / name).strip()), f"Invalid {name}")
    issuer = json.loads(read(secret / "portal-issuer.json"))
    require(issuer.get("issuer") == spec["portal_url"], "Portal issuer origin does not match")
    read(secret / "member-trust.json")
    source = read(base / "config/receiver-domain-id").decode().strip()
    require(re.fullmatch(r"0x[0-9a-f]{64}", source) and int(source, 16), "Invalid receiver domain")
    creds = secret / "service-credentials"
    metadata = json.loads(read(creds / "metadata.json"))
    require(metadata["pool_id"] == spec["pool_id"] and metadata["source_id"] == source,
            "Credential metadata differs from node identity")
    now = dt.datetime.now(dt.timezone.utc)
    expected_ids = {}
    for caller, target, role, resource in LINKS:
        filename = token_name(caller, target, role)
        token = read(creds / caller / filename).decode().strip()
        require(re.fullmatch(r"[0-9a-f]{64}", token), "Invalid service token")
        entries = json.loads(read(creds / target / "service-auth.json"))["credentials"]
        selected = [x for x in entries if x["id"] == filename[:-6]]
        require(len(selected) == 1, "Missing or duplicate credential verifier")
        entry = selected[0]
        expiry = dt.datetime.fromisoformat(entry["expires_at"].replace("Z", "+00:00"))
        require(entry["pool_id"] == spec["pool_id"] and entry["roles"] == [role]
                and entry["resources"] == [resource] and not entry.get("revoked")
                and entry["token_sha256"] == hashlib.sha256(token.encode()).hexdigest()
                and expiry > now, "Credential scope, hash or expiry mismatch")
        require(entry.get("source_id", "") == (source if caller == B and target == C else ""),
                "Credential source binding mismatch")
        expected_ids.setdefault(target, set()).add(entry["id"])
    for target, ids in expected_ids.items():
        entries = json.loads(read(creds / target / "service-auth.json"))["credentials"]
        require(len(entries) == len(ids) and {e["id"] for e in entries} == ids,
                "Unexpected verifier credentials; inspect before staging")
    return source


def prepare(base, spec, rpc_urls, output, agent_image, registry="tztcloud", tag="v2.0.0"):
    os.umask(0o077)
    source_id = validate(base, spec, rpc_urls)
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}", agent_image),
            "Agent enrollment requires a repository@sha256 digest")
    require(not output.exists(), "Output exists; refusing to overwrite deployment files")
    require(shutil.which("openssl"), "openssl is required")
    output.mkdir(mode=0o700)
    secret = base / "secrets"
    creds = secret / "service-credentials"
    pid = spec["pool_id"]
    services, volumes = {}, {}
    compose = {"name": spec["deployment_id"] + "-us", "services": services, "volumes": volumes,
               "networks": {"default": {"name": spec["deployment_id"] + "-us"}}}

    def copy(name, dest, path):
        write(output / "secrets" / name / dest, read(path))

    def add(name, command, profile=None):
        volume = spec["deployment_id"] + "-" + name + "-data"
        volumes[volume] = {"external": True, "name": volume}
        services[name] = {"image": f"{registry}/livepeer-{IMAGES[name]}:{tag}",
            "restart": "unless-stopped", "user": "65532:65532", "read_only": True,
            "security_opt": ["no-new-privileges:true"], "cap_drop": ["ALL"],
            "tmpfs": ["/tmp:uid=65532,gid=65532,mode=0700"], "command": command,
            "volumes": [f"{volume}:/data", f"./secrets/{name}:/secrets:ro"]}
        (output / "secrets" / name).mkdir(parents=True, mode=0o700)
        if profile:
            services[name]["profiles"] = [profile]

    def config(name, data):
        write(output / "config" / (name + ".json"), data)
        services[name]["volumes"].append(f"./config/{name}.json:/etc/livepeer/config.json:ro")

    def client(caller, target, role, url, scoped=False):
        result = {"pool_id": pid, "token_file": "/secrets/" + token_name(caller, target, role)}
        result.update({"method": "scoped"} if scoped else {"url": url})
        if target == O:
            result["ca_file"] = "/secrets/ownership-ca.crt"
        return result

    def socket(name, owners):
        volume = spec["deployment_id"] + "-" + name + "-socket"
        volumes[volume] = {"external": True, "name": volume}
        for owner in owners:
            services[owner]["volumes"].append(volume + ":/run/livepeer")

    add(O, ["--listen=:8443", "--data-dir=/data", "--service-auth-file=/secrets/service-auth.json",
            "--tls-cert=/secrets/tls.crt", "--tls-key=/secrets/tls.key"])
    services[O]["entrypoint"] = ["/usr/local/bin/livepeer-pool-ownership"]
    add(C, ["serve", "--config=/etc/livepeer/config.json", "--data-dir=/data"])
    add("us-observer", ["--mode=read-only", "--chain-rpc-urls=" + ",".join(rpc_urls),
                        "--socket=/run/livepeer/protocol.sock", "--store-path=/data/observer.db"])
    add(R, ["watch-rounds", "--config=/etc/livepeer/config.json"], "accounting")
    add(E, ["reconcile-loop", "--config=/etc/livepeer/config.json"], "payouts")
    receiver = B + "-receiver"
    add(receiver, ["--mode=receiver", "--socket=/run/livepeer/payment.sock", "--db=/data/receiver.db",
        "--txintent-db=/data/txintents.db", "--settlement-domain-id=" + source_id,
        "--chain-rpc-urls=" + ",".join(rpc_urls), "--keystore-path=/secrets/receiver-keystore.json",
        "--keystore-password-file=/secrets/receiver-password", "--orch-address=" + spec["payee"],
        "--metrics-listen=:9090"], "traffic")
    add(B, ["--config=/etc/livepeer/config.json"], "traffic")
    add("member-portal", ["--config=/etc/livepeer/config.json"], "members")
    socket("us-observer", ["us-observer", R])
    socket(receiver, [receiver, B])

    for caller, target, role, _ in LINKS:
        filename = token_name(caller, target, role)
        if caller in services:
            copy(caller, filename, creds / caller / filename)
        else:
            write(output / "operator" / filename, read(creds / caller / filename))
    for target in (C, B, O):
        copy(target, "service-auth.json", creds / target / "service-auth.json")
    for role, service in (("receiver", receiver), ("payout", E)):
        for suffix in ("-keystore.json", "-password"):
            copy(service, role + suffix, secret / (role + suffix))
    for filename in ("broker-settlement.key", "broker-sealing.key"):
        copy(B, filename, secret / filename)
    copy("member-portal", "portal-issuer.json", secret / "portal-issuer.json")
    copy(C, "member-trust.json", secret / "member-trust.json")

    # Direct HTTPS on the Compose network: no proxy, public certificate or cold key needed.
    owner_dir = output / "secrets" / O
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:3072", "-nodes", "-sha256",
        "-days", "365", "-subj", "/CN=gpu-ownership", "-addext", "subjectAltName=DNS:gpu-ownership",
        "-addext", "basicConstraints=critical,CA:TRUE", "-keyout", str(owner_dir / "tls.key"),
        "-out", str(owner_dir / "tls.crt")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    for filename in ("tls.key", "tls.crt"):
        (owner_dir / filename).chmod(0o600)
    for caller in (B, C):
        copy(caller, "ownership-ca.crt", owner_dir / "tls.crt")
    ownership_url = "https://gpu-ownership:8443"
    controller_auth = client(B, C, "broker", spec["admin_url"], True)
    config(B, {"pool_id": pid, "service_resource": B, "service_auth_file": "/secrets/service-auth.json",
        "ownership": client(B, O, "ownership-reader", ownership_url),
        "identity": {"orch_eth_address": spec["payee"], "label": B,
                     "settlement_key_file": "/secrets/broker-settlement.key"},
        "external_base_url": spec["broker_url"], "listen": {"paid": ":8080", "metrics": ":9090"},
        "payment_daemon": {"socket": "/run/livepeer/payment.sock"},
        "accounting_store_path": "/data/work-accounting.db",
        "credential_store": {"path": "/data/credentials.db", "sealing_key_file": "/secrets/broker-sealing.key"},
        "session_store": {"path": "/data/sessions.db", "sealing_key_file": "/secrets/broker-sealing.key", "job_retention": "96h"},
        "offers_source": "admin", "offers_state_path": "/data/offers.json",
        "pool_snapshot": {"url": spec["admin_url"] + "/admin/v1/backend-selection-snapshot", "auth": controller_auth},
        "receipt_sink": {"url": spec["admin_url"] + "/admin/v1/work-receipts", "auth": controller_auth}})
    source = {"pool_id": pid, "source_id": source_id, "broker_id": B, "chain_id": 42161,
              "payee": spec["payee"], "url": spec["broker_admin_url"]}
    def revenue(caller):
        return dict(source, token_file="/secrets/" + token_name(caller, B, "revenue-reader"))
    config(C, {"identity": {"orch_eth_address": spec["payee"], "label": "us-pool"},
        "listen": {"paid": ":8080", "member": ":8084", "metrics": ":9090"},
        "service_auth_file": "/secrets/service-auth.json", "member_issuer_trust_file": "/secrets/member-trust.json",
        "template_catalog_dir": "/etc/livepeer/templates", "ownership": client(C, O, "ownership-controller", ownership_url),
        "revenue_sources": [revenue(C)], "bootstrap": {"public_controller_url": spec["member_url"],
            "member_agent_image": agent_image,
            "brokers": [{"name": B, "admin_url": spec["broker_admin_url"], "public_url": spec["broker_url"],
                         "template_ids": TEMPLATES, "auth": client(C, B, "controller", spec["broker_admin_url"], True)}]}})
    config(R, {"pool_controller": client(R, C, "reconciler", spec["admin_url"]), "revenue_sources": [revenue(R)],
        "round_source": {"protocol_daemon_socket": "/run/livepeer/protocol.sock"},
        "reconcile": {"state_path": "/data/reconciler.db", "backfill_limit": 1000, "retry_interval_ms": 5000,
                      "metrics_addr": ":9090"}})
    config(E, {"pool_controller": client(E, C, "payout-executor", spec["admin_url"]),
        "executor": {"executor_id": "us-payout", "expected_wallet_address": spec["payout_wallet"], "chain_id": 42161,
            "rpc_urls": rpc_urls, "keystore_path": "/secrets/payout-keystore.json", "keystore_password_path": "/secrets/payout-password",
            "state_path": "/data/executor.db", "intent_store_path": "/data/payout-intents.db", "confirmation_blocks": 4,
            "metrics_addr": ":9090"}})
    config("member-portal", {"listen": ":8080", "public_origin": spec["portal_url"], "state_path": "/data/portal.db",
        "signer_file": "/secrets/portal-issuer.json", "regions": [{"pool_id": pid, "name": "US pool", "url": spec["member_url"]}]})
    write(output / "compose.yaml", compose)  # JSON is valid YAML and accepted by Compose.
    write(output / "operator/coordinator-brokers.json", {"brokers": [{"pool_id": pid, "name": B,
        "base_url": spec["broker_admin_url"], "admin_token_ref": "file:///run/secrets/" + token_name("coordinator", B, "coordinator")}]})
    write(output / "operator/us-source-registration.json", {"source": source, "start_round": None, "reason": "initial US transcode source"})
    write(output / "node.json", spec)
    write(output / "manifest.json", {"stage": "US transcode only; EU/audio/LLM not yet provisioned",
        "pool_id": pid, "source_id": source_id, "expected_controller_volume": spec["deployment_id"] + "-us-controller-data",
        "volumes": list(volumes), "network": spec["deployment_id"] + "-us",
        "status": "prepared offline; no volumes created, services started, or financial operations performed"})
    return output


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--base", type=Path, default=Path("/opt/livepeer/open-pool-v2"))
    p.add_argument("--spec", required=True, type=Path)
    p.add_argument("--rpc-file", required=True, type=Path, help="One HTTPS RPC URL per line; keep private")
    p.add_argument("--output", type=Path)
    p.add_argument("--registry", default="tztcloud")
    p.add_argument("--tag", default="v2.0.0")
    p.add_argument("--agent-image", help="Immutable agent image; otherwise resolve locally pulled agent tag")
    args = p.parse_args()
    try:
        urls = [x.strip() for x in read(args.rpc_file).decode().splitlines() if x.strip()]
        agent_image = args.agent_image
        if not agent_image:
            ref = f"{args.registry}/livepeer-pool-member-agent:{args.tag}"
            inspected = json.loads(subprocess.check_output(["docker", "image", "inspect", ref]))[0]
            candidates = [x for x in inspected.get("RepoDigests", [])
                          if x.split("@", 1)[0].removeprefix("docker.io/") == ref.rsplit(":", 1)[0].removeprefix("docker.io/")]
            require(bool(candidates), "Pull the member-agent image first, or supply --agent-image repository@sha256:digest")
            agent_image = candidates[0]
        folder = prepare(args.base.resolve(), json.loads(read(args.spec)), urls,
                         (args.output or args.base / "deployment").resolve(), agent_image, args.registry, args.tag)
        print(f"Prepared: {folder}/compose.yaml")
        print("No services started. Preserve original material and back up deployment/ securely.")
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as exc:
        raise SystemExit(f"Preparation failed: {exc}; preserve partial output for inspection")
