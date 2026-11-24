import importlib.util
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from contextlib import contextmanager
from unittest import mock

SCRIPTS=Path(__file__).resolve().parents[2]/'scripts'
sys.path.insert(0,str(SCRIPTS))
from build_config import config,version
from ci_metadata import resolve

def module(name,file):
    spec=importlib.util.spec_from_file_location(name,SCRIPTS/file)
    item=importlib.util.module_from_spec(spec);spec.loader.exec_module(item);return item
stage=module('stage_release','stage-release.py')
publisher=module('publish_release','publish-release.py')
SHA='a'*40

@contextmanager
def environment(values=None):
    # Preserve unrelated process variables, including empty Git config values
    # that Windows removes if the whole environment is cleared and restored.
    keys=('GITHUB_SHA','GITHUB_EVENT_NAME','NETGO_PUBLISH','NETGO_VERSION','NETGO_ARCH','NETGO_REQUESTED_VERSION','NETGO_REQUESTED_ARCH')
    saved={key:os.environ.get(key) for key in keys}
    try:
        for key in keys:os.environ.pop(key,None)
        os.environ.update(values or {})
        yield
    finally:
        for key,value in saved.items():
            if value is None:os.environ.pop(key,None)
            else:os.environ[key]=value

class FakeAPI:
    def __init__(self,race=False):self.release=None;self.tag=SHA;self.assets=[];self.writes=[];self.race=race
    def maybe(self,path):
        if path.startswith('/releases/tags/'):return self.release if self.release and not self.release['draft'] else None
        if path.startswith('/commits/'):return {'sha':self.tag}
        raise AssertionError(path)
    def request(self,path,method='GET',data=None,upload=False):
        if method=='GET' and path.startswith('/releases?'):return [self.release] if self.release else []
        if path=='/releases' and method=='POST':
            self.writes.append(path);self.release={'id':1,'draft':data['draft'],'prerelease':data['prerelease'],'target_commitish':SHA,'tag_name':data['tag_name'],'html_url':'https://github.com/example/repo/releases/tag/'+data['tag_name']}
            if self.race:self.race=False;raise publisher.APIError(422)
            return self.release
        if method=='GET' and path.startswith('/releases/1/assets'):return self.assets
        if upload:
            import hashlib,urllib.parse
            name=urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)['name'][0]
            self.writes.append(name);item={'id':len(self.assets)+10,'name':name,'size':len(data),'digest':'sha256:'+hashlib.sha256(data).hexdigest()};self.assets.append(item);return item
        raise AssertionError((path,method))

