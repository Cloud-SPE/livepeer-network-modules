import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import setup
from render import render, links, token_name


def environment(root):
    material=root/'input'; material.mkdir()
    (material/'rpc-urls').write_text('https://rpc.example.invalid\n')
    return {'SETUP_INPUT':str(material),'SETUP_OUTPUT':str(root/'output'),'SETUP_CONTROLLER':str(root/'controller'),
        'DEPLOYMENT_ID':'test-pool','REGION':'us','BROKER_ROLE':'transcode',
        'ORCHESTRATOR_ADDRESS':'0x'+'1'*40,'BROKER_URL':'https://broker.example.invalid',
        'BROKER_ADMIN_URL':'https://broker-admin.example.invalid','REGION_MEMBER_URL':'https://members.example.invalid',
        'REGION_ADMIN_URL':'https://management.example.invalid','PORTAL_URL':'https://portal.example.invalid'}


class SetupTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name); self.env=environment(self.root)

    def test_plan_has_no_writes_or_subprocess(self):
        instance=setup.Setup(self.env)
        with patch('setup.subprocess.run',side_effect=AssertionError('subprocess forbidden')):
            instance.plan()
        self.assertFalse(instance.output.exists())
        self.assertFalse(instance.controller.exists())

    def test_missing_reuse_identity_cannot_initialize(self):
        instance=setup.Setup(self.env)
        instance.state.mkdir(parents=True)
        with patch.object(instance,'tool',side_effect=AssertionError('initializer forbidden')):
            with self.assertRaisesRegex(ValueError,'Existing controller'):
                instance.identity()

    def test_expected_identity_cannot_initialize_empty_volume(self):
        self.env.update(POOL_ID='pool_'+'1'*32,POOL_IDENTITY_MODE='create-if-missing')
        instance=setup.Setup(self.env); instance.state.mkdir(parents=True)
        with self.assertRaisesRegex(ValueError,'Expected pool'):
            instance.identity()

    def test_input_escape_and_invalid_flags_refused(self):
        for setting,value in [('RPC_URLS_FILE','../escape'),('INCLUDE_MEMBER_PORTAL','yes'),
                              ('RECEIVER_WALLET_MODE','overwrite'),('REGION','unknown')]:
            env=dict(self.env); env[setting]=value
            with self.subTest(setting=setting),self.assertRaises(ValueError): setup.Setup(env)

    def test_stable_import_refuses_material_replacement(self):
        instance=setup.Setup(self.env); instance.material.mkdir(parents=True)
        src=instance.input/'broker-sealing.key'; src.write_text('a'*64)
        dest=instance.copy_existing('broker-sealing.key')
        self.assertEqual(dest.read_text(),'a'*64)
        src.write_text('b'*64)
        with self.assertRaisesRegex(ValueError,'differs'): instance.copy_existing('broker-sealing.key')
        self.assertEqual(dest.read_text(),'a'*64)

    def test_interrupted_credential_creation_reuses_token(self):
        self.env['SERVICE_CREDENTIALS_MODE']='create-if-missing'
        instance=setup.Setup(self.env); instance.material.mkdir(parents=True)
        instance.spec.update(pool_id='pool_'+'1'*32,source_id='0x'+'2'*64)
        caller,target,role,_=links('us','transcode')[0]
        name=token_name(caller,target,role)
        token=instance.material/'service-credentials'/caller/name
        setup.atomic(token,'a'*64+'\n')
        instance.credentials()
        self.assertEqual(token.read_text(),'a'*64+'\n')
        files={str(p):p.read_bytes() for p in instance.material.rglob('*') if p.is_file()}
        instance.credentials()
        self.assertEqual(files,{str(p):p.read_bytes() for p in instance.material.rglob('*') if p.is_file()})

    def test_incomplete_wallet_reuses_password(self):
        self.env['RECEIVER_WALLET_MODE']='create-if-missing'
        instance=setup.Setup(self.env); instance.material.mkdir(parents=True)
        pw=instance.material/'receiver-password'; pw.write_text('password-preserved')
        def keygen(*args):
            (instance.material/'receiver-keystore.json').write_text(json.dumps({'version':3,'address':'2'*40,'crypto':{}}))
        with patch.object(instance,'tool',side_effect=keygen): instance.wallet('receiver')
        self.assertEqual(pw.read_text(),'password-preserved')
        self.assertEqual(instance.spec['receiver_wallet'],'0x'+'2'*40)


if __name__=='__main__': unittest.main()
