"""Publish only verified per-platform assets after an explicit manual choice."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from build_config import config, source_commit
from ci_metadata import boolean

class APIError(RuntimeError):
    def __init__(self, status): self.status=status;super().__init__('GitHub 请求失败，HTTP '+str(status))

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):return None

class GitHub:
    def __init__(self,repo,token):
        if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+',repo):raise ValueError('无效仓库名称')
        if not token:raise ValueError('缺少仅用于发布任务的 GITHUB_TOKEN')
        self.repo=repo;self.token=token;self.opener=urllib.request.build_opener(NoRedirect())
    def request(self,path,method='GET',data=None,upload=False):
        base='https://uploads.github.com' if upload else 'https://api.github.com'
        body=data if isinstance(data,bytes) else json.dumps(data).encode() if data is not None else None
        headers={'Authorization':'Bearer '+self.token,'User-Agent':'NetGo-release','Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28','Content-Type':'application/octet-stream' if upload else 'application/json'}
        req=urllib.request.Request(base+'/repos/'+self.repo+path,method=method,data=body,headers=headers)
        try:
            with self.opener.open(req,timeout=600) as response:raw=response.read();return json.loads(raw) if raw else None
        except urllib.error.HTTPError as error:raise APIError(error.code) from None
    def binary(self,path):
        # Artifact ZIP endpoints redirect using the JSON media type. The
        # octet-stream type is only accepted for individual Release assets.
        accept='application/vnd.github+json' if path.startswith('/actions/') else 'application/octet-stream'
        req=urllib.request.Request('https://api.github.com/repos/'+self.repo+path,headers={'Authorization':'Bearer '+self.token,'User-Agent':'NetGo-release','Accept':accept,'X-GitHub-Api-Version':'2022-11-28'})
        try:
            with self.opener.open(req,timeout=60) as response:raw=response.read(512*1024*1024+1)
        except urllib.error.HTTPError as error:
            if error.code!=302:raise APIError(error.code) from None
            location=error.headers['Location']
            if urllib.parse.urlsplit(location).scheme!='https':raise ValueError('下载跳转必须使用 HTTPS')
            # Never forward the GitHub token to the signed storage URL.
            with urllib.request.urlopen(location,timeout=120) as response:raw=response.read(512*1024*1024+1)
        if len(raw)>512*1024*1024:raise ValueError('产物超过下载大小限制')
        return raw
    def maybe(self,path):
        try:return self.request(path)
        except APIError as error:
            if error.status==404:return None
            raise

def load_payload(directory):
    directory=Path(directory);manifest=json.loads((directory/'release-manifest.json').read_text(encoding='utf-8'))
    if manifest.get('schema')!=1:raise ValueError('无效发布清单')
    config(manifest['target'],manifest['version'],manifest['arch'])
    if not re.fullmatch(r'[0-9a-f]{40}',manifest['commit']):raise ValueError('无效源码提交')
    names=set()
    for entry in manifest['assets']:
        name=entry['name']
        if not re.fullmatch(r'[A-Za-z0-9_.+-]+',name) or name in names:raise ValueError('无效或重复发布文件名')
        names.add(name);path=directory/name
        if path.is_symlink() or not path.is_file() or path.stat().st_size!=entry['bytes']:raise ValueError('发布文件缺失或变化：'+name)
        with path.open('rb') as stream:actual=hashlib.file_digest(stream,'sha256').hexdigest()
        if actual!=entry['sha256']:raise ValueError('发布文件摘要不符：'+name)
    if {p.name for p in directory.iterdir()}!=names|{'release-manifest.json'}:raise ValueError('发布目录包含清单以外的文件')
    for entry in manifest['assets']:
        if not entry['name'].endswith('.sha256'):
            sidecar=entry['name']+'.sha256'
            if sidecar not in names or (directory/sidecar).read_text()!=entry['sha256']+'  '+entry['name']+'\n':raise ValueError('缺少平台独立校验文件')
    return manifest

def download_payload(api,run_id,name,directory):
    entries=api.request('/actions/runs/'+str(run_id)+'/artifacts?per_page=100')['artifacts']
    selected=[x for x in entries if x['name']==name and not x['expired']]
    if len(selected)!=1:raise ValueError('未找到唯一成功构建的发布暂存产物')
    raw=api.binary('/actions/artifacts/'+str(selected[0]['id'])+'/zip')
    expected=selected[0].get('digest')
    if not expected or expected!='sha256:'+hashlib.sha256(raw).hexdigest():raise ValueError('GitHub 构建产物摘要不符')
    directory=Path(directory);directory.mkdir(parents=True,exist_ok=False)
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        names=set()
        for item in archive.infolist():
            if item.is_dir():continue
            if not re.fullmatch(r'[A-Za-z0-9_.+-]+',item.filename) or item.filename in names or item.file_size>512*1024*1024:raise ValueError('发布产物路径或大小非法')
            names.add(item.filename);(directory/item.filename).write_bytes(archive.read(item))
        if archive.testzip() is not None:raise ValueError('发布产物 CRC 不符')
    return directory

def find_release(api,tag):
    release=api.maybe('/releases/tags/'+urllib.parse.quote(tag,safe=''))
    if release is not None:return release
    # The tag endpoint only returns published releases. With contents:write,
    # the list endpoint also includes drafts created by other platform jobs.
    page=1
    while True:
        entries=api.request('/releases?per_page=100&page='+str(page))
        matches=[item for item in entries if item['tag_name']==tag]
        if len(matches)>1:raise ValueError('同一标签存在多个 Release，请先在 Releases 页面处理重复草稿')
        if matches:return matches[0]
        if len(entries)<100:return None
        page+=1

def ensure_release(api,manifest,channel):
    tag='v'+manifest['version']
    flags={'draft':channel=='draft','prerelease':channel=='prerelease'}
    if channel=='stable' and '-' in manifest['version'].split('+',1)[0]:raise ValueError('正式 Release 版本不可含预发布后缀')
    release=find_release(api,tag)
    if release is None:
        existing=api.maybe('/commits/'+urllib.parse.quote(tag,safe=''))
        if existing and existing['sha']!=manifest['commit']:raise ValueError('同版本标签已指向其他提交，请使用新版本号')
        body={'tag_name':tag,'target_commitish':manifest['commit'],'name':'NetGo '+tag,
              'body':'各平台可分别构建并发布到同一版本。源码：'+manifest['commit']+'。\n\n构建通过不代替完整设备与远控验收，验证范围见仓库文档。',
              **flags,'make_latest':'true' if channel=='stable' else 'false'}
        try:release=api.request('/releases','POST',body)
        except APIError as error:
            if error.status!=422:raise
            # Independent platforms may create the same tag concurrently.
            for _ in range(6):
                release=find_release(api,tag)
                if release is not None:break
                time.sleep(1)
            if release is None:raise ValueError('并行创建 Release 后仍不可读取') from None
    actual=api.maybe('/commits/'+urllib.parse.quote(tag,safe=''))
    if actual is None and release['draft']:actual=api.maybe('/commits/'+urllib.parse.quote(release['target_commitish'],safe=''))
    if actual is None or actual['sha']!=manifest['commit']:raise ValueError('Release 标签与构建提交不同，拒绝混合版本产物')
    if any(release[k]!=v for k,v in flags.items()):raise ValueError('已有 Release 类型不同；保持同一类型或在 Releases 页面显式修改状态')
    return release

def publish(api,directory,channel):
    manifest=load_payload(directory)
    if channel not in ('draft','prerelease','stable'):raise ValueError('未知 Release 类型')
    release=ensure_release(api,manifest,channel)
    for entry in manifest['assets']:
        path=Path(directory)/entry['name'];existing=next((x for x in api.request('/releases/'+str(release['id'])+'/assets?per_page=100') if x['name']==entry['name']),None)
        if existing:
            actual=existing.get('digest') or 'sha256:'+hashlib.sha256(api.binary('/releases/assets/'+str(existing['id']))).hexdigest()
            if actual!='sha256:'+entry['sha256']:raise ValueError('已发布同名文件的摘要不同，保留原文件：'+entry['name'])
            continue
        endpoint='/releases/'+str(release['id'])+'/assets?name='+urllib.parse.quote(entry['name'],safe='')
        try:uploaded=api.request(endpoint,'POST',path.read_bytes(),upload=True)
        except APIError as error:
            if error.status!=422:raise
            uploaded=next((x for x in api.request('/releases/'+str(release['id'])+'/assets?per_page=100') if x['name']==entry['name']),None)
            if not uploaded:raise ValueError('并行上传后未找到发布文件') from None
        actual=uploaded.get('digest') or 'sha256:'+hashlib.sha256(api.binary('/releases/assets/'+str(uploaded['id']))).hexdigest()
        if uploaded['size']!=entry['bytes'] or actual!='sha256:'+entry['sha256']:raise ValueError('GitHub 发布文件大小或摘要不同')
    return release

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory',type=Path)
    parser.add_argument('--artifact',default=os.environ.get('NETGO_RELEASE_ARTIFACT',''))
    parser.add_argument('--plan',action='store_true')
    args=parser.parse_args()
    try:
        if args.plan:
            if not args.directory:raise ValueError('发布计划需指定已构建的暂存目录')
            data=load_payload(args.directory);print(json.dumps({'tag':'v'+data['version'],'commit':data['commit'],'assets':[x['name'] for x in data['assets']]},ensure_ascii=False));return
        if os.environ.get('GITHUB_EVENT_NAME')!='workflow_dispatch' or not boolean(os.environ.get('NETGO_PUBLISH')):raise ValueError('未获得手动发布开关授权')
        api=GitHub(os.environ.get('GITHUB_REPOSITORY',''),os.environ.get('GITHUB_TOKEN',''))
        directory=args.directory or download_payload(api,os.environ['GITHUB_RUN_ID'],args.artifact,Path('.cache/release-download'))
        data=load_payload(directory)
        if data['commit']!=source_commit(require_git=True) or data['commit']!=os.environ.get('GITHUB_SHA'):raise ValueError('发布任务与构建提交不同')
        result=publish(api,directory,os.environ.get('NETGO_RELEASE_TYPE','prerelease'))
        print('Release 文件及摘要已核验：'+result['html_url'])
        if os.environ.get('GITHUB_STEP_SUMMARY'):
            with Path(os.environ['GITHUB_STEP_SUMMARY']).open('a',encoding='utf-8') as stream:stream.write('### 发布完成\n\n['+result['tag_name']+']('+result['html_url']+')\n')
    except (ValueError,APIError,KeyError) as error:parser.error(str(error))

if __name__=='__main__':main()
