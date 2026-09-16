#!/usr/bin/env python3
"""Offline regional configuration compiler. Never starts containers or calls RPC.

JSON is also YAML, so generated Compose and component files need no host YAML
dependency. Output is exclusive: re-running cannot replace live credentials.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
from urllib.parse import urlsplit

BROKERS = {"eu-transcode-broker": "eu", "us-transcode-broker": "us",
           "audio-broker": "us", "llm-broker": "us"}
IMAGES = {"controller", "broker", "receiver", "observer", "reconciler",
          "executor", "portal", "agent", "proxy", "volume-init"}
PIN = re.compile(r"(?:sha256:|[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:)[0-9a-f]{64}\Z")
NAME = re.compile(r"[a-z][a-z0-9-]{0,62}\Z")
ADDRESS = re.compile(r"0x[0-9a-fA-F]{40}\Z")
SOURCE = re.compile(r"0x[0-9a-f]{64}\Z")


def require(ok, message):
    if not ok:
        raise ValueError(message)


def fields(obj, required, optional=()):
    require(isinstance(obj, dict), "expected object")
    require(set(required) <= obj.keys() and obj.keys() <= set(required) | set(optional),
            "missing or unknown fields: " + ",".join(sorted(obj.keys())))


def origin(value):
    u = urlsplit(value)
    require(u.scheme == "https" and u.hostname and u.netloc == u.hostname
            and not u.path and not u.query and not u.fragment
            and re.fullmatch(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", u.hostname),
            "service URL must be a canonical HTTPS origin on port 443")
    return u.hostname


def validate(spec):
    fields(spec, {"version", "deployment_id", "chain_id", "payee", "rpc_urls", "credential_expires_at",
                  "images", "provenance", "hosts", "regions", "brokers", "portal", "ownership"})
    require(NAME.fullmatch(spec["deployment_id"]), "stable deployment_id must be a role-like name")
    require(spec["version"] == 1 and spec["chain_id"] == 42161, "version 1 requires Arbitrum One")
    require(ADDRESS.fullmatch(spec["payee"]) and int(spec["payee"], 16), "invalid payee")
    require(spec["rpc_urls"], "chain RPC URLs required; dev receiver mode forbidden")
    for url in spec["rpc_urls"]:
        u = urlsplit(url)
        require(u.scheme == "https" and u.hostname and not u.username and not u.fragment,
                "RPC endpoints require HTTPS")
    expiry = dt.datetime.fromisoformat(spec["credential_expires_at"].replace("Z", "+00:00"))
    require(expiry.tzinfo is not None and expiry > dt.datetime.now(dt.timezone.utc), "expired service credentials")
    require(set(spec["images"]) == IMAGES, "all service and helper images must be pinned")
    require(all(isinstance(v, str) and PIN.fullmatch(v) for v in spec["images"].values()), "mutable image reference")
    fields(spec["provenance"], {"git_commit", "tree_sha256", "validation_evidence", "local_only"})
    require(re.fullmatch(r"[0-9a-f]{40}", spec["provenance"]["git_commit"])
            and re.fullmatch(r"[0-9a-f]{64}", spec["provenance"]["tree_sha256"])
            and spec["provenance"]["validation_evidence"], "image provenance and evidence required")
    require(type(spec["provenance"]["local_only"]) is bool, "local_only must be explicit")
    if any(v.startswith("sha256:") for v in spec["images"].values()):
        require(spec["provenance"]["local_only"], "local image IDs are not rollout evidence")
    require(spec["hosts"] and all(NAME.fullmatch(h) and h != "secure-orch" for h in spec["hosts"]), "invalid regional host role")
    for host in spec["hosts"].values():
        fields(host, {"tls_cert", "tls_key"})
    require(set(spec["regions"]) == {"eu", "us"} and set(spec["brokers"]) == set(BROKERS), "initial EU1/US3 topology required")
    identities, wallets, origins, used_hosts, key_paths = set(), {spec["payee"].lower()}, set(), set(), set()

    def unique_origin(value):
        domain = origin(value)
        require(domain not in origins, "duplicate ingress origin")
        origins.add(domain)

    def host(value):
        require(value in spec["hosts"], "unknown physical host role")
        used_hosts.add(value)

    def wallet(value, key):
        require(ADDRESS.fullmatch(value) and int(value, 16) and value.lower() not in wallets,
                "regional payout/receiver/payee wallet collision or invalid address")
        wallets.add(value.lower())
        path = str(Path(key).resolve())
        require(path not in key_paths, "key material reused across signing roles")
        key_paths.add(path)

    for region in spec["regions"].values():
        fields(region, {"pool_id", "host", "member_url", "admin_url", "payout_wallet", "keystore", "password_file"})
        pid = region["pool_id"]
        require(re.fullmatch(r"pool_[0-9a-f]{32}", pid) and pid not in identities, "invalid or duplicate persisted pool identity")
        identities.add(pid)
        host(region["host"])
        unique_origin(region["member_url"])
        unique_origin(region["admin_url"])
        wallet(region["payout_wallet"], region["keystore"])
    templates = {"eu": set(), "us": set()}
    require(len({b["host"] for b in spec["brokers"].values()}) == 4, "initial topology requires four separate broker hosts")
    for role, broker in spec["brokers"].items():
        fields(broker, {"host", "pool", "source_id", "public_url", "admin_url", "template_ids",
                        "receiver_wallet", "keystore", "password_file", "settlement_key", "sealing_key"})
        require(broker["pool"] == BROKERS[role], "broker has wrong regional owner")
        sid = broker["source_id"]
        require(SOURCE.fullmatch(sid) and int(sid, 16) and sid not in identities, "invalid or duplicate settlement domain")
        identities.add(sid)
        host(broker["host"])
        unique_origin(broker["public_url"])
        unique_origin(broker["admin_url"])
        wallet(broker["receiver_wallet"], broker["keystore"])
        ids = broker["template_ids"]
        require(isinstance(ids, list) and ids and all(NAME.fullmatch(t) or re.fullmatch(r"[a-z0-9][a-z0-9.-]*", t) for t in ids), "template IDs required")
        require(len(set(ids)) == len(ids) and not templates[broker["pool"]].intersection(ids), "template owned by multiple brokers in one region")
        templates[broker["pool"]].update(ids)
    fields(spec["portal"], {"host", "url", "signer_file", "trust_file", "ca_file"})
    fields(spec["ownership"], {"host", "url"})
    for service in (spec["portal"], spec["ownership"]):
        host(service["host"])
        unique_origin(service["url"])
    require(used_hosts == set(spec["hosts"]), "unused host entries are forbidden")
    return spec


def write(path, value, mode=0o600):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    data = value if isinstance(value, str) else json.dumps(value, indent=2, sort_keys=True) + "\n"
    with path.open("x") as stream:
        stream.write(data)
    path.chmod(mode)


class Compiler:
    def __init__(self, spec, output):
        self.s = validate(spec)
        self.root = Path(output)
        require(not self.root.exists(), "output already exists; refuse credential or deployment overwrite")
        self.hosts = {h: {"name": spec["deployment_id"] + "-" + h, "services": {}, "volumes": {}} for h in spec["hosts"]}
        self.services, self.auth, self.vhosts, self.inventory = {}, {}, {h: [] for h in spec["hosts"]}, []

    def material(self, source):
        path = Path(source)
        require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 4 * 1024 * 1024, "missing or unsafe provisioning material: " + str(path))
        return path.read_bytes()

    def secret(self, service, name, data):
        folder = self.root / self.services[service]["host"] / "secrets" / service
        folder.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = folder / name
        require(not path.exists(), "duplicate secret target")
        path.write_bytes(data if isinstance(data, bytes) else data.encode())
        path.chmod(0o600)
        return "/secrets/" + name

    def copy(self, service, name, source):
        return self.secret(service, name, self.material(source))

    def add(self, name, host, image, command, *, entrypoint=None, data=True):
        require(name not in self.services, "duplicate service name")
        volume = self.s["deployment_id"] + "-" + name + "-data"
        definition = {"image": self.s["images"][image], "pull_policy": "never" if self.s["images"][image].startswith("sha256:") else "missing",
                      "restart": "unless-stopped", "user": "65532:65532", "read_only": True,
                      "security_opt": ["no-new-privileges:true"], "cap_drop": ["ALL"], "tmpfs": ["/tmp:uid=65532,gid=65532,mode=0700"],
                      "command": command, "volumes": [f"./secrets/{name}:/secrets:ro"]}
        if entrypoint:
            definition["entrypoint"] = entrypoint
        if data:
            self.hosts[host]["volumes"][volume] = {"name": volume, "external": True}
            definition["volumes"].append(volume + ":/data")
            self.inventory.append({"host": host, "service": name, "volume": volume, "mount": "/data"})
        self.hosts[host]["services"][name] = definition
        self.services[name] = {"host": host, "definition": definition}
        self.auth[name] = []
        (self.root / host / "secrets" / name).mkdir(parents=True, mode=0o700)
        self.copy(name, "ca.crt", self.s["portal"]["ca_file"])
        return name

    def config(self, name, value):
        host = self.services[name]["host"]
        write(self.root / host / "config" / (name + ".json"), value)
        self.services[name]["definition"]["volumes"].append(f"./config/{name}.json:/etc/livepeer/config.json:ro")

    def socket(self, host, name, owners):
        volume = self.s["deployment_id"] + "-" + name + "-socket"
        self.hosts[host]["volumes"][volume] = {"name": volume, "external": True}
        for owner in owners:
            require(self.services[owner]["host"] == host, "financial socket cannot cross hosts")
            self.services[owner]["definition"]["volumes"].append(volume + ":/run/livepeer")

    def credential(self, caller, target, pool_id, role, resource, source_id=""):
        token = secrets.token_hex(32)
        name = caller + "-to-" + target + "-" + role
        principal = {"id": name, "pool_id": pool_id, "roles": [role], "resources": [resource],
                     "token_sha256": hashlib.sha256(token.encode()).hexdigest(), "expires_at": self.s["credential_expires_at"]}
        if source_id:
            principal["source_id"] = source_id
        self.auth[target].append(principal)
        if caller in self.services:
            return self.secret(caller, name + ".token", token + "\n")
        write(self.root / "operator" / (name + ".token"), token + "\n")
        return "/secrets/" + name + ".token"

    def client(self, caller, target, pool_id, role, resource, url, source_id="", scoped=False):
        result = {"pool_id": pool_id, "token_file": self.credential(caller, target, pool_id, role, resource, source_id), "ca_file": "/secrets/ca.crt"}
        if scoped:
            result["method"] = "scoped"
        else:
            result["url"] = url
        return result

    def ingress(self, host, url, service, port, kind):
        self.vhosts[host].append({"domain": origin(url), "service": service, "port": port, "kind": kind})

    def compile(self):
        # Material validation precedes generation of credentials and outputs.
        for r in self.s["regions"].values():
            self.check_keystore(r["keystore"], r["payout_wallet"])
        for b in self.s["brokers"].values():
            self.check_keystore(b["keystore"], b["receiver_wallet"])
        self.root.mkdir(mode=0o700)
        try:
            self._compile()
        except Exception:
            # Only the exclusively-created, not-yet-delivered output is removed.
            shutil.rmtree(self.root)
            raise
        return self.root

    def check_keystore(self, path, expected):
        data = json.loads(self.material(path))
        require(data.get("address", "").lower().removeprefix("0x") == expected.lower()[2:]
                and data.get("version") == 3 and ("crypto" in data or "Crypto" in data), "keystore address/format differs from declared signing wallet")

    def _compile(self):
        s = self.s
        owner = self.add("gpu-ownership", s["ownership"]["host"], "controller", ["--listen=:8443", "--data-dir=/data", "--service-auth-file=/secrets/service-auth.json", "--tls-cert=/secrets/tls.crt", "--tls-key=/secrets/tls.key"], entrypoint=["/usr/local/bin/livepeer-pool-ownership"])
        tls = s["hosts"][s["ownership"]["host"]]
        self.copy(owner, "tls.crt", tls["tls_cert"])
        self.copy(owner, "tls.key", tls["tls_key"])
        self.ingress(s["ownership"]["host"], s["ownership"]["url"], owner, 8443, "ownership")
        portal = self.add("member-portal", s["portal"]["host"], "portal", ["--config=/etc/livepeer/config.json"])
        self.copy(portal, "issuer.json", s["portal"]["signer_file"])
        self.ingress(s["portal"]["host"], s["portal"]["url"], portal, 8080, "portal")
        for region, r in s["regions"].items():
            host, pid = r["host"], r["pool_id"]
            controller = self.add(region + "-controller", host, "controller", ["serve", "--config=/etc/livepeer/config.json", "--data-dir=/data"])
            self.copy(controller, "member-trust.json", s["portal"]["trust_file"])
            observer = self.add(region + "-observer", host, "observer", ["--mode=read-only", "--chain-rpc-urls=" + ",".join(s["rpc_urls"]), "--socket=/run/livepeer/protocol.sock", "--store-path=/data/observer.db"])
            reconciler = self.add(region + "-reconciler", host, "reconciler", ["watch-rounds", "--config=/etc/livepeer/config.json"])
            executor = self.add(region + "-executor", host, "executor", ["reconcile-loop", "--config=/etc/livepeer/config.json"])
            self.copy(executor, "payout-keystore.json", r["keystore"])
            self.copy(executor, "payout-password", r["password_file"])
            self.socket(host, region + "-observer", [observer, reconciler])
            self.ingress(host, r["member_url"], controller, 8084, "member")
            self.ingress(host, r["admin_url"], controller, 8080, "controller")
            self.credential("operator-" + region, controller, pid, "pool-admin", "controller")
            self.credential("operator-" + region, owner, pid, "ownership-admin", "ownership")
        for role, b in s["brokers"].items():
            region, host = b["pool"], b["host"]
            pid = s["regions"][region]["pool_id"]
            receiver = role + "-receiver"
            self.add(receiver, host, "receiver", ["--mode=receiver", "--socket=/run/livepeer/payment.sock", "--db=/data/receiver.db", "--txintent-db=/data/txintents.db", "--settlement-domain-id=" + b["source_id"], "--chain-rpc-urls=" + ",".join(s["rpc_urls"]), "--keystore-path=/secrets/receiver-keystore.json", "--keystore-password-file=/secrets/receiver-password", "--orch-address=" + s["payee"], "--metrics-listen=:9090"])
            self.copy(receiver, "receiver-keystore.json", b["keystore"])
            self.copy(receiver, "receiver-password", b["password_file"])
            self.add(role, host, "broker", ["--config=/etc/livepeer/config.json"])
            self.copy(role, "settlement-key", b["settlement_key"])
            self.copy(role, "sealing-key", b["sealing_key"])
            self.socket(host, role + "-receiver", [role, receiver])
            self.ingress(host, b["public_url"], role, 8080, "broker-public")
            self.ingress(host, b["admin_url"], role, 8080, "broker-admin")
            self.credential("operator-" + role, role, pid, "pool-admin", role)
            self.credential("coordinator", role, pid, "coordinator", role)
        for role, b in s["brokers"].items():
            r = s["regions"][b["pool"]]
            pid, controller = r["pool_id"], b["pool"] + "-controller"
            controller_auth = self.client(role, controller, pid, "broker", "controller", r["admin_url"], b["source_id"], scoped=True)
            self.config(role, {"pool_id": pid, "service_resource": role, "service_auth_file": "/secrets/service-auth.json",
                "ownership": self.client(role, owner, pid, "ownership-reader", "ownership", s["ownership"]["url"]),
                "identity": {"orch_eth_address": s["payee"], "label": role, "settlement_key_file": "/secrets/settlement-key"},
                "external_base_url": b["public_url"], "listen": {"paid": ":8080", "metrics": ":9090"},
                "payment_daemon": {"socket": "/run/livepeer/payment.sock"}, "accounting_store_path": "/data/work-accounting.db",
                "credential_store": {"path": "/data/credentials.db", "sealing_key_file": "/secrets/sealing-key"},
                "session_store": {"path": "/data/sessions.db", "sealing_key_file": "/secrets/sealing-key", "job_retention": "96h"},
                "offers_source": "admin", "offers_state_path": "/data/offers.json",
                "pool_snapshot": {"url": r["admin_url"] + "/admin/v1/backend-selection-snapshot", "auth": controller_auth},
                "receipt_sink": {"url": r["admin_url"] + "/admin/v1/work-receipts", "auth": controller_auth}})
        for region, r in s["regions"].items():
            pid, controller, reconciler, executor = r["pool_id"], region + "-controller", region + "-reconciler", region + "-executor"
            sources, targets = [], []
            for role, b in s["brokers"].items():
                if b["pool"] != region:
                    continue
                source = {"pool_id": pid, "source_id": b["source_id"], "broker_id": role, "chain_id": s["chain_id"], "payee": s["payee"], "url": b["admin_url"], "ca_file": "/secrets/ca.crt"}
                source["token_file"] = self.credential(reconciler, role, pid, "revenue-reader", role)
                sources.append(source)
                targets.append({"name": role, "admin_url": b["admin_url"], "public_url": b["public_url"], "template_ids": b["template_ids"], "auth": self.client(controller, role, pid, "controller", role, b["admin_url"], scoped=True)})
                # Controller prepares/validates source proofs with its own reader credential.
            controller_sources = []
            for source in sources:
                own = dict(source)
                own["token_file"] = self.credential(controller, source["broker_id"], pid, "revenue-reader", source["broker_id"])
                controller_sources.append(own)
            self.config(controller, {"identity": {"orch_eth_address": s["payee"], "label": region + "-pool"}, "listen": {"paid": ":8080", "member": ":8084", "metrics": ":9090"}, "service_auth_file": "/secrets/service-auth.json", "member_issuer_trust_file": "/secrets/member-trust.json", "template_catalog_dir": "/etc/livepeer/templates",
                "ownership": self.client(controller, owner, pid, "ownership-controller", "ownership", s["ownership"]["url"]), "revenue_sources": controller_sources,
                "bootstrap": {"public_controller_url": r["member_url"], "member_agent_image": s["images"]["agent"], "brokers": targets}})
            self.config(reconciler, {"pool_controller": self.client(reconciler, controller, pid, "reconciler", "controller", r["admin_url"]), "revenue_sources": sources, "round_source": {"protocol_daemon_socket": "/run/livepeer/protocol.sock"}, "reconcile": {"state_path": "/data/reconciler.db", "backfill_limit": 1000, "retry_interval_ms": 5000, "metrics_addr": ":9090"}})
            self.config(executor, {"pool_controller": self.client(executor, controller, pid, "payout-executor", "controller", r["admin_url"]), "executor": {"executor_id": region + "-payout", "expected_wallet_address": r["payout_wallet"], "chain_id": s["chain_id"], "rpc_urls": s["rpc_urls"], "keystore_path": "/secrets/payout-keystore.json", "keystore_password_path": "/secrets/payout-password", "state_path": "/data/executor.db", "intent_store_path": "/data/payout-intents.db", "confirmation_blocks": 4, "metrics_addr": ":9090"}})
            write(self.root / "operator" / (region + "-source-registration.json"), [{"source": {k: v for k, v in source.items() if k not in {"token_file", "ca_file"}}, "start_round": None, "reason": "initial regional source registration"} for source in sources])
        self.config(portal, {"listen": ":8080", "public_origin": s["portal"]["url"], "state_path": "/data/portal.db", "signer_file": "/secrets/issuer.json", "regions": [{"pool_id": r["pool_id"], "name": region.upper() + " pool", "url": r["member_url"], "ca_file": "/secrets/ca.crt"} for region, r in s["regions"].items()]})
        for service, credentials in self.auth.items():
            if credentials:
                self.secret(service, "service-auth.json", json.dumps({"credentials": credentials}, indent=2) + "\n")
        self.render_proxies()
        write(self.root / "operator" / "coordinator-brokers.json", {"brokers": [{"pool_id": s["regions"][b["pool"]]["pool_id"], "name": role, "base_url": b["admin_url"], "admin_token_ref": "file:///run/secrets/coordinator-to-" + role + "-coordinator.token"} for role, b in s["brokers"].items()]})
        for host, compose in self.hosts.items():
            write(self.root / host / "compose.json", compose)
        write(self.root / "manifest.json", {"version": 1, "topology": s, "durable_volumes": self.inventory, "ingress": self.vhosts,
             "material_files": sorted(p.relative_to(self.root).as_posix() for p in self.root.rglob("*") if p.is_file()),
             "status": "generated offline; no rollout or funds movement", "requires_existing_controller_identities": {r + "-controller": v["pool_id"] for r, v in s["regions"].items()}})

    def render_proxies(self):
        for host, entries in self.vhosts.items():
            proxy = self.add(host + "-https", host, "proxy", [], data=False)
            definition = self.services[proxy]["definition"]
            definition.pop("command")
            definition["ports"] = ["443:8443"]
            definition["tmpfs"] += ["/var/cache/nginx:uid=65532,gid=65532,mode=0700", "/var/run:uid=65532,gid=65532,mode=0700"]
            self.copy(proxy, "tls.crt", self.s["hosts"][host]["tls_cert"])
            self.copy(proxy, "tls.key", self.s["hosts"][host]["tls_key"])
            blocks = []
            for entry in entries:
                kind, service = entry["kind"], entry["service"]
                endpoint = ("https" if kind == "ownership" else "http") + "://" + service + ":" + str(entry["port"])
                settings = f"set $service_origin {endpoint}; proxy_pass $service_origin; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto https; proxy_http_version 1.1; proxy_buffering off; proxy_request_buffering off; proxy_pass_trailers on; proxy_set_header TE trailers; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection $connection_upgrade; proxy_read_timeout 3600s;"
                if kind == "ownership":
                    settings += f" proxy_ssl_verify on; proxy_ssl_trusted_certificate /secrets/ca.crt; proxy_ssl_server_name on; proxy_ssl_name {entry['domain']};"
                if kind == "broker-public":
                    locations = f"location ~ ^/(admin|reporting)(/|$) {{ return 404; }} location / {{ {settings} }}"
                elif kind in {"broker-admin", "controller"}:
                    locations = f"location ~ ^/(admin/v1|reporting/v1)(/|$) {{ {settings} }} location / {{ return 404; }}"
                    if kind == "broker-admin":
                        locations += f" location /registry/ {{ limit_except GET {{ deny all; }} {settings} }}"
                elif kind == "member":
                    locations = f"location /member/v1/ {{ {settings} }} location / {{ return 404; }}"
                elif kind == "ownership":
                    locations = f"location /ownership/v1/ {{ {settings} }} location / {{ return 404; }}"
                else:
                    locations = f"location / {{ {settings} }}"
                body_limit = "0" if kind == "broker-public" else "8m"
                blocks.append(f"server {{ listen 8443 ssl; server_name {entry['domain']}; ssl_certificate /secrets/tls.crt; ssl_certificate_key /secrets/tls.key; ssl_protocols TLSv1.2 TLSv1.3; client_max_body_size {body_limit}; {locations} }}")
            conf = "pid /tmp/nginx.pid; events {} http { resolver 127.0.0.11 valid=10s ipv6=off; map $http_upgrade $connection_upgrade { default upgrade; '' te; } server { listen 8443 ssl default_server; ssl_reject_handshake on; }\n" + "\n".join(blocks) + "\n}\n"
            write(self.root / host / "config" / "nginx.conf", conf)
            definition["volumes"].append("./config/nginx.conf:/etc/nginx/nginx.conf:ro")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("spec", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    with args.spec.open() as stream:
        spec = json.load(stream)
    result = Compiler(spec, args.output).compile()
    print("Generated offline configuration in", result)


if __name__ == "__main__":
    main()
