#!/usr/bin/env python3
"""Offline regional provisioning. No Docker daemon or transaction execution."""
import argparse
import base64
import datetime as dt
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

from render import render, write, links, token_name

MODES = ('reuse', 'create-if-missing')
COMPONENTS = ('pool-controller', 'capability-broker', 'payment-daemon', 'protocol-daemon',
              'pool-reconciler', 'pool-payout-executor', 'member-portal', 'pool-member-agent')


def need(ok, message):
    if not ok:
        raise ValueError(message)


def run(args, **kwargs):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180, **kwargs)
    need(result.returncode == 0, f'{Path(args[0]).name} failed (exit {result.returncode}); no private command output logged')
    return result.stdout.decode()


def load(path):
    need(path.is_file() and not path.is_symlink(), f'Missing or unsafe file: {path.name}')
    need(path.stat().st_size < 4*1024*1024, 'Input too large')
    return path.read_bytes()


def atomic(path, content):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as f:
        name = Path(f.name)
        f.write(content if isinstance(content, bytes) else content.encode())
        f.flush()
        os.fsync(f.fileno())
    os.replace(name, path)


class Setup:
    def __init__(self, env=os.environ):
        self.env = env
        self.output = Path(env.get('SETUP_OUTPUT', '/output'))
        self.input = Path(env.get('SETUP_INPUT', '/input'))
        self.controller = Path(env.get('SETUP_CONTROLLER', '/controller'))
        self.bin = Path(env.get('SETUP_BIN', '/usr/local/bin'))
        self.region = env.get('REGION', 'us')
        self.role = env.get('BROKER_ROLE', 'transcode')
        need(self.region in ('eu', 'us') and self.role in ('transcode', 'audio', 'llm'), 'Invalid region or broker role')
        need(self.region == 'us' or self.role == 'transcode', 'EU initial topology supports transcode only')
        self.management = self.flag('INCLUDE_REGIONAL_MANAGEMENT', True)
        self.portal = self.flag('INCLUDE_MEMBER_PORTAL', True)
        self.ownership = self.flag('INCLUDE_GPU_OWNERSHIP', True)
        self.deployment = env.get('DEPLOYMENT_ID', 'regional-pool')
        need(re.fullmatch(r'[a-z][a-z0-9-]{0,62}', self.deployment), 'Invalid deployment ID')
        need(env.get('INGRESS_MODE', 'external') == 'external', 'Only operator-managed external ingress is supported')
        self.state = self.output / '.setup'
        self.material = self.state / 'material'
        need(1 <= int(env.get('CREDENTIAL_DAYS','365')) <= 3650, 'CREDENTIAL_DAYS must be between 1 and 3650')
        self.volume = env.get('CONTROLLER_VOLUME', f'{self.deployment}-{self.region}-controller-data')
        need(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*', self.volume), 'Invalid controller volume name')
        self.options = dict(region=self.region, role=self.role, management=self.management,
                            portal=self.portal, ownership=self.ownership, controller_volume=self.volume,
                            ownership_url='https://gpu-ownership:8443' if self.ownership else self.origin('OWNERSHIP_URL'))
        self.spec = {'deployment_id': self.deployment, 'payee': self.address(env.get('ORCHESTRATOR_ADDRESS', ''))}
        for key, variable in (('broker_url','BROKER_URL'), ('broker_admin_url','BROKER_ADMIN_URL'),
                              ('member_url','REGION_MEMBER_URL'), ('admin_url','REGION_ADMIN_URL'), ('portal_url','PORTAL_URL')):
            self.spec[key] = self.origin(variable)
        need(len(set(self.spec[k] for k in self.spec if k.endswith('_url'))) == 5, 'Public service origins must be distinct')
        self.rpc = [x.strip() for x in load(self.input_path('RPC_URLS_FILE','rpc-urls')).decode().splitlines() if x.strip()]
        need(self.rpc and all(urlsplit(u).scheme == 'https' and urlsplit(u).hostname and not urlsplit(u).username
                             and not urlsplit(u).fragment for u in self.rpc), 'Supply HTTPS RPC URLs in RPC_URLS_FILE')
        for mode in ('RECEIVER_WALLET','PAYOUT_WALLET','BROKER_KEYS','PORTAL_KEYS','SERVICE_CREDENTIALS','RECEIVER_DOMAIN','POOL_IDENTITY','OWNERSHIP_TLS'):
            need(self.mode(mode) in MODES, f'{mode}_MODE must be reuse or create-if-missing')

    def flag(self, name, default):
        v = self.env.get(name, str(default).lower())
        need(v in ('true','false'), f'{name} must be true or false')
        return v == 'true'

    def mode(self, name):
        return self.env.get(name + '_MODE', 'reuse')

    def origin(self, name):
        s = self.env.get(name, '')
        u = urlsplit(s)
        need(u.scheme == 'https' and u.hostname and u.netloc == u.hostname and not u.path and not u.query and not u.fragment,
             f'{name} must be a canonical HTTPS origin')
        return s

    def address(self, value):
        need(re.fullmatch(r'0x[0-9a-fA-F]{40}', value) and int(value,16), 'Invalid Ethereum address')
        return value.lower()

    def input_path(self, variable, default):
        relative = Path(self.env.get(variable, default))
        need(not relative.is_absolute() and '..' not in relative.parts, f'{variable} must be relative to input directory')
        target = self.input / relative
        need(target.resolve().is_relative_to(self.input.resolve()), 'Input path escapes material directory')
        return target

    def copy_existing(self, name, variable=None, default=None):
        target = self.material / name
        source = self.input_path(variable or name.upper().replace('-','_').replace('.','_') + '_FILE', default or name)
        if source.exists():
            content = load(source)
            if target.exists():
                need(load(target) == content, f'Provisioned {name} differs from imported material; explicit rotation required')
            else:
                atomic(target, content)
        return target

    def tool(self, name, *args):
        return run([str(self.bin/name), *map(str,args)])

    def identity(self):
        expected = self.env.get('POOL_ID','')
        saved = self.state/'identity.json'
        if saved.exists():
            stored = json.loads(load(saved))['pool_id']
            need(not expected or expected == stored, 'Configured pool differs from setup journal')
            expected = stored
        if self.management:
            exists = (self.controller/'pool-controller.db').exists()
            need(exists or self.mode('POOL_IDENTITY') == 'create-if-missing', 'Existing controller database required')
            need(exists or not expected, 'Expected pool cannot be created in an empty controller volume')
            need(not exists or expected, 'Existing controller requires explicit POOL_ID')
            args = ['init-identity','--data-dir',self.controller]
            if expected:
                args += ['--expect-pool-id',expected]
            pid = json.loads(self.tool('livepeer-pool-controller',*args))['pool_id']
            os.chown(self.controller,65532,65532)
            os.chown(self.controller/'pool-controller.db',65532,65532)
        else:
            pid = expected
        need(re.fullmatch(r'pool_[0-9a-f]{32}',pid or ''), 'A persisted regional POOL_ID is required')
        atomic(saved,json.dumps({'pool_id':pid}))
        self.spec['pool_id'] = pid

    def wallet(self, role):
        key = self.copy_existing(role+'-keystore.json', role.upper()+'_KEYSTORE_FILE')
        pw = self.copy_existing(role+'-password', role.upper()+'_PASSWORD_FILE')
        if not key.exists():
            need(self.mode(role.upper()+'_WALLET') == 'create-if-missing', f'Missing {role} wallet')
            if not pw.exists():
                atomic(pw,os.urandom(32).hex()+'\n')
            self.tool('secure-orch-keygen','--out',key,'--password-file',pw)
        need(pw.exists() and load(pw).strip(), f'Missing {role} password')
        data=json.loads(load(key))
        need(data.get('version') == 3 and ('crypto' in data or 'Crypto' in data), 'Require V3 keystore')
        address=self.address('0x'+data['address'].removeprefix('0x'))
        expected=self.env.get(role.upper()+'_WALLET_ADDRESS','')
        need(not expected or self.address(expected)==address, f'{role} wallet address mismatch')
        self.spec[role+'_wallet']=address

    def keys(self):
        for name in ('broker-settlement.key','broker-sealing.key'):
            path=self.copy_existing(name)
            if not path.exists():
                need(self.mode('BROKER_KEYS')=='create-if-missing',f'Missing {name}')
                if name=='broker-settlement.key':
                    self.tool('livepeer-capability-broker','settlement-key','generate','--out',path)
                else:
                    atomic(path,os.urandom(32).hex()+'\n')
            need(re.fullmatch(rb'[0-9a-fA-F]{64}',load(path).strip()),f'Invalid {name}')
        domain=self.copy_existing('receiver-domain-id','RECEIVER_DOMAIN_FILE','receiver-domain-id')
        if not domain.exists():
            need(self.mode('RECEIVER_DOMAIN')=='create-if-missing','Receiver domain required')
            atomic(domain,'0x'+os.urandom(32).hex()+'\n')
        self.spec['source_id']=load(domain).decode().strip()
        need(re.fullmatch(r'0x[0-9a-f]{64}',self.spec['source_id']) and int(self.spec['source_id'],16),'Invalid domain')
        issuer=self.copy_existing('portal-issuer.json') if self.portal else self.material/'portal-issuer.json'
        trust=self.copy_existing('member-trust.json')
        if self.portal and (not issuer.exists() or not trust.exists()):
            need(not issuer.exists() and not trust.exists(),'Incomplete portal key pair; inspect before retry')
            need(self.mode('PORTAL_KEYS')=='create-if-missing','Portal issuer and trust required')
            self.tool('livepeer-member-portal','--generate-key',issuer,'--trust-output',trust,
                      '--issuer',self.spec['portal_url'],'--key-id','portal-'+dt.datetime.now(dt.timezone.utc).strftime('%Y%m%d'))
        if self.portal or self.management:
            data=json.loads(load(trust))
            need(data['issuer']==self.spec['portal_url'],'Portal trust issuer mismatch')
            now=dt.datetime.now(dt.timezone.utc)
            need(any(not k.get('revoked') and dt.datetime.fromisoformat(k['not_before'].replace('Z','+00:00')) <= now < dt.datetime.fromisoformat(k['not_after'].replace('Z','+00:00')) for k in data['keys']), 'No currently valid portal trust key')
        if self.portal:
            data=json.loads(load(issuer))
            need(data['issuer']==self.spec['portal_url'],'Portal signing issuer mismatch')
            private=base64.b64decode(data['private_key'],validate=True)
            matching=[k for k in json.loads(load(trust))['keys'] if k['id']==data['key_id']]
            need(len(private)==64 and len(matching)==1 and base64.b64decode(matching[0]['public_key'])==private[32:],'Portal key/trust mismatch')
        for name in ('ownership-tls.key','ownership-tls.crt','ownership-ca.crt'):
            if self.ownership or name=='ownership-ca.crt':
                self.copy_existing(name)
        if self.ownership:
            key,cert=self.material/'ownership-tls.key',self.material/'ownership-tls.crt'
            if not key.exists() or not cert.exists():
                need(not key.exists() and not cert.exists(),'Incomplete ownership TLS pair')
                need(self.mode('OWNERSHIP_TLS')=='create-if-missing','Ownership TLS pair required')
                # Generate in a temporary directory, publish a complete pair on success.
                with tempfile.TemporaryDirectory(dir=self.state) as tmp:
                    run(['openssl','req','-x509','-newkey','rsa:3072','-nodes','-sha256','-days','365','-subj','/CN=gpu-ownership',
                         '-addext','subjectAltName=DNS:gpu-ownership','-addext','basicConstraints=critical,CA:TRUE',
                         '-keyout',tmp+'/key','-out',tmp+'/cert'])
                    atomic(key,Path(tmp+'/key').read_bytes())
                    atomic(cert,Path(tmp+'/cert').read_bytes())
            ca=self.material/'ownership-ca.crt'
            if not ca.exists(): atomic(ca,load(cert))
            run(['openssl','verify','-CAfile',str(ca),'-verify_hostname','gpu-ownership',str(cert)])
            need(run(['openssl','pkey','-in',str(key),'-pubout'])==run(['openssl','x509','-in',str(cert),'-pubkey','-noout']), 'TLS key/certificate mismatch')

    def credentials(self):
        directory=self.material/'service-credentials'
        imported=self.input_path('SERVICE_CREDENTIALS_DIR','service-credentials')
        if imported.exists():
            for path in imported.rglob('*'):
                need(not path.is_symlink(),'Symlinks forbidden in imported credentials')
                if path.is_file():
                    relative=path.relative_to(imported)
                    dest=directory/relative
                    if dest.exists(): need(load(dest)==load(path),'Imported credential differs from setup journal')
                    else: atomic(dest,load(path))
        expiry=(dt.datetime.now(dt.timezone.utc)+dt.timedelta(days=int(self.env.get('CREDENTIAL_DAYS','365')))).isoformat()
        for caller,target,role,resource in links(self.region,self.role):
            name=token_name(caller,target,role)
            tokenpath=directory/caller/name
            verifier=directory/target/'service-auth.json'
            entries=json.loads(load(verifier))['credentials'] if verifier.exists() else []
            selected=[x for x in entries if x['id']==name[:-6]]
            if not tokenpath.exists():
                need(not selected,'Verifier exists without corresponding token')
                need(self.mode('SERVICE_CREDENTIALS')=='create-if-missing',f'Missing credential: {name}')
                atomic(tokenpath,os.urandom(32).hex()+'\n')
            token=load(tokenpath).decode().strip()
            need(re.fullmatch(r'[0-9a-f]{64}',token),'Invalid credential token')
            expected={'id':name[:-6],'pool_id':self.spec['pool_id'],'roles':[role],'resources':[resource],
                      'token_sha256':hashlib.sha256(token.encode()).hexdigest()}
            if role=='broker': expected['source_id']=self.spec['source_id']
            if not selected:
                need(self.mode('SERVICE_CREDENTIALS')=='create-if-missing','Missing credential verifier')
                selected=[dict(expected,expires_at=expiry)]
                entries+=selected
                atomic(verifier,json.dumps({'credentials':entries}))
            need(len(selected)==1 and all(selected[0].get(k)==v for k,v in expected.items()) and not selected[0].get('revoked'), 'Credential scope or hash mismatch')
            need(dt.datetime.fromisoformat(selected[0]['expires_at'].replace('Z','+00:00'))>dt.datetime.now(dt.timezone.utc),'Expired credential')
        metadata=directory/'metadata.json'
        identity={'pool_id':self.spec['pool_id'],'source_id':self.spec['source_id']}
        if metadata.exists():
            stored=json.loads(load(metadata))
            need(all(stored.get(k)==v for k,v in identity.items()),'Credential identity mismatch')
        else: atomic(metadata,json.dumps(identity))

    def images(self):
        lock=self.env.get('IMAGE_LOCK_FILE','')
        if lock:
            images=json.loads(load(self.input_path('IMAGE_LOCK_FILE',lock)))
        else:
            images={}
            registry=self.env.get('IMAGE_REGISTRY','tztcloud')
            tag=self.env.get('IMAGE_TAG','v2.0.0')
            for component in COMPONENTS:
                ref=f'{registry}/livepeer-{component}:{tag}'
                digest=run(['skopeo','inspect','--format','{{.Digest}}','docker://'+ref]).strip()
                images[component]=ref.rsplit(':',1)[0]+'@'+digest
        need(set(images)==set(COMPONENTS),'Image lock must contain all eight component names')
        need(all(re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}',x) for x in images.values()),'Immutable image references required')
        return images

    def plan(self):
        result={'region':self.region,'broker_role':self.role,'controller_volume':self.volume,
                'components':self.options,'actions':{},'writes':False}
        groups={'RECEIVER_WALLET':['receiver-keystore.json','receiver-password'],
                'BROKER_KEYS':['broker-settlement.key','broker-sealing.key'],
                'RECEIVER_DOMAIN':['receiver-domain-id']}
        if self.management: groups['PAYOUT_WALLET']=['payout-keystore.json','payout-password']
        if self.portal: groups['PORTAL_KEYS']=['portal-issuer.json','member-trust.json']
        if self.ownership: groups['OWNERSHIP_TLS']=['ownership-tls.key','ownership-tls.crt']
        for group,names in groups.items():
            aliases = {'receiver-keystore.json':'RECEIVER_KEYSTORE_FILE','payout-keystore.json':'PAYOUT_KEYSTORE_FILE','receiver-domain-id':'RECEIVER_DOMAIN_FILE'}
            present=all((self.material/n).exists() or self.input_path(aliases.get(n,n.upper().replace('-','_').replace('.','_')+'_FILE'),n).exists() for n in names)
            result['actions'][group]='reuse' if present else ('create missing' if self.mode(group)=='create-if-missing' else 'missing: reuse requires files')
        result['actions']['POOL_IDENTITY']='verify existing' if (self.controller/'pool-controller.db').exists() else ('initialize' if self.mode('POOL_IDENTITY')=='create-if-missing' else 'missing existing store')
        if not self.management:
            result['actions']['POOL_IDENTITY']='use configured remote pool ID'
        if self.management and not self.portal:
            trust=self.input_path('MEMBER_TRUST_JSON_FILE','member-trust.json')
            result['actions']['PORTAL_TRUST']='reuse' if trust.exists() or (self.material/'member-trust.json').exists() else 'missing public portal trust'
        result['actions']['SERVICE_CREDENTIALS']=self.mode('SERVICE_CREDENTIALS')
        print(json.dumps(result,indent=2))

    def validate(self, package):
        manifest=json.loads(load(package/'deployment-manifest.json'))
        for relative,digest in manifest['files'].items():
            path=package/relative
            need(path.resolve().is_relative_to(package.resolve()),'Invalid manifest path')
            need(hashlib.sha256(load(path)).hexdigest()==digest,'Package file changed since generation')
        compose=json.loads(load(package/'compose.yaml'))
        results={}
        for name,service in compose['services'].items():
            config=package/'config'/f'{name}.json'
            if not config.exists(): continue
            data=json.loads(load(config))
            secret_dir=package/'secrets'/name
            def rewrite(v):
                if isinstance(v,dict): return {k:rewrite(x) for k,x in v.items()}
                if isinstance(v,list): return [rewrite(x) for x in v]
                if isinstance(v,str) and v.startswith('/secrets/'): return str(secret_dir/v.removeprefix('/secrets/'))
                return v
            with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
                temp=Path(tmp)/'config.json'
                temp.write_text(json.dumps(rewrite(data)))
                if name.endswith('-controller'): tool='livepeer-pool-controller'; args=['validate-config']
                elif name.endswith('-reconciler'): tool='livepeer-pool-reconciler'; args=['validate-config']
                elif name.endswith('-executor'): tool='livepeer-pool-payout-executor'; args=['validate-config']
                elif name.endswith('-broker'): tool='livepeer-capability-broker'; args=['config','validate']
                else: tool='livepeer-member-portal'; args=['--validate-config']
                self.tool(tool,*args,'--config',temp)
                results[name]='offline schema parser passed'
            for p in secret_dir.rglob('*'):
                if p.is_file(): need(p.stat().st_mode & 0o077 == 0, 'Secret permissions too broad')
        report={'checks':results,'live_validation':False,'scope':'embedded module parsers; no chain, wallets unlocked, ingress or GPU validation'}
        atomic(package/'validation-report.json',json.dumps(report,indent=2)+'\n')
        print(json.dumps(report,indent=2))

    def generate(self):
        self.state.mkdir(parents=True,exist_ok=True,mode=0o700)
        with (self.state/'lock').open('a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            binding=self.state/'binding.json'
            current_binding={'deployment_id':self.deployment,'region':self.region,'role':self.role,'volume':self.volume,'payee':self.spec['payee']}
            if binding.exists(): need(json.loads(load(binding))==current_binding,'Setup journal binding mismatch')
            else: atomic(binding,json.dumps(current_binding))
            self.material.mkdir(exist_ok=True,mode=0o700)
            self.identity()
            self.wallet('receiver')
            if self.management: self.wallet('payout')
            addresses=[self.spec[k] for k in ('payee','receiver_wallet','payout_wallet') if k in self.spec]
            need(len(set(addresses))==len(addresses),'Signing wallet roles must be distinct')
            self.keys()
            self.credentials()
            images=self.images()
            hashes={str(p.relative_to(self.material)):hashlib.sha256(load(p)).hexdigest() for p in self.material.rglob('*') if p.is_file()}
            fingerprint=hashlib.sha256(json.dumps([self.spec,self.options,self.rpc,images,hashes],sort_keys=True).encode()).hexdigest()
            revisions=self.output/'revisions'
            revisions.mkdir(exist_ok=True,mode=0o700)
            package=revisions/fingerprint[:20]
            if not package.exists():
                staging=Path(tempfile.mkdtemp(prefix='.pending-',dir=revisions))
                compose=render(staging,self.material,self.spec,self.rpc,images,self.options)
                files={str(p.relative_to(staging)):hashlib.sha256(load(p)).hexdigest() for p in staging.rglob('*') if p.is_file()}
                expiry_info={'service_credentials': {str(p.relative_to(self.material)): {c['id']:c['expires_at'] for c in json.loads(load(p))['credentials']} for p in (self.material/'service-credentials').rglob('service-auth.json')}}
                trust=self.material/'member-trust.json'
                if trust.exists(): expiry_info['portal_keys']={k['id']:k['not_after'] for k in json.loads(load(trust))['keys']}
                cert=self.material/'ownership-tls.crt'
                if cert.exists(): expiry_info['ownership_tls']=run(['openssl','x509','-in',str(cert),'-enddate','-noout']).strip()
                write(staging/'deployment-manifest.json',{'version':1,'expiry':expiry_info,'spec':self.spec,'options':self.options,'images':images,'files':files,
                    'fingerprint':fingerprint,'volumes':list(compose['volumes']),'status':'offline generated; not deployed'})
                self.validate(staging)
                # Only service-mounted copies become runtime-owned. Journal and operator secrets stay root-only.
                for folder in ('config','secrets'):
                    for p in (staging/folder).rglob('*'): os.chown(p,65532,65532)
                    os.chown(staging/folder,65532,65532)
                os.rename(staging,package)
            else:
                need(json.loads(load(package/'deployment-manifest.json'))['fingerprint']==fingerprint, 'Revision fingerprint collision')
                self.validate(package)
            atomic(self.output/'current-revision',str(package.relative_to(self.output))+'\n')
            print('Package ready: '+str(package))
            if self.env.get('OUTPUT_DIR'):
                print('Host package: '+str(Path(self.env['OUTPUT_DIR'])/package.relative_to(self.output)))
            print('No services started or financial operations performed.')


def main():
    os.umask(0o077)
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command',choices=('plan','generate','validate'))
    args=parser.parse_args()
    setup=Setup()
    if args.command=='plan': setup.plan()
    elif args.command=='generate': setup.generate()
    else:
        revision=load(setup.output/'current-revision').decode().strip()
        path=setup.output/revision
        need(path.resolve().is_relative_to(setup.output.resolve()),'Invalid revision path')
        setup.validate(path)


if __name__=='__main__':
    try: main()
    except (ValueError,OSError,KeyError,subprocess.SubprocessError) as exc:
        print('Setup failed: '+str(exc),file=sys.stderr)
        sys.exit(1)
