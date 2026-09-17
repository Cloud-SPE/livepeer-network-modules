"""Compose/config renderer; no subprocess, network or key generation."""
import json
from pathlib import Path


def read(path):
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


def links(region, role):
    b, c, o, r, e = f"{region}-{role}-broker", f"{region}-controller", "gpu-ownership", f"{region}-reconciler", f"{region}-executor"
    return [("operator", c, "pool-admin", "controller"), ("operator", b, "pool-admin", b),
        ("operator", o, "ownership-admin", "ownership"), ("coordinator", b, "coordinator", b),
        (b, c, "broker", "controller"), (b, o, "ownership-reader", "ownership"),
        (c, b, "controller", b), (c, b, "revenue-reader", b), (c, o, "ownership-controller", "ownership"),
        (r, b, "revenue-reader", b), (r, c, "reconciler", "controller"), (e, c, "payout-executor", "controller")]


def render(output, material, spec, rpc_urls, images, options):
    region, role = options["region"], options["role"]
    B, C, O, R, E = f"{region}-{role}-broker", f"{region}-controller", "gpu-ownership", f"{region}-reconciler", f"{region}-executor"
    IMAGES = {C: "pool-controller", O: "pool-controller", B: "capability-broker", B + "-receiver": "payment-daemon",
              region + "-observer": "protocol-daemon", R: "pool-reconciler", E: "pool-payout-executor", "member-portal": "member-portal"}
    enabled = {B, B + "-receiver"}
    if options["management"]:
        enabled.update({C, R, E, region + "-observer"})
    if options["ownership"]:
        enabled.add(O)
    if options["portal"]:
        enabled.add("member-portal")
    TEMPLATES = {"transcode": ["video-transcode-abr", "video-transcode-vod", "video-transcode-live"],
                 "audio": ["openai-audio-transcriptions-whisper-large-v3", "openai-audio-speech-kokoro"],
                 "llm": ["openai-chat-qwen3.6-27b"]}[role]
    LINKS = links(region, role)
    secret = material
    creds = material / "service-credentials"
    pid, source_id = spec["pool_id"], spec["source_id"]
    controller_volume = options["controller_volume"]
    agent_image = images["pool-member-agent"]
    services, volumes = {}, {}
    compose = {"name": spec["deployment_id"] + "-" + region, "services": services, "volumes": volumes,
               "networks": {"default": {"name": spec["deployment_id"] + "-" + region}}}

    def copy(name, dest, path):
        if name not in services:
            return
        write(output / "secrets" / name / dest, read(path))

    def add(name, command, profile=None):
        if name not in enabled:
            return
        volume = controller_volume if name == C else spec["deployment_id"] + "-" + name + "-data"
        volumes[volume] = {"external": True, "name": volume}
        services[name] = {"image": images[IMAGES[name]],
            "restart": "unless-stopped", "user": "65532:65532", "read_only": True,
            "security_opt": ["no-new-privileges:true"], "cap_drop": ["ALL"],
            "tmpfs": ["/tmp:uid=65532,gid=65532,mode=0700"], "command": command,
            "volumes": [f"{volume}:/data", f"./secrets/{name}:/secrets:ro"]}
        (output / "secrets" / name).mkdir(parents=True, mode=0o700)
        if profile:
            services[name]["profiles"] = [profile]

    def config(name, data):
        if name not in enabled:
            return
        write(output / "config" / (name + ".json"), data)
        services[name]["volumes"].append(f"./config/{name}.json:/etc/livepeer/config.json:ro")

    def client(caller, target, role, url, scoped=False):
        result = {"pool_id": pid, "token_file": "/secrets/" + token_name(caller, target, role)}
        result.update({"method": "scoped"} if scoped else {"url": url})
        if target == O and (secret / "ownership-ca.crt").exists():
            result["ca_file"] = "/secrets/ownership-ca.crt"
        return result

    def socket(name, owners):
        volume = spec["deployment_id"] + "-" + name + "-socket"
        if not any(owner in services for owner in owners):
            return
        volumes[volume] = {"external": True, "name": volume}
        for owner in owners:
            if owner not in services:
                continue
            services[owner]["volumes"].append(volume + ":/run/livepeer")

    add(O, ["--listen=:8443", "--data-dir=/data", "--service-auth-file=/secrets/service-auth.json",
            "--tls-cert=/secrets/tls.crt", "--tls-key=/secrets/tls.key"])
    if O in services:
        services[O]["entrypoint"] = ["/usr/local/bin/livepeer-pool-ownership"]
    add(C, ["serve", "--config=/etc/livepeer/config.json", "--data-dir=/data"])
    add(region + "-observer", ["--mode=read-only", "--chain-rpc-urls=" + ",".join(rpc_urls),
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
    socket(region + "-observer", [region + "-observer", R])
    socket(receiver, [receiver, B])

    for caller, target, role, _ in LINKS:
        filename = token_name(caller, target, role)
        if caller in services:
            copy(caller, filename, creds / caller / filename)
        else:
            write(output / "operator" / filename, read(creds / caller / filename))
    for target in (C, B, O):
        if target in services:
            copy(target, "service-auth.json", creds / target / "service-auth.json")
        else:
            write(output / "operator" / (target + "-service-auth.json"), read(creds / target / "service-auth.json"))
    for role, service in (("receiver", receiver), ("payout", E)):
        for suffix in ("-keystore.json", "-password"):
            copy(service, role + suffix, secret / (role + suffix))
    for filename in ("broker-settlement.key", "broker-sealing.key"):
        copy(B, filename, secret / filename)
    copy("member-portal", "portal-issuer.json", secret / "portal-issuer.json")
    copy(C, "member-trust.json", secret / "member-trust.json")

    copy(O, "tls.key", secret / "ownership-tls.key")
    copy(O, "tls.crt", secret / "ownership-tls.crt")
    for caller in (B, C):
        if (secret / "ownership-ca.crt").exists():
            copy(caller, "ownership-ca.crt", secret / "ownership-ca.crt")
    ownership_url = options["ownership_url"]
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
    config(C, {"identity": {"orch_eth_address": spec["payee"], "label": region + "-pool"},
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
    if E in enabled:
        config(E, {"pool_controller": client(E, C, "payout-executor", spec["admin_url"]),
            "executor": {"executor_id": region + "-payout", "expected_wallet_address": spec["payout_wallet"], "chain_id": 42161,
                "rpc_urls": rpc_urls, "keystore_path": "/secrets/payout-keystore.json", "keystore_password_path": "/secrets/payout-password",
                "state_path": "/data/executor.db", "intent_store_path": "/data/payout-intents.db", "confirmation_blocks": 4,
                "metrics_addr": ":9090"}})
    config("member-portal", {"listen": ":8080", "public_origin": spec["portal_url"], "state_path": "/data/portal.db",
        "signer_file": "/secrets/portal-issuer.json", "regions": [{"pool_id": pid, "name": region.upper() + " pool", "url": spec["member_url"]}]})
    write(output / "compose.yaml", compose)
    write(output / "operator/coordinator-brokers.json", {"brokers": [{"pool_id": pid, "name": B,
        "base_url": spec["broker_admin_url"], "admin_token_ref": "file:///run/secrets/" + token_name("coordinator", B, "coordinator")}]})
    write(output / "operator/source-registration.json", {"source": source, "start_round": None, "reason": "initial regional source"})
    return compose
