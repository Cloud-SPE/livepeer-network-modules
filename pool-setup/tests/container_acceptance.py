#!/usr/bin/env python3
"""Exercise actual setup/keygen/parser binaries with isolated synthetic state."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile


def main(image):
    root=Path(tempfile.mkdtemp(prefix='pool-setup-acceptance-'))
    for n in ('input','output','controller','reused-output','remote-output'): (root/n).mkdir()
    (root/'input/rpc-urls').write_text('https://rpc.example.invalid\n')
    components=('pool-controller','capability-broker','payment-daemon','protocol-daemon','pool-reconciler','pool-payout-executor','member-portal','pool-member-agent')
    (root/'input/images.json').write_text(json.dumps({c:'test/livepeer-'+c+'@sha256:'+'a'*64 for c in components}))
    env={'DEPLOYMENT_ID':'test-pool','REGION':'us','BROKER_ROLE':'transcode','ORCHESTRATOR_ADDRESS':'0x'+'1'*40,
         'BROKER_URL':'https://broker.example.invalid','BROKER_ADMIN_URL':'https://broker-admin.example.invalid',
         'REGION_MEMBER_URL':'https://members.example.invalid','REGION_ADMIN_URL':'https://management.example.invalid',
         'PORTAL_URL':'https://portal.example.invalid','IMAGE_LOCK_FILE':'images.json'}
    prefixes=('POOL_IDENTITY','RECEIVER_WALLET','PAYOUT_WALLET','BROKER_KEYS','PORTAL_KEYS','RECEIVER_DOMAIN','SERVICE_CREDENTIALS','OWNERSHIP_TLS')
    for prefix in prefixes: env[prefix+'_MODE']='create-if-missing'

    def execute(command, output='output', input_dir='input', settings=None, fail=False, controller_dir='controller'):
        (root/'env').write_text('\n'.join(k+'='+v for k,v in (settings or env).items())+'\n')
        args=['docker','run','--rm','--network=none','--read-only','--tmpfs','/tmp','--env-file',str(root/'env')]
        for name,dest in [(input_dir,'/input'),(output,'/output'),(controller_dir,'/controller')]:
            args+=['--mount',f'type=bind,src={root/name},dst={dest}'+(',readonly' if dest=='/input' else '')]
        result=subprocess.run(args+[image,command],capture_output=True,text=True)
        if fail:
            assert result.returncode!=0, 'Expected failure'
        else:
            assert result.returncode==0,result.stdout+result.stderr
        return result.stdout

    def inspect(code):
        return subprocess.check_output(['docker','run','--rm','--network=none','--mount',f'type=bind,src={root},dst=/fixture',
            '--entrypoint','python3',image,'-c','import json, pathlib, hashlib, shutil; root=pathlib.Path("/fixture"); '+code],text=True).strip()

    execute('plan')
    assert not (root/'output/.setup').exists()
    print('PASS read-only plan',flush=True)
    execute('generate')
    snapshot=inspect('print(json.dumps({str(p.relative_to(root/"output/.setup/material")):hashlib.sha256(p.read_bytes()).hexdigest() for p in (root/"output/.setup/material").rglob("*") if p.is_file()},sort_keys=True))')
    revision=(root/'output/current-revision').read_text()
    execute('generate'); execute('validate')
    assert revision==(root/'output/current-revision').read_text()
    assert snapshot==inspect('print(json.dumps({str(p.relative_to(root/"output/.setup/material")):hashlib.sha256(p.read_bytes()).hexdigest() for p in (root/"output/.setup/material").rglob("*") if p.is_file()},sort_keys=True))')
    print('PASS real wallet/key generation, five parsers, idempotent revision and material',flush=True)
    pid=json.loads((root/'output/.setup/identity.json').read_text())['pool_id']
    # Recreate the exact operator import shape: secrets/* plus config/domain.
    inspect('shutil.copytree(root/"output/.setup/material",root/"import/secrets"); (root/"import/config").mkdir(); shutil.copyfile(root/"import/secrets/receiver-domain-id",root/"import/config/receiver-domain-id"); shutil.copyfile(root/"input/rpc-urls",root/"import/secrets/rpc-urls"); shutil.copyfile(root/"input/images.json",root/"import/images.json")')
    reused=dict(env,POOL_ID=pid,RPC_URLS_FILE='secrets/rpc-urls',RECEIVER_DOMAIN_FILE='config/receiver-domain-id',SERVICE_CREDENTIALS_DIR='secrets/service-credentials')
    for prefix in prefixes: reused[prefix+'_MODE']='reuse'
    for variable,name in [('RECEIVER_KEYSTORE_FILE','receiver-keystore.json'),('RECEIVER_PASSWORD_FILE','receiver-password'),('PAYOUT_KEYSTORE_FILE','payout-keystore.json'),('PAYOUT_PASSWORD_FILE','payout-password'),('BROKER_SETTLEMENT_KEY_FILE','broker-settlement.key'),('BROKER_SEALING_KEY_FILE','broker-sealing.key'),('PORTAL_ISSUER_JSON_FILE','portal-issuer.json'),('MEMBER_TRUST_JSON_FILE','member-trust.json'),('OWNERSHIP_TLS_KEY_FILE','ownership-tls.key'),('OWNERSHIP_TLS_CRT_FILE','ownership-tls.crt'),('OWNERSHIP_CA_CRT_FILE','ownership-ca.crt')]: reused[variable]='secrets/'+name
    execute('generate',output='reused-output',input_dir='import',settings=reused)
    assert snapshot==inspect('print(json.dumps({str(p.relative_to(root/"reused-output/.setup/material")):hashlib.sha256(p.read_bytes()).hexdigest() for p in (root/"reused-output/.setup/material").rglob("*") if p.is_file()},sort_keys=True))')
    print('PASS existing operator layout and controller identity reused byte-for-byte',flush=True)
    changed=dict(env,BROKER_URL='https://new-broker.example.invalid')
    execute('generate',settings=changed)
    assert revision!=(root/'output/current-revision').read_text()
    assert snapshot==inspect('print(json.dumps({str(p.relative_to(root/"output/.setup/material")):hashlib.sha256(p.read_bytes()).hexdigest() for p in (root/"output/.setup/material").rglob("*") if p.is_file()},sort_keys=True))')
    print('PASS new configuration revision preserves all material',flush=True)
    remote=dict(reused,INCLUDE_REGIONAL_MANAGEMENT='false',INCLUDE_MEMBER_PORTAL='false',INCLUDE_GPU_OWNERSHIP='false',OWNERSHIP_URL='https://ownership.example.invalid')
    execute('generate',output='remote-output',input_dir='import',settings=remote)
    remote_services=inspect('p=root/"remote-output"/(root/"remote-output/current-revision").read_text().strip(); print(json.dumps(list(json.loads((p/"compose.yaml").read_text())["services"])))')
    assert set(json.loads(remote_services))=={'us-transcode-broker','us-transcode-broker-receiver'}
    assert inspect('print((root/"remote-output/.setup/material/payout-keystore.json").exists())')=='False'
    print('PASS optional shared/management services omitted without payout/private portal keys',flush=True)
    for region,role in [('eu','transcode'),('us','audio'),('us','llm')]:
        name=region+'-'+role
        for folder in (name+'-output',name+'-controller'): (root/folder).mkdir()
        settings=dict(env,REGION=region,BROKER_ROLE=role)
        execute('generate',output=name+'-output',settings=settings,controller_dir=name+'-controller')
    print('PASS generic EU transcode and US audio/LLM packages',flush=True)
    # Actual Compose parser on generated JSON-as-YAML, all profiles.
    compose=root/'output'/ (root/'output/current-revision').read_text().strip()/'compose.yaml'
    subprocess.run(['docker','compose','-f',str(compose),'--profile','*','config','--quiet'],check=True)
    inspect('p=root/"output"/(root/"output/current-revision").read_text().strip(); (p/"config/us-controller.json").write_text("{}")')
    execute('validate',fail=True)
    print('PASS Compose parser and tamper rejection',flush=True)
    print('Fixture evidence:',root)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image',default='livepeer-pool-setup:local')
    main(parser.parse_args().image)
