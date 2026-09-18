import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('orchestrator_setup', Path(__file__).resolve().parents[1] / 'setup.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class InputTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.input = self.root / 'input'
        self.input.mkdir()
        self.output = self.root / 'output'
        self.output.mkdir()
        self.env = {'SETUP_INPUT': str(self.input), 'SETUP_OUTPUT': str(self.output),
                    'BROKER_URL': 'https://broker.example.invalid', 'ORCHESTRATOR_ADDRESS': '0x' + '1' * 40}
        (self.input / 'rpc-urls').write_text('https://rpc.example.invalid/private-token\n')
        (self.input / 'offers.json').write_text('[{"offering_id":"example"}]')
        self.images = {k: 'example/' + k + '@sha256:' + 'a' * 64 for k in ('capability-broker', 'payment-daemon')}
        (self.input / 'images.json').write_text(json.dumps(self.images))

    def test_inputs_do_not_write(self):
        module.Setup(self.env)
        self.assertEqual(list(self.output.iterdir()), [])

    def test_invalid_origins(self):
        for url in ('http://broker.example', 'https://user:secret@broker.example', 'https://broker.example/path',
                    'https://broker.example?token=secret', 'https://broker.example:443'):
            with self.subTest(url=url), self.assertRaises(module.SetupError):
                module.Setup(dict(self.env, BROKER_URL=url))

    def test_image_tags_rejected(self):
        self.images['capability-broker'] = 'example/broker:latest'
        (self.input / 'images.json').write_text(json.dumps(self.images))
        with self.assertRaisesRegex(module.SetupError, 'immutable'):
            module.Setup(self.env)

    def test_input_symlink_rejected(self):
        (self.input / 'offers.json').unlink()
        outside = self.root / 'outside'
        outside.write_text('[]')
        (self.input / 'offers.json').symlink_to(outside)
        with self.assertRaises(module.SetupError):
            module.Setup(self.env)

    def test_overlapping_directories_rejected(self):
        with self.assertRaisesRegex(module.SetupError, 'separate'):
            module.Setup(dict(self.env, SETUP_OUTPUT=str(self.input / 'output')))

    def test_fixture_extension_and_url_refs(self):
        fixture = self.input / 'fixtures/audio/probe.wav'
        fixture.parent.mkdir(parents=True)
        fixture.write_bytes(b'synthetic')
        (self.input / 'offers.json').write_text(json.dumps([{'fixture': {'ref': 'audio/probe'},
                                                          'url': '{{fixture_url.audio/probe}}'}]))
        module.Setup(self.env)
        fixture.unlink()
        with self.assertRaisesRegex(module.SetupError, 'fixture'):
            module.Setup(self.env)

    def test_exception_does_not_disclose_rpc(self):
        (self.input / 'rpc-urls').write_text('http://rpc.example.invalid/SECRET')
        with self.assertRaises(module.SetupError) as context:
            module.Setup(self.env)
        self.assertNotIn('SECRET', str(context.exception))


if __name__ == '__main__':
    os.umask(0o077)
    unittest.main()
