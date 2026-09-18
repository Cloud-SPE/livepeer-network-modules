#!/usr/bin/env python3
"""Generate a standalone broker package offline; never start services."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit


class SetupError(ValueError):
    """An error whose message is safe to show to the operator."""


def require(condition, message):
    if not condition:
        raise SetupError(message)


def read(path):
    require(path.is_file() and not path.is_symlink(), 'Missing or unsafe input: ' + path.name)
    return path.read_bytes()


def encode(value):
    if isinstance(value, bytes):
        return value
    return (json.dumps(value, indent=2, sort_keys=True) + '\n').encode()


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    require(not path.is_symlink(), 'Refusing symlink output')
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
        stream.write(encode(value))
        stream.flush()
        os.fsync(stream.fileno())
        temporary = stream.name
    os.replace(temporary, path)


def digest(path):
    return hashlib.sha256(read(path)).hexdigest()


def command(args):
    result = subprocess.run(list(map(str, args)), capture_output=True, timeout=180)
    # Parsers can echo configuration, including provider credentials. Do not log it.
    require(result.returncode == 0, Path(args[0]).name + ' failed; private tool output suppressed')
    return result.stdout.decode()


def address(value):
    require(isinstance(value, str) and re.fullmatch(r'0x[0-9a-fA-F]{40}', value)
            and int(value, 16), 'Expected a nonzero Ethereum address')
    return value.lower()


def origin(value):
    url = urlsplit(value)
    require(url.scheme == 'https' and url.hostname and url.netloc == url.hostname
            and not url.path and not url.query and not url.fragment
            and re.fullmatch(r'[a-z0-9.-]+', url.hostname), 'Broker URLs must be canonical HTTPS origins')
    return value


class Setup:
    def __init__(self, env=None):
        self.env = dict(os.environ if env is None else env)
        self.input = Path(self.env.get('SETUP_INPUT', '/input')).resolve()
        self.output = Path(self.env.get('SETUP_OUTPUT', '/output')).resolve()
        require(not self.output.is_relative_to(self.input) and not self.input.is_relative_to(self.output),
                'Input and output directories must be separate')
        self.bin = Path(self.env.get('SETUP_BIN', '/usr/local/bin'))
        self.state = self.output / '.setup'
        self.material = self.state / 'material'
        self.name = self.env.get('DEPLOYMENT_ID', 'broker-a')
        require(re.fullmatch(r'[a-z][a-z0-9-]{0,62}', self.name), 'Invalid DEPLOYMENT_ID')
        self.orch = address(self.env.get('ORCHESTRATOR_ADDRESS', ''))
        self.url = origin(self.env.get('BROKER_URL', ''))
        self.admin_url = origin(self.env.get('BROKER_ADMIN_URL', self.url))
        self.mode = self.env.get('MATERIAL_MODE', 'create-if-missing')
        require(self.mode in ('reuse', 'create-if-missing'), 'Invalid MATERIAL_MODE')
        self.rpc = [line.strip() for line in read(self.source('rpc-urls')).decode().splitlines() if line.strip()]
        require(self.rpc and all(urlsplit(u).scheme == 'https' and urlsplit(u).hostname
                and not urlsplit(u).username and not urlsplit(u).fragment and ',' not in u for u in self.rpc),
                'rpc-urls must contain one HTTPS RPC endpoint per line')
        self.offers = json.loads(read(self.source('offers.json')))
        require(isinstance(self.offers, list) and self.offers, 'offers.json must be a nonempty array of broker offers')
        self.images = json.loads(read(self.source('images.json')))
        require(isinstance(self.images, dict) and set(self.images) == {'capability-broker', 'payment-daemon'},
                'images.json must contain capability-broker and payment-daemon')
        require(all(isinstance(v, str) and re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64}', v)
                    for v in self.images.values()), 'Image references must use immutable sha256 digests')
        self.fixtures = {}
        fixtures = self.input / 'fixtures'
        if fixtures.exists():
            require(not fixtures.is_symlink(), 'Fixture directory cannot be a symlink')
            for path in fixtures.rglob('*'):
                require(not path.is_symlink(), 'Fixture symlinks are not supported')
                if path.is_file():
                    self.fixtures[str(path.relative_to(fixtures))] = read(path)
        self.check_fixture_refs(self.offers)

    def source(self, name):
        path = self.input / name
        require(path.resolve().is_relative_to(self.input), 'Input path escapes input directory')
        return path

    def tool(self, name, *args):
        return command([self.bin / name, *args])

    def check_fixture_refs(self, value):
        if isinstance(value, dict):
            fixture = value.get('fixture')
            if isinstance(fixture, dict) and 'ref' in fixture:
                ref = fixture['ref']
                require(isinstance(ref, str) and (ref in self.fixtures or
                        any(name.startswith(ref + '.') for name in self.fixtures)), 'Missing certification fixture')
            for child in value.values():
                self.check_fixture_refs(child)
        elif isinstance(value, str):
            for ref in re.findall(r'\{\{fixture_url\.([^}]+)\}\}', value):
                require(ref in self.fixtures or any(name.startswith(ref + '.') for name in self.fixtures),
                        'Missing URL certification fixture')
        elif isinstance(value, list):
            for child in value:
                self.check_fixture_refs(child)

    def preflight(self):
        require(self.output.is_dir() and os.access(self.output, os.W_OK), 'Create a writable output directory first')
        require(shutil.disk_usage(self.output).free > sum(map(len, self.fixtures.values())) * 2 + 32 * 1024**2,
                'Insufficient output disk space')
        for name in ('livepeer-capability-broker', 'secure-orch-keygen', 'docker'):
            require(os.access(self.bin / name, os.X_OK), 'Missing setup tool: ' + name)
        self.tool('docker', 'compose', 'version')
        return {'output_storage': 'passed', 'embedded_tools': 'passed', 'network_checks': 'not performed',
                'host_docker_gpu_ports': 'not checked from isolated setup container'}

    def plan(self):
        checks = self.preflight()
        return {'deployment_id': self.name, 'orchestrator_address': self.orch, 'broker_url': self.url,
                'offers': len(self.offers), 'material_mode': self.mode, 'checks': checks,
                'writes': False, 'services_started': False,
                'material': {name: ('reuse' if (self.material / name).exists() or self.source(name).exists()
                                    else 'missing' if self.mode == 'reuse' else 'create') for name in MATERIAL}}

    def provision(self):
        self.material.mkdir(exist_ok=True, mode=0o700)
        for name in MATERIAL:
            source, target = self.source(name), self.material / name
            if source.exists():
                data = read(source)
                if target.exists():
                    require(read(target) == data, 'Imported material conflicts with journal: ' + name)
                else:
                    save(target, data)
        key, password = self.material / 'receiver-keystore.json', self.material / 'receiver-password'
        if not key.exists():
            require(self.mode == 'create-if-missing', 'Receiver wallet missing in reuse mode')
            if not password.exists():
                save(password, (os.urandom(32).hex() + '\n').encode())
            # Publish only a complete keygen result. An interruption preserves the password.
            with tempfile.TemporaryDirectory(dir=self.state) as tmp:
                generated = Path(tmp) / 'wallet.json'
                self.tool('secure-orch-keygen', '--out', generated, '--password-file', password)
                save(key, read(generated))
        require(password.exists() and read(password).strip(), 'Receiver password missing; refusing regeneration')
        wallet = json.loads(read(key))
        require(wallet.get('version') == 3 and ('crypto' in wallet or 'Crypto' in wallet), 'Expected encrypted V3 wallet')
        receiver = address('0x' + wallet['address'].removeprefix('0x'))
        require(receiver != self.orch, 'Cold orchestrator wallet must not be used as the receiver wallet')
        for name in MATERIAL[2:]:
            target = self.material / name
            if not target.exists():
                require(self.mode == 'create-if-missing', 'Material missing in reuse mode: ' + name)
                if name == 'broker-settlement.key':
                    with tempfile.TemporaryDirectory(dir=self.state) as tmp:
                        generated = Path(tmp) / 'key'
                        self.tool('livepeer-capability-broker', 'settlement-key', 'generate', '--out', generated)
                        save(target, read(generated))
                else:
                    save(target, (os.urandom(32).hex() + '\n').encode())
            require(re.fullmatch(rb'[0-9a-f]{64}', read(target).strip()), 'Invalid material: ' + name)
        self.tool('livepeer-capability-broker', 'settlement-key', 'pubkey', '--file', self.material / 'broker-settlement.key')
        return receiver

    def render(self, package):
        broker_secret, receiver_secret = package / 'secrets/broker', package / 'secrets/receiver'
        for name in MATERIAL:
            target = receiver_secret if name.startswith('receiver-') else broker_secret
            save(target / name, read(self.material / name))
        # Broker supports env:// bearer refs. env_file keeps secrets out of compose.yaml.
        token = read(self.material / 'broker-admin.token').decode().strip()
        save(broker_secret / 'admin.env', ('BROKER_ADMIN_TOKEN=' + token + '\n').encode())
        save(package / 'operator/broker-admin.token', read(self.material / 'broker-admin.token'))
        fixtures = package / 'fixtures'
        fixtures.mkdir(mode=0o700)
        for name, data in self.fixtures.items():
            save(fixtures / name, data)
        config = {
            'identity': {'orch_eth_address': self.orch, 'label': self.name,
                         'settlement_key_file': '/secrets/broker-settlement.key'},
            'external_base_url': self.url, 'listen': {'paid': ':8080', 'metrics': ':9090'},
            'admin_auth': {'method': 'bearer', 'secret_ref': 'env://BROKER_ADMIN_TOKEN'},
            'payment_daemon': {'socket': '/run/livepeer/payment.sock'},
            'credential_store': {'path': '/data/credentials.db', 'sealing_key_file': '/secrets/broker-sealing.key'},
            'session_store': {'path': '/data/sessions.db', 'sealing_key_file': '/secrets/broker-sealing.key', 'job_retention': '96h'},
            'offers_state_path': '/data/offers.json', 'offers_source': 'file', 'offers': self.offers,
            'certification_fixtures_dir': '/fixtures', 'accounting_store_path': '/data/work-accounting.db'}
        save(package / 'config/host-config.yaml', config)
        services = {}
        for name, image in [('broker', 'capability-broker'), ('receiver', 'payment-daemon')]:
            services[name] = {'image': self.images[image], 'user': '65532:65532', 'restart': 'unless-stopped',
                'read_only': True, 'security_opt': ['no-new-privileges:true'], 'cap_drop': ['ALL'],
                'tmpfs': ['/tmp:uid=65532,gid=65532,mode=0700'],
                'volumes': [name + '-data:/data', 'payment-socket:/run/livepeer',
                            {'type': 'bind', 'source': './secrets/' + name, 'target': '/secrets',
                             'read_only': True, 'bind': {'create_host_path': False}}]}
        services['broker'].update({'command': ['--config=/etc/livepeer/host-config.yaml'],
                                  'env_file': ['./secrets/broker/admin.env'],
                                  'ports': ['127.0.0.1:8080:8080', '127.0.0.1:9090:9090'],
                                  'depends_on': ['receiver']})
        for source, target in [('./config/host-config.yaml', '/etc/livepeer/host-config.yaml'), ('./fixtures', '/fixtures')]:
            services['broker']['volumes'].append({'type': 'bind', 'source': source, 'target': target,
                                                'read_only': True, 'bind': {'create_host_path': False}})
        # Compose interpolates dollar signs even inside JSON strings.
        rpc = ','.join(self.rpc).replace('$', '$$')
        services['receiver']['command'] = ['--mode=receiver', '--socket=/run/livepeer/payment.sock',
            '--db=/data/receiver.db', '--txintent-db=/data/txintents.db', '--chain-rpc-urls=' + rpc,
            '--keystore-path=/secrets/receiver-keystore.json', '--keystore-password-file=/secrets/receiver-password',
            '--orch-address=' + self.orch]
        volumes = {name: {'external': True, 'name': self.name + '-' + name}
                   for name in ('broker-data', 'receiver-data', 'payment-socket')}
        save(package / 'compose.yaml', {'name': self.name, 'services': services, 'volumes': volumes})
        save(package / 'operator/coordinator-broker.json', {'name': self.name, 'base_url': self.admin_url,
             'admin_token_ref': 'file:///run/secrets/' + self.name + '-admin.token'})
        save(package / 'operator/nginx.conf', (
            '# Install in a HOST nginx HTTPS server block with your TLS certificate.\n'
            '# Public traffic and WebSocket attach only; administer through a private path.\n'
            'location = /admin { return 404; }\n'
            'location ^~ /admin/ { return 404; }\n'
            'location / {\n'
            '    proxy_pass http://127.0.0.1:8080;\n'
            '    proxy_http_version 1.1;\n'
            '    proxy_set_header Host $host;\n'
            '    proxy_set_header Upgrade $http_upgrade;\n'
            '    proxy_set_header Connection "upgrade";\n'
            '    proxy_buffering off;\n'
            '    proxy_read_timeout 3600s;\n'
            '    client_max_body_size 0;\n'
            '}\n').encode())
        save(package / 'operator/README.txt', HANDOFF.encode())
        return volumes

    def validate(self, package):
        require(package.resolve().is_relative_to(self.output / 'revisions'), 'Invalid revision path')
        require(not package.is_symlink() and not any(p.is_symlink() for p in package.rglob('*')),
                'Package symlinks are not supported')
        manifest = json.loads(read(package / 'deployment-manifest.json'))
        actual = {str(p.relative_to(package)) for p in package.rglob('*') if p.is_file()}
        require(actual == set(manifest['files']) | {'deployment-manifest.json', 'validation-report.json'}
                or actual == set(manifest['files']) | {'deployment-manifest.json'}, 'Package file inventory changed')
        for name, expected in manifest['files'].items():
            path = package / name
            require(path.resolve().is_relative_to(package.resolve()), 'Unsafe package path')
            require(digest(path) == expected, 'Package integrity check failed')
            require(path.stat().st_mode & 0o077 == 0, 'Package file permissions too broad')
        self.tool('livepeer-capability-broker', 'config', 'validate', '--config', package / 'config/host-config.yaml')
        self.tool('docker', 'compose', '-f', package / 'compose.yaml', 'config', '--quiet')
        report = {'passed': ['package integrity', 'private file permissions', 'broker config parser', 'Compose parser'],
                  'not_checked': ['payment wallet unlocking', 'payment runtime startup', 'host volume ownership',
                                  'DNS/TLS', 'RPC and chain state', 'funding and redemption authority',
                                  'GPU and runners', 'certification', 'coordinator and manifest publication'],
                  'network_used': False, 'services_started': False}
        save(package / 'validation-report.json', report)
        return report

    def generate(self):
        self.preflight()
        self.state.mkdir(mode=0o700, exist_ok=True)
        # Reject symlinked journal/package paths before any writes.
        require(not any(p.is_symlink() for p in self.output.rglob('*')), 'Output symlinks are not supported')
        with (self.state / 'lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            binding = {'deployment_id': self.name, 'orchestrator_address': self.orch}
            path = self.state / 'binding.json'
            if path.exists():
                require(json.loads(read(path)) == binding, 'Setup journal identity mismatch')
            else:
                save(path, binding)
            index = self.state / 'material-index.json'
            if index.exists():
                expected = json.loads(read(index))
                require(set(expected) == set(MATERIAL), 'Invalid material journal index')
                require(all((self.material / name).is_file() and digest(self.material / name) == expected[name]
                            for name in MATERIAL), 'Journaled material missing or changed; restore backup before proceeding')
            receiver = self.provision()
            material = {name: digest(self.material / name) for name in MATERIAL}
            save(index, material)
            inputs = [binding, self.url, self.admin_url, self.rpc, self.offers, self.images, material,
                      {k: hashlib.sha256(v).hexdigest() for k, v in self.fixtures.items()},
                      digest(Path(__file__)), digest(self.bin / 'livepeer-capability-broker')]
            fingerprint = hashlib.sha256(encode(inputs)).hexdigest()
            revisions = self.output / 'revisions'
            revisions.mkdir(mode=0o700, exist_ok=True)
            package = revisions / fingerprint
            if not package.exists():
                staging = Path(tempfile.mkdtemp(prefix='.pending-', dir=revisions))
                volumes = self.render(staging)
                files = {str(p.relative_to(staging)): digest(p) for p in staging.rglob('*') if p.is_file()}
                save(staging / 'deployment-manifest.json', {'version': 1, 'fingerprint': fingerprint,
                     **binding, 'broker_url': self.url, 'receiver_address': receiver, 'images': self.images,
                     'files': files, 'volumes': volumes, 'broker_validator_sha256': inputs[-1],
                     'status': 'offline generated; not deployed'})
                self.validate(staging)
                for folder in ('config', 'secrets', 'fixtures'):
                    for p in (staging / folder).rglob('*'):
                        os.chown(p, 65532, 65532)
                    os.chown(staging / folder, 65532, 65532)
                os.rename(staging, package)
            else:
                self.validate(package)
            save(self.output / 'current-revision', (str(package.relative_to(self.output)) + '\n').encode())
            return {'package': str(package.relative_to(self.output)), 'receiver_address': receiver,
                    'services_started': False}


MATERIAL = ('receiver-keystore.json', 'receiver-password', 'broker-settlement.key',
            'broker-sealing.key', 'broker-admin.token')
HANDOFF = '''This package is generated and validated offline. It has not been deployed.
Create the three external volumes in deployment-manifest.json and prepare their
roots for UID/GID 65532 before starting Compose. Preserve these volumes on reruns.
Back up .setup, the selected revision, and coordinated broker/payment state.
The receiver wallet is HOT. Fund it and establish redemption authority separately.
No cold orchestrator private key belongs on this server.
The broker is bound to host loopback 8080; metrics use loopback 9090. The nginx
snippet is for a host HTTPS reverse proxy. Supply DNS and TLS separately. Runners
use the WebSocket attach transport; QUIC is not enabled in this initial profile.
Merge coordinator-broker.json into the existing coordinator roster; do not replace
it. Install broker-admin.token at its referenced path on the coordinator through
a private transfer. Its admin connection must use a private route that permits
/admin/ (the public nginx snippet denies it); use the appropriate reachable URL
in the coordinator entry. This tool does not configure that route or coordinator.
Enroll runners and certify offers before signing and publishing a manifest.
The setup image's broker parser must match the selected broker runtime release.
Digest syntax checks do not prove that arbitrary supplied images match the parser.
Inspect validation-report.json for the exact remaining live checks.
'''


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('plan', 'generate', 'validate'))
    args = parser.parse_args()
    setup = Setup()
    if args.command == 'validate':
        revision = read(setup.output / 'current-revision').decode().strip()
        result = setup.validate(setup.output / revision)
    else:
        result = getattr(setup, args.command)()
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    try:
        main()
    except SetupError as exc:
        print('Setup failed: ' + str(exc), file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError):
        # Arbitrary parser/input exceptions may contain secrets. No traceback.
        print('Setup failed: invalid input, conflicting material, or validation failure. '
              'Check private inputs and the documented requirements; output was not activated.', file=sys.stderr)
        sys.exit(1)
