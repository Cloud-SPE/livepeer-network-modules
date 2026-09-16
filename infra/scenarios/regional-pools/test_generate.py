import copy
import datetime as dt
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from generate import BROKERS, IMAGES, Compiler, validate


def fixture(root):
    """Synthetic material for parser tests only; never suitable for rollout."""
    root = Path(root)
    root.mkdir(exist_ok=True)
    def material(name, value):
        path = root / name
        path.write_text(value)
        path.chmod(0o600)
        return str(path)
    ca = material("ca.crt", "synthetic fixture; replace with test CA for parser integration")
    tls = material("tls.key", "synthetic fixture")
    password = material("password", "synthetic-only")
    signer = material("issuer.json", "{}")
    trust = material("trust.json", "{}")
    def wallet(n):
        address = "0x" + f"{n:040x}"
        return address, material(f"keystore-{n}.json", json.dumps({"version": 3, "address": address[2:], "crypto": {"fixture": "not a usable encrypted key"}}))
    spec = {"version": 1, "deployment_id": "regional-pools-test", "chain_id": 42161, "payee": "0x" + "1" * 40,
        "rpc_urls": ["https://rpc.example.invalid"], "credential_expires_at": (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=30)).isoformat(),
        "images": {key: "sha256:" + "a" * 64 for key in IMAGES},
        "provenance": {"git_commit": "b" * 40, "tree_sha256": "c" * 64, "validation_evidence": "synthetic configuration unit test only", "local_only": True},
        "hosts": {role: {"tls_cert": ca, "tls_key": tls} for role in BROKERS},
        "regions": {}, "brokers": {},
        "portal": {"host": "eu-transcode-broker", "url": "https://portal.example.invalid", "signer_file": signer, "trust_file": trust, "ca_file": ca},
        "ownership": {"host": "eu-transcode-broker", "url": "https://ownership.example.invalid"}}
    for i, region in enumerate(("eu", "us"), 2):
        address, key = wallet(i)
        spec["regions"][region] = {"pool_id": "pool_" + str(i) * 32, "host": region + "-transcode-broker", "member_url": f"https://{region}-members.example.invalid", "admin_url": f"https://{region}-management.example.invalid", "payout_wallet": address, "keystore": key, "password_file": password}
    template_ids = {"eu-transcode-broker": ["video-transcode-abr", "video-transcode-vod", "video-transcode-live"], "us-transcode-broker": ["video-transcode-abr", "video-transcode-vod", "video-transcode-live"], "audio-broker": ["openai-audio-transcriptions-whisper-large-v3", "openai-audio-speech-kokoro"], "llm-broker": ["openai-chat-qwen3.6-27b"]}
    for i, (role, pool) in enumerate(BROKERS.items(), 4):
        address, key = wallet(i)
        spec["brokers"][role] = {"host": role, "pool": pool, "source_id": "0x" + f"{i:064x}", "public_url": f"https://{role}.example.invalid", "admin_url": f"https://{role}-admin.example.invalid", "template_ids": template_ids[role], "receiver_wallet": address, "keystore": key, "password_file": password, "settlement_key": material(role + "-settlement.key", f"{i:064x}"), "sealing_key": material(role + "-seal.key", f"{i+10:064x}")}
    return spec


class TopologyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.spec = fixture(self.root / "material")

    def test_locked_boundaries(self):
        changes = [
            lambda s: s["regions"]["us"].update(pool_id=s["regions"]["eu"]["pool_id"]),
            lambda s: s["regions"]["us"].update(payout_wallet=s["regions"]["eu"]["payout_wallet"]),
            lambda s: s["regions"]["us"].update(payout_wallet=s["payee"]),
            lambda s: s["brokers"]["audio-broker"].update(pool="eu"),
            lambda s: s["brokers"].pop("llm-broker"),
            lambda s: s["brokers"]["audio-broker"].update(source_id=s["brokers"]["llm-broker"]["source_id"]),
            lambda s: s["brokers"]["audio-broker"].update(host="us-transcode-broker"),
            lambda s: s["regions"]["eu"].update(admin_url="http://eu.example.invalid"),
            lambda s: s["images"].update(executor="executor:latest"),
            lambda s: s["brokers"]["audio-broker"].update(template_ids=s["brokers"]["llm-broker"]["template_ids"]),
            lambda s: s.update(rpc_urls=[]),
            lambda s: s.update(automatic_failover=True),
            lambda s: s["provenance"].update(local_only=False),
        ]
        for change in changes:
            spec = copy.deepcopy(self.spec)
            change(spec)
            with self.subTest(change=change), self.assertRaises(ValueError):
                validate(spec)

    def test_durable_isolation_and_role_permissions(self):
        output = Compiler(self.spec, self.root / "out").compile()
        manifest = json.loads((output / "manifest.json").read_text())
        inventory = manifest["durable_volumes"]
        self.assertEqual(len(inventory), len({item["volume"] for item in inventory}))
        for region in ("eu", "us"):
            host = self.spec["regions"][region]["host"]
            config = json.loads((output / host / "config" / f"{region}-reconciler.json").read_text())
            self.assertEqual(len(config["revenue_sources"]), 1 if region == "eu" else 3)
            executor = json.loads((output / host / "config" / f"{region}-executor.json").read_text())
            self.assertEqual(executor["executor"]["expected_wallet_address"], self.spec["regions"][region]["payout_wallet"])
        for host in self.spec["hosts"]:
            compose = json.loads((output / host / "compose.json").read_text())
            self.assertEqual(sum(bool(service.get("ports")) for service in compose["services"].values()), 1)
            for name, service in compose["services"].items():
                self.assertNotIn("privileged", service)
                self.assertEqual(service["user"], "65532:65532")
                if name.endswith("observer"):
                    self.assertIn("--mode=read-only", service["command"])
                    self.assertNotIn("keystore", json.dumps(service))
                if name.endswith("executor"):
                    self.assertFalse(any("socket" in v for v in service["volumes"]))
        audio = output / "audio-broker" / "secrets" / "audio-broker"
        auth = json.loads((audio / "service-auth.json").read_text())["credentials"]
        reader = next(c for c in auth if c["id"] == "us-reconciler-to-audio-broker-revenue-reader")
        token = (output / "us-transcode-broker" / "secrets" / "us-reconciler" / "us-reconciler-to-audio-broker-revenue-reader.token").read_text().strip()
        self.assertEqual(reader["roles"], ["revenue-reader"])
        self.assertEqual(reader["resources"], ["audio-broker"])
        self.assertEqual(reader["token_sha256"], hashlib.sha256(token.encode()).hexdigest())
        self.assertFalse((output / "eu-transcode-broker" / "secrets" / "member-portal" / "service-auth.json").exists())
        controller_auth = json.loads((output / "us-transcode-broker" / "secrets" / "us-controller" / "service-auth.json").read_text())["credentials"]
        broker = next(c for c in controller_auth if c["id"] == "audio-broker-to-us-controller-broker")
        self.assertEqual(broker["source_id"], self.spec["brokers"]["audio-broker"]["source_id"])
        with self.assertRaises(ValueError):
            Compiler(self.spec, output).compile()

    def test_management_can_move_without_changing_identities(self):
        for region in ("eu", "us"):
            self.spec["hosts"][region + "-management"] = copy.deepcopy(self.spec["hosts"][region + "-transcode-broker"])
            self.spec["regions"][region]["host"] = region + "-management"
        out = Compiler(self.spec, self.root / "separate").compile()
        for region in ("eu", "us"):
            compose = json.loads((out / (region + "-management") / "compose.json").read_text())
            self.assertIn(region + "-controller", compose["services"])
            self.assertNotIn(region + "-transcode-broker", compose["services"])
            self.assertTrue(all("receiver-socket" not in json.dumps(s) for s in compose["services"].values()))

    def test_material_mismatch_and_failure_cleanup(self):
        self.spec["regions"]["eu"]["keystore"] = self.spec["regions"]["us"]["keystore"]
        with self.assertRaises(ValueError):
            Compiler(self.spec, self.root / "bad").compile()
        self.assertFalse((self.root / "bad").exists())
        self.spec = fixture(self.root / "new-material")
        self.spec["portal"]["signer_file"] = "/missing/issuer.json"
        with self.assertRaises(ValueError):
            Compiler(self.spec, self.root / "partial").compile()
        self.assertFalse((self.root / "partial").exists())


if __name__ == "__main__":
    unittest.main()
