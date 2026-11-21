#!/usr/bin/env python3
"""Render reviewable configurations; never deploy or modify the host network."""
import argparse, base64, json, os, pathlib, re, secrets, urllib.parse
p=argparse.ArgumentParser()
p.add_argument('--config',required=True)
p.add_argument('--output',required=True)
p.add_argument('--mode',choices=['diagnostic','443'],default='diagnostic')
p.add_argument('--secrets',help='Existing protected JSON secrets for repeat renders')
a=p.parse_args()
c=json.loads(pathlib.Path(a.config).read_text(encoding='utf-8-sig'))
site=c['Site']; host=urllib.parse.urlparse(site['Origin']).hostname
relay=site['LocalRelay']; aux=(c.get('Auxiliaries') or [{}])[0]
def hostname(v):
    if not v or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{0,252}',v): raise ValueError('invalid host name')
    return v
host=hostname(host);relay=hostname(relay)
aux_host=hostname(aux.get('Host','aux.example.invalid'))
relay_host=hostname(urllib.parse.urlparse(aux.get('Relay','rels://relay.aux.example.invalid:443')).hostname)
tunnel_host=hostname(aux.get('TunnelHost','tunnel.aux.example.invalid'))
s=json.loads(pathlib.Path(a.secrets).read_text(encoding='utf-8')) if a.secrets else {'relay_secret':secrets.token_hex(32),'frp_secret':secrets.token_hex(32),'store_key':base64.b64encode(secrets.token_bytes(32)).decode(),'native_secret':secrets.token_hex(32)}
for k,v in s.items():
    if not isinstance(v,str) or not re.fullmatch(r'[A-Za-z0-9+/=_-]{32,128}',v): raise ValueError('invalid secret format')
values={'CONTROL_HOST':host,'CONTROL_PORT':'9443' if a.mode=='443' else '443','LOCAL_RELAY':relay,'PUBLIC_RELAY_PORT':'443' if a.mode=='443' else '8443','AUX_HOST':aux_host,'AUX_RELAY_HOST':relay_host,'TUNNEL_HOST':tunnel_host,'TUNNEL_PORT':'443' if a.mode=='443' else '7000','REMOTE_CONTROL_PORT':'24443' if a.mode=='443' else '8443','PROXY_BIND':'127.0.0.1' if a.mode=='443' else '0.0.0.0','RELAY_SECRET':s['relay_secret'],'FRP_SECRET':s['frp_secret'],'STORE_KEY':s['store_key'],'AUX_STUN':('    - uri: "stun:'+relay_host+':3478"') if aux else '', 'AUX_RELAY':('      - "rels://'+relay_host+':443"') if aux else ''}
root=pathlib.Path(__file__).resolve().parents[1]
out=pathlib.Path(a.output).resolve()
if out.exists(): raise ValueError('output directory must not exist; preserve previous render')
out.mkdir(mode=0o700,parents=True)
for source in (root/'deploy/templates').rglob('*.tmpl'):
    rel=source.relative_to(root/'deploy/templates')
    if not aux and (rel.parts[0]=='auxiliary' or source.name=='frpc.toml.tmpl'): continue
    if a.mode!='443' and source.name=='haproxy.cfg.tmpl': continue
    text=source.read_text(encoding='utf-8')
    for k,v in values.items(): text=text.replace('@@'+k+'@@',v)
    if re.search(r'@@[A-Z_]+@@',text): raise ValueError('unexpanded template')
    target=out/str(rel).removesuffix('.tmpl');target.parent.mkdir(parents=True,exist_ok=True)
    target.write_text(text,encoding='utf-8');os.chmod(target,0o600)
if a.mode=='443':
    (out/'local/relay-port.nft').write_text((root/'deploy/templates/auxiliary/relay-port.nft.tmpl').read_text(),encoding='utf-8')
(out/'secrets.json').write_text(json.dumps(s,indent=2)+'\n',encoding='utf-8');os.chmod(out/'secrets.json',0o600)
(out/'local/native.secret').write_text(s['native_secret']+'\n',encoding='utf-8');os.chmod(out/'local/native.secret',0o600)
(out/'README.txt').write_text('Generated for review. No services or firewall rules applied. Keep secrets.json for later renders. Copy only auxiliary/ to VPS; control TLS key and native.secret stay on local Linux. Validate configs with pinned executables before activation.\n',encoding='utf-8')
print(json.dumps({'output':str(out),'mode':a.mode,'auxiliary':bool(aux),'secrets':'protected files; not printed'}))
