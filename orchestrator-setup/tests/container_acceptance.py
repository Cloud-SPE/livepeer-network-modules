#!/usr/bin/env python3
"""Synthetic acceptance harness. Only this harness accesses Docker, never setup."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def main(image):
    with tempfile.TemporaryDirectory(prefix='orchestrator-setup-acceptance-') as tmp:
        root = Path(tmp)
        incoming, output = root / 'input', root / 'output'
        incoming.mkdir(); output.mkdir()
        (incoming / 'rpc-urls').write_text('https://rpc.example.invalid/SECRET$TOKEN\n')
        shutil.copyfile(Path(__file__).resolve().parents[1] / 'examples/offers.json', incoming / 'offers.json')
        (incoming / 'images.json').write_text(json.dumps({k: 'synthetic/' + k + '@sha256:' + 'a' * 64
                                                        for k in ('capability-broker', 'payment-daemon')}))
        env = {'ORCHESTRATOR_ADDRESS': '0x' + '1' * 40, 'BROKER_URL': 'https://broker.example.invalid',
               'DEPLOYMENT_ID': 'acceptance-broker'}

        def execute(action, settings=None, destination=output, source=incoming, failure=False):
            args = ['docker', 'run', '--rm', '--network=none', '--read-only', '--tmpfs', '/tmp']
            for key, value in (settings or env).items():
                args += ['-e', key + '=' + value]
            args += ['--mount', f'type=bind,src={source},dst=/input,readonly',
                     '--mount', f'type=bind,src={destination},dst=/output', image, action]
            result = subprocess.run(args, capture_output=True, text=True)
            assert (result.returncode != 0) == failure, result.stdout + result.stderr
            assert 'SECRET' not in result.stdout + result.stderr, 'RPC credential leaked'
            return result

        def snapshot():
            return {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (output / '.setup/material').iterdir()}

        def package():
            return output / (output / 'current-revision').read_text().strip()

        execute('plan')
        assert not list(output.iterdir())
        execute('generate')
        first_revision = (output / 'current-revision').read_text()
        material = snapshot()
        execute('generate'); execute('validate')
        assert first_revision == (output / 'current-revision').read_text()
        assert material == snapshot()
        print('PASS actual keygen, broker and Compose parsers, offline rerun stability', flush=True)

        composed = subprocess.check_output(['docker', 'compose', '-f', str(package() / 'compose.yaml'),
                                            'config', '--format', 'json'], text=True)
        compose = json.loads(composed)
        # Compose's reusable config output preserves its $$ escape for literal $.
        assert '--chain-rpc-urls=https://rpc.example.invalid/SECRET$$TOKEN' in compose['services']['receiver']['command']
        assert all(v['external'] for v in compose['volumes'].values())
        assert compose['services']['broker']['ports'][0]['host_ip'] == '127.0.0.1'
        for p in (package() / 'secrets').rglob('*'):
            assert p.stat().st_uid == 65532
            if p.is_file():
                assert p.stat().st_mode & 0o077 == 0
        print('PASS runtime secret ownership, private ports, external volumes, escaped RPC dollar sign', flush=True)

        imported = root / 'imported'
        shutil.copytree(incoming, imported)
        for p in (output / '.setup/material').iterdir():
            shutil.copyfile(p, imported / p.name)
        reused = root / 'reused'; reused.mkdir()
        execute('generate', settings=dict(env, MATERIAL_MODE='reuse'), destination=reused, source=imported)
        assert material == {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (reused / '.setup/material').iterdir()}
        print('PASS imported material reused byte-for-byte', flush=True)

        execute('generate', settings=dict(env, BROKER_URL='https://new-broker.example.invalid'))
        assert first_revision != (output / 'current-revision').read_text()
        assert material == snapshot()
        active = (output / 'current-revision').read_text()
        execute('generate', settings=dict(env, ORCHESTRATOR_ADDRESS='0x' + '2' * 40), failure=True)
        original_offers = (incoming / 'offers.json').read_bytes()
        (incoming / 'offers.json').write_text('[{"capability":"invalid"}]')
        execute('generate', failure=True)
        assert active == (output / 'current-revision').read_text()
        assert material == snapshot()
        (incoming / 'offers.json').write_bytes(original_offers)
        print('PASS new revision preserves keys; identity conflict and failed parser preserve active revision', flush=True)

        # Simulate interruption after creating the wallet password, before its key.
        resumed = root / 'resumed'; (resumed / '.setup/material').mkdir(parents=True)
        password = resumed / '.setup/material/receiver-password'
        password.write_text('interrupted-password-at-least-12-chars\n')
        before = password.read_bytes()
        execute('generate', destination=resumed)
        assert password.read_bytes() == before
        key = resumed / '.setup/material/receiver-keystore.json'
        key.write_text('{broken')
        execute('generate', destination=resumed, failure=True)
        assert key.read_text() == '{broken'
        print('PASS interrupted wallet creation resumes and corrupt wallet is preserved for diagnosis', flush=True)

        (imported / 'broker-admin.token').write_text('f' * 64 + '\n')
        execute('generate', source=imported, failure=True)
        assert material == snapshot()
        (package() / 'config/host-config.yaml').write_text('{}')
        execute('validate', failure=True)
        print('PASS imported key conflict and package tamper rejection', flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', default='livepeer-orchestrator-setup:local')
    main(parser.parse_args().image)
