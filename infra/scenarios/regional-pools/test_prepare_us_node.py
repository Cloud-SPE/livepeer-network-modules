import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import tempfile
import unittest

loader = importlib.util.spec_from_file_location("prepare_us", Path(__file__).with_name("prepare-us-node.py"))
module = importlib.util.module_from_spec(loader)
loader.loader.exec_module(module)


def fixture(base):
    spec = {"deployment_id": "open-pool", "pool_id": "pool_" + "1" * 32,
            "payee": "0x" + "1" * 40, "receiver_wallet": "0x" + "2" * 40, "payout_wallet": "0x" + "3" * 40,
            "broker_url": "https://broker.example.invalid", "broker_admin_url": "https://broker-admin.example.invalid",
            "member_url": "https://members.example.invalid", "admin_url": "https://management.example.invalid",
            "portal_url": "https://portal.example.invalid"}
    source = "0x" + "4" * 64
    root = base / "secrets"
    for role in ("receiver", "payout"):
        module.write(root / f"{role}-keystore.json", {"version": 3, "address": spec[role + "_wallet"][2:], "crypto": {}})
        module.write(root / f"{role}-password", "synthetic-not-a-wallet-password")
    for name in ("broker-settlement.key", "broker-sealing.key"):
        module.write(root / name, secrets.token_hex(32))
    module.write(root / "portal-issuer.json", {"issuer": spec["portal_url"]})
    module.write(root / "member-trust.json", {})
    module.write(base / "config/receiver-domain-id", source)
    creds = root / "service-credentials"
    expiry = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=365)).isoformat()
    auth = {}
    for caller, target, role, resource in module.LINKS:
        token = secrets.token_hex(32)
        name = module.token_name(caller, target, role)
        module.write(creds / caller / name, token + "\n")
        entry = {"id": name[:-6], "pool_id": spec["pool_id"], "roles": [role], "resources": [resource],
                 "token_sha256": hashlib.sha256(token.encode()).hexdigest(), "expires_at": expiry}
        if caller == module.B and target == module.C:
            entry["source_id"] = source
        auth.setdefault(target, []).append(entry)
    for target, entries in auth.items():
        module.write(creds / target / "service-auth.json", {"credentials": entries})
    module.write(creds / "metadata.json", {"pool_id": spec["pool_id"], "source_id": source, "credential_expires_at": expiry})
    return spec


class PreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.spec = fixture(self.base)
        self.rpc = ["https://rpc.example.invalid"]
        self.agent = "test/agent@sha256:" + "a" * 64

    def test_preserved_identity_isolation_and_staged_start(self):
        output = module.prepare(self.base, self.spec, self.rpc, self.base / "deployment", self.agent)
        compose = json.loads((output / "compose.yaml").read_text())
        self.assertEqual(len(compose["services"]), 8)
        controller = compose["services"][module.C]
        self.assertIn("open-pool-us-controller-data:/data", controller["volumes"])
        self.assertTrue(all(v["external"] for v in compose["volumes"].values()))
        defaults = {n for n, s in compose["services"].items() if not s.get("profiles")}
        self.assertEqual(defaults, {module.C, module.O, "us-observer"})
        for name, service in compose["services"].items():
            self.assertNotIn("ports", service)
            self.assertNotIn("privileged", service)
            self.assertEqual(service["user"], "65532:65532")
            own = output / "secrets" / name
            self.assertEqual((own / "payout-keystore.json").exists(), name == module.E)
            self.assertEqual((own / "receiver-keystore.json").exists(), name == module.B + "-receiver")
            self.assertEqual((own / "portal-issuer.json").exists(), name == "member-portal")
            self.assertEqual((own / "tls.key").exists(), name == module.O)
        with self.assertRaises(ValueError):
            module.prepare(self.base, self.spec, self.rpc, output, self.agent)

    def test_wrong_wallet_pool_and_source_are_rejected_before_output(self):
        for key in ("pool_id", "receiver_wallet", "payout_wallet"):
            changed = dict(self.spec)
            changed[key] = "pool_" + "a" * 32 if key == "pool_id" else "0x" + "a" * 40
            with self.subTest(key=key), self.assertRaises(ValueError):
                module.prepare(self.base, changed, self.rpc, self.base / "bad", self.agent)
            self.assertFalse((self.base / "bad").exists())
        (self.base / "config/receiver-domain-id").write_text("0x" + "a" * 64)
        with self.assertRaises(ValueError):
            module.validate(self.base, self.spec, self.rpc)

    def test_credential_tampering_rejected(self):
        path = self.base / "secrets/service-credentials" / module.C / "service-auth.json"
        original = json.loads(path.read_text())
        for key, value in (("roles", ["pool-admin", "reconciler"]), ("token_sha256", "0" * 64),
                           ("expires_at", "2020-01-01T00:00:00Z")):
            altered = json.loads(json.dumps(original))
            altered["credentials"][0][key] = value
            path.write_text(json.dumps(altered))
            with self.subTest(key=key), self.assertRaises(ValueError):
                module.validate(self.base, self.spec, self.rpc)


if __name__ == "__main__":
    unittest.main()