class ReleaseTests(unittest.TestCase):
    def data(self):return resolve('linux-client',{'NETGO_REQUESTED_VERSION':'0.3.1','NETGO_REQUESTED_ARCH':'amd64'})
    def payload(self,root):
        data=self.data();folder=root/'dist/linux-desktop-amd64';folder.mkdir(parents=True)
        (folder/'BUILDINFO.json').write_text(json.dumps({'version':'0.3.1','arch':'amd64'}))
        sample=root/'program';sample.write_bytes(b'program')
        archive=root/'dist/NetGo-0.3.1-linux-client-amd64.tar.gz'
        with tarfile.open(archive,'w:gz') as tar:tar.add(sample,arcname='linux-desktop-amd64/netgo-desktop')
        output=root/'release';stage.stage(data,output,root,commit=SHA);return output
    def test_versions_and_real_architectures(self):
        self.assertEqual(version('0.3.1-rc.2+build.7'),'0.3.1-rc.2+build.7')
        for value in ('v0.3.1','01.2.3','0.3.1-01','0.3.1-rc..1','0.3.1+..','0.3.1; echo token','0.3.1\n','65536.0.0'):
            with self.subTest(value=value),self.assertRaises(ValueError):version(value)
        with self.assertRaises(ValueError):config('linux-server','0.3.1','arm64')
        self.assertEqual(config('macos','0.3.1','x86_64')['arch'],'x86_64')
    def test_push_cannot_publish_and_stable_rejects_dev_suffix(self):
        with self.assertRaises(ValueError):resolve('vps',{'NETGO_PUBLISH':'true','GITHUB_EVENT_NAME':'push'})
        with self.assertRaises(ValueError):resolve('vps',{'NETGO_REQUESTED_VERSION':'0.3.1-dev','NETGO_PUBLISH':'true','GITHUB_EVENT_NAME':'workflow_dispatch','NETGO_RELEASE_TYPE':'stable'})
        self.assertEqual(resolve('vps',{'GITHUB_EVENT_NAME':'push'})['publish'],'false')
    def test_only_selected_platform_assets_and_sidecars_are_staged(self):
        with tempfile.TemporaryDirectory() as temporary,environment():
            root=Path(temporary);output=self.payload(root);data=publisher.load_payload(output)
            self.assertEqual(len(data['assets']),4)
            self.assertFalse(any(x['name']=='SHA256SUMS' for x in data['assets']))
            self.assertTrue(any(x['name'].endswith('.tar.gz.sha256') for x in data['assets']))
    def test_tampering_is_rejected_before_github_write(self):
        with tempfile.TemporaryDirectory() as temporary,environment():
            output=self.payload(Path(temporary));next(output.glob('*.tar.gz')).write_bytes(b'changed');api=FakeAPI()
            with self.assertRaises(ValueError):publisher.publish(api,output,'stable')
            self.assertEqual(api.writes,[])
    def test_parallel_release_creation_and_identical_retry(self):
        with tempfile.TemporaryDirectory() as temporary,environment():
            output=self.payload(Path(temporary));api=FakeAPI(race=True)
            publisher.publish(api,output,'stable');count=len(api.writes)
            publisher.publish(api,output,'stable');self.assertEqual(len(api.writes),count)
    def test_wrong_commit_and_existing_changed_asset_are_preserved(self):
        with tempfile.TemporaryDirectory() as temporary,environment():
            output=self.payload(Path(temporary));api=FakeAPI();api.tag='b'*40
            with self.assertRaises(ValueError):publisher.publish(api,output,'stable')
            self.assertEqual(api.writes,[])
            api.tag=SHA;publisher.publish(api,output,'stable');api.assets[0]['digest']='sha256:'+'0'*64;count=len(api.writes)
            with self.assertRaises(ValueError):publisher.publish(api,output,'stable')
            self.assertEqual(len(api.writes),count)
    def test_draft_creation_race_and_repeat_reuse_unpublished_release(self):
        with tempfile.TemporaryDirectory() as temporary,environment():
            output=self.payload(Path(temporary));api=FakeAPI(race=True)
            publisher.publish(api,output,'draft');count=len(api.writes)
            self.assertIsNone(api.maybe('/releases/tags/v0.3.1'))
            publisher.publish(api,output,'draft')
            self.assertEqual(len(api.writes),count)
            self.assertEqual(api.writes.count('/releases'),1)
            with self.assertRaises(ValueError):publisher.publish(api,output,'stable')
            self.assertTrue(api.release['draft'])
    def test_draft_lookup_follows_release_pages(self):
        api=FakeAPI();api.release={'tag_name':'v0.3.1','draft':True}
        def page(path,*args,**kwargs):
            return [{'tag_name':'v0.0.'+str(i),'draft':False} for i in range(100)] if path.endswith('page=1') else [api.release]
        with mock.patch.object(api,'request',side_effect=page) as request:
            self.assertEqual(publisher.find_release(api,'v0.3.1'),api.release)
            self.assertEqual(request.call_count,2)
    def test_artifact_download_uses_supported_api_media_type(self):
        api=publisher.GitHub('example/repo','fake-token')
        def response(request,**kwargs):
            if '/actions/artifacts/' in request.full_url and request.get_header('Accept')!='application/vnd.github+json':
                raise publisher.urllib.error.HTTPError(request.full_url,415,'Unsupported Media Type',{},None)
            handle=mock.MagicMock();handle.__enter__.return_value.read.return_value=b'archive';return handle
        with mock.patch.object(api.opener,'open',side_effect=response) as request:
            self.assertEqual(api.binary('/actions/artifacts/123/zip'),b'archive')
            self.assertEqual(api.binary('/releases/assets/456'),b'archive')
            self.assertEqual(request.call_args.args[0].get_header('Accept'),'application/octet-stream')
    def test_no_publish_permission_without_manual_switch(self):
        with environment({'GITHUB_EVENT_NAME':'push','NETGO_PUBLISH':'false'}),mock.patch.object(sys,'argv',['publish-release.py']),mock.patch('sys.stderr'),self.assertRaises(SystemExit):publisher.main()

if __name__=='__main__':unittest.main()
