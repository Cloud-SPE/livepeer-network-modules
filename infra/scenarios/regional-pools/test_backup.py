import copy
import datetime as dt
import hashlib
import io
import json
from pathlib import Path
import shutil
import tarfile
import tempfile
import unittest

from backup import seal, verify
from generate import Compiler
from test_generate import fixture


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        generated = Compiler(fixture(self.root / "material"), self.root / "generated").compile()
        self.expected = generated / "manifest.json"
        self.manifest = json.loads(self.expected.read_text())
        self.stage = self.root / "stage"
        self.stage.mkdir()
        for item in self.manifest["durable_volumes"]:
            folder = self.stage / "volumes" / item["volume"]
            folder.mkdir(parents=True)
            (folder / "durable-state.fixture").write_bytes(b"synthetic archive-byte fixture\0" + item["service"].encode())
        for host in self.manifest["topology"]["hosts"]:
            shutil.copytree(generated / host, self.stage / "hosts" / host)
        shutil.copytree(generated / "operator", self.stage / "operator")
        shutil.copyfile(self.expected, self.stage / "deployment-manifest.json")
        self.fence = {"incident_id": "local-test", "operator": "test-operator", "fenced_at": dt.datetime.now(dt.timezone.utc).isoformat(), "fenced_hosts": list(self.manifest["topology"]["hosts"]), "evidence": "synthetic local fixture; no running services"}
        (self.stage / "fence.json").write_text(json.dumps(self.fence))

    def archive(self):
        path = self.root / "backup.tar"
        with tarfile.open(path, "w") as archive:
            archive.add(self.stage, arcname=".")
        return path, hashlib.sha256(path.read_bytes()).hexdigest()

    def test_complete_archive_and_identity_mismatch(self):
        index = seal(self.stage, self.expected)
        path, digest = self.archive()
        restored = verify(path, self.expected, digest)
        self.assertEqual(restored, index)
        for mutate in (
            lambda m: m["topology"]["regions"]["eu"].update(pool_id="pool_" + "f" * 32),
            lambda m: m["topology"]["regions"]["us"].update(payout_wallet="0x" + "f" * 40),
            lambda m: m["topology"]["brokers"]["audio-broker"].update(source_id="0x" + "f" * 64),
            lambda m: m["topology"].update(deployment_id="new-empty-books"),
        ):
            wrong = copy.deepcopy(self.manifest)
            mutate(wrong)
            expected = self.root / "wrong.json"
            expected.write_text(json.dumps(wrong))
            with self.assertRaisesRegex(ValueError, "identity mismatch"):
                verify(path, expected, digest)
        with self.assertRaisesRegex(ValueError, "checksum"):
            verify(path, self.expected, "0" * 64)

    def test_changed_file_and_unsafe_archive_refused(self):
        seal(self.stage, self.expected)
        file = next((self.stage / "volumes").rglob("*.fixture"))
        file.write_text("changed after sealing")
        path, digest = self.archive()
        with self.assertRaisesRegex(ValueError, "changed archived file"):
            verify(path, self.expected, digest)
        with tarfile.open(path, "w") as archive:
            item = tarfile.TarInfo("../../outside")
            item.size = 1
            archive.addfile(item, io.BytesIO(b"x"))
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        with self.assertRaisesRegex(ValueError, "unsafe archive path"):
            verify(path, self.expected, digest)

    def test_missing_scope_material_and_links_refused(self):
        self.fence["fenced_hosts"].pop()
        (self.stage / "fence.json").write_text(json.dumps(self.fence))
        with self.assertRaisesRegex(ValueError, "every writer host"):
            seal(self.stage, self.expected)
        self.fence["fenced_hosts"] = list(self.manifest["topology"]["hosts"])
        (self.stage / "fence.json").write_text(json.dumps(self.fence))
        volume = self.stage / "volumes" / self.manifest["durable_volumes"][0]["volume"]
        shutil.rmtree(volume)
        with self.assertRaisesRegex(ValueError, "missing durable volume"):
            seal(self.stage, self.expected)
        volume.mkdir()
        (volume / "outside-link").symlink_to("/etc/passwd")
        with self.assertRaisesRegex(ValueError, "symlinks"):
            seal(self.stage, self.expected)


if __name__ == "__main__":
    unittest.main()
