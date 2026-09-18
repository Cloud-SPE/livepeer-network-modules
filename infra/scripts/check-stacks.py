#!/usr/bin/env python3
"""Render every active Compose entry point without starting containers.
Uses Docker Compose and synthetic inputs; never loads deployment .env files.
"""
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
FILES = sorted(p for p in ROOT.rglob('*compose*.y*ml') if
               not any(x in p.parts for x in ('.git', 'node_modules', 'archive', 'testdata'))
               and p.name in ('compose.yaml', 'docker-compose.yml', 'docker-compose.yaml'))


def inputs(files):
    env = {k: v for k, v in os.environ.items() if k in ('PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG')}
    for p in files:
        example = p.parent / '.env.example'
        if not example.exists():
            example = p.parent / 'stack.env.example'
        if example.exists():
            for line in example.read_text().splitlines():
                line = line.strip()
                if line and not line.startswith('#') and '=' in line:
                    k, v = line.split('=', 1)
                    env[k] = v.strip('"\'')
    for p in files:
        for name in re.findall(r'(?<!\$)\$\{([A-Z][A-Z0-9_]*)(?::\?[^}]*|\?[^}]*|)\}', p.read_text()):
            if not env.get(name):
                env[name] = 'stack-check-placeholder'
    # Values here are public test data only, not deployment credentials.
    env.update(REGISTRY='tztcloud', TAG='v2.0.0', CHAIN_RPC_URLS='https://rpc.example.invalid',
               ORCH_ADDRESS='0x'+'12'*20, ORCH_ETH_ADDRESS='0x'+'12'*20,
               BROKER_EXTERNAL_BASE_URL='https://broker.example.invalid',
               LIVEPEER_RUNNERS_JSON='[]', MAX_PAYMENT_WEI='1000')
    for k in list(env):
        if k.endswith('_IMAGE'):
            component = {'PAYMENT_IMAGE': 'payment-daemon', 'BROKER_IMAGE': 'capability-broker',
                         'RUNNER_IMAGE': 'conformance'}.get(k, 'conformance')
            env[k] = 'tztcloud/livepeer-' + component + '@sha256:' + '1'*64
    return env


def validate(files):
    args = ['docker', 'compose', '--project-name', 'stack-check', '--env-file', '/dev/null']
    for p in files:
        args += ['-f', str(p)]
    result = subprocess.run(args + ['config', '--format', 'json', '--no-env-resolution'],
                            cwd=files[0].parent, env=inputs(files), capture_output=True, text=True)
    label = ' + '.join(str(p.relative_to(ROOT)) for p in files)
    if result.returncode:
        raise AssertionError(label + ': ' + result.stderr)
    model = json.loads(result.stdout)
    ports = {}
    for name, svc in model.get('services', {}).items():
        image = svc.get('image', '')
        if image.startswith('tztcloud/livepeer-') and '@sha256:' not in image:
            assert image.endswith(':v2.0.0'), (label, name, image)
        command = svc.get('command') or []
        cli_dir = os.environ.get('STACK_CLI_DIR')
        if cli_dir and image.startswith('tztcloud/livepeer-') and not svc.get('entrypoint'):
            component = image.split('livepeer-', 1)[1].split(':', 1)[0].split('@', 1)[0]
            binary = Path(cli_dir) / component
            if binary.exists():
                # Parse the actual rendered flags, then stop at help before
                # opening stores, keys, listeners, or chain connections.
                result = subprocess.run([str(binary)] + command + ['--help'],
                                        capture_output=True, text=True, timeout=10,
                                        env={'PATH': os.environ['PATH']})
                output = result.stdout + result.stderr
                assert 'flag provided but not defined' not in output and 'unknown flag' not in output, (label, name, output)
                assert 'help requested' in output or 'Usage' in output or 'usage:' in output, (label, name, 'did not stop at help', output)

        if any(x == '--mode=sender' for x in command):
            db = next((x.split('=', 1)[1] for x in command if x.startswith('--db=')),
                      '/var/lib/livepeer/payment-daemon/sessions.db')
            assert any(db.startswith(v['target'].rstrip('/') + '/') and not v.get('read_only')
                       for v in svc.get('volumes', [])), (label, name, 'sender ledger is not durable')
        for port in svc.get('ports', []):
            key = (port.get('published'), port.get('protocol', 'tcp'))
            # Different profiles may intentionally occupy the same host port.
            if key in ports and not svc.get('profiles'):
                old_name, old_svc, old_ip = ports[key]
                ip = port.get('host_ip', '0.0.0.0')
                assert old_svc.get('profiles') or ip != old_ip and '0.0.0.0' not in (ip, old_ip), (label, name, old_name, key)
            ports[key] = (name, svc, port.get('host_ip', '0.0.0.0'))
    print('ok', label)


def main():
    count = 0
    for base in FILES:
        # Config examples happen to contain "compose" in their name; these
        # exact entry-point filenames are the deployable Compose models.
        validate([base]); count += 1
        if base.name == 'docker-compose.yml':
            for overlay in sorted(base.parent.glob('docker-compose.*.yml')):
                # dev/scenario files define standalone services, not overrides.
                validate([overlay] if overlay.name in ('docker-compose.dev.yml', 'docker-compose.scenario.yml') else [base, overlay])
                count += 1
    print(f'{count} Compose configurations validated (no containers started)')

if __name__ == '__main__':
    main()
