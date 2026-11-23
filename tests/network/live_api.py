#!/usr/bin/env python3
"""Exercise real TLS, identity, signed API requests and account isolation in a lab."""
import argparse
import base64
from datetime import datetime, timezone
import getpass
import hashlib
import json
from pathlib import Path
import secrets
import ssl
import sys
import time
import urllib.error
import urllib.request

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat


def b64(data):return base64.urlsafe_b64encode(data).decode().rstrip('=')
def sha(data):return hashlib.sha256(data).hexdigest()


class ApiError(RuntimeError):
    def __init__(self,status,error):
        self.status=status;self.error=error
        super().__init__('HTTP '+str(status)+': '+str(error))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,req,fp,code,msg,headers,newurl):return None


class Session:
    def __init__(self,profile,client_type='admin'):
        self.profile=profile;self.client_type=client_type
        context=ssl.create_default_context()
        if profile.get('CA'):context.load_verify_locations(cadata=profile['CA'])
        self.http=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),urllib.request.HTTPSHandler(context=context))
        self.key=Ed25519PrivateKey.generate();self.wireguard_key=base64.b64encode(secrets.token_bytes(32)).decode()
        self.token='';self.refresh_token='';self.user=None;self.device_id=None

    def raw(self,path,method='GET',data=None,headers=None):
        request=urllib.request.Request(self.profile['Origin']+path,data=data,method=method,
                                      headers=headers or {'X-NetGo-Protocol':'2','Content-Type':'application/json'})
        try:
            with self.http.open(request,timeout=25) as response:
                payload=response.read(4*1024*1024)
                return response.status,json.loads(payload) if payload else None
        except urllib.error.HTTPError as error:
            try:body=json.loads(error.read(65536))
            except (ValueError,UnicodeError):body={}
            return error.code,body

    def headers(self,path,method,data):
        headers={'X-NetGo-Protocol':'2','Content-Type':'application/json'}
        if self.token:
            stamp=str(int(time.time()));nonce=secrets.token_urlsafe(32)
            proof='\n'.join([method,path,stamp,nonce,sha(data or b''),sha(self.token.encode())])
            headers.update(Authorization='Bearer '+self.token,**{'X-NetGo-Time':stamp,'X-NetGo-Nonce':nonce,
                           'X-NetGo-Signature':b64(self.key.sign(proof.encode()))})
        return headers

    def call(self,path,method='GET',body=None,extra_headers=None):
        data=None if body is None else json.dumps(body,separators=(',',':')).encode()
        headers=self.headers(path,method,data)
        headers.update(extra_headers or {})
        status,result=self.raw(path,method,data,headers)
        if not 200<=status<300:raise ApiError(status,(result or {}).get('error','request rejected'))
        return result

    def login(self,alias,password):
        self.token='';self.refresh_token=''
        health=self.call('/netgo/v2/health')
        if health['protocol']!=2 or health['site_id']!=self.profile['ID']:raise ValueError('site/protocol mismatch')
        verifier=secrets.token_urlsafe(32)
        tx=self.call('/netgo/v2/auth/transactions','POST',{'client_type':self.client_type,'site_id':self.profile['ID'],
            'public_key':b64(self.key.public_key().public_bytes(Encoding.Raw,PublicFormat.Raw)),
            'wireguard_key':self.wireguard_key,'challenge':b64(hashlib.sha256(verifier.encode()).digest())})
        proof='\n'.join(['netgo-native-v1',self.profile['ID'],tx['id'],tx['nonce']])
        self.call('/netgo/v2/auth/transactions/'+tx['id']+'/complete','POST',{'Alias':alias,'Password':password,
            'Signature':b64(self.key.sign(proof.encode()))})
        result=self.call('/netgo/v2/auth/transactions/'+tx['id']+'/exchange','POST',{'Verifier':verifier,
            'Signature':b64(self.key.sign((proof+'\nexchange\n'+verifier).encode()))})
        self.token=result['token'];self.refresh_token=result.get('refresh_token','');self.user=result['user'];self.device_id=result['device_id']
        return result

    def refresh(self):
        result=self.call('/netgo/v2/auth/refresh','POST',{'refresh_token':self.refresh_token})
        self.token=result['token'];self.refresh_token=result.get('refresh_token','')
        return result


def require(condition,message):
    if not condition:raise AssertionError(message)


def rejected(action,status=None,error=None):
    try:action()
    except ApiError as failure:
        require(status is None or failure.status==status,'unexpected rejection status')
        require(error is None or failure.error==error,'unexpected rejection reason')
        return
    raise AssertionError('operation unexpectedly succeeded')


def exercise(profile,alias,password,output):
    records=[];admin=Session(profile);user=None;resource=None
    def check(name,action):
        started=time.monotonic();record={'name':name,'status':'FAIL'}
        try:action();record['status']='PASS'
        except BaseException as error:
            record['error']=str(error);raise
        finally:
            record['seconds']=round(time.monotonic()-started,3);records.append(record)
            print(record['status']+' '+name,flush=True)
    try:
        check('trusted_https_health',lambda:require(admin.call('/netgo/v2/health')['site_id']==profile['ID'],'wrong site'))
        check('bootstrap_and_native_backend_not_public',lambda:require(all(admin.raw(path,'POST',b'{}')[0]==404 for path in ('/api/setup','/oauth2/netgo/native')),'private endpoint exposed'))
        check('protocol_required',lambda:require(admin.raw('/netgo/v2/site',headers={'Content-Type':'application/json'})[0]==426,'missing protocol accepted'))
        check('admin_requires_authentication',lambda:rejected(lambda:admin.call('/netgo/v2/admin/state'),401))
        check('wrong_password_rejected',lambda:rejected(lambda:Session(profile).login(alias,secrets.token_urlsafe(24)),401))
        check('real_dex_administrator_login',lambda:require(admin.login(alias,password)['user']['role']=='admin','wrong role'))
        check('signed_administrator_state',lambda:require(admin.user['id'] in admin.call('/netgo/v2/admin/state')['users'],'administrator not registered'))
        def replay():
            path='/netgo/v2/admin/state';headers=admin.headers(path,'GET',None)
            require(admin.raw(path,headers=headers)[0]==200,'signed request failed')
            require(admin.raw(path,headers=headers)[0]==401,'replayed nonce accepted')
        check('request_replay_rejected',replay)
        def refresh():
            path='/netgo/v2/admin/state';old=admin.headers(path,'GET',None)
            admin.refresh()
            require(admin.raw(path,headers=old)[0]==401,'old bearer credential retained')
            admin.call(path)
        check('real_refresh_rotates_credentials',refresh)
        suffix=secrets.token_hex(5);test_alias='lab'+suffix
        created={}
        def create_user():
            nonlocal user
            created.update(admin.call('/netgo/v2/admin/users','POST',{'Alias':test_alias,'Email':test_alias+'@netgo.invalid','Name':'NetGo acceptance user'}))
            user=created.get('user')
            require(bool(created.get('initial_password')),'local IdP did not return an initial password')
        check('create_real_local_user',create_user)
        user=created['user'];user_password=created['initial_password'];normal=Session(profile,'')
        check('new_user_has_no_resources',lambda:require(user['resources']==[],'new user has unexpected LAN permissions'))
        check('normal_user_login',lambda:normal.login(test_alias,user_password))
        check('normal_user_admin_denied',lambda:rejected(lambda:normal.call('/netgo/v2/admin/state'),403))
        rid='lab'+suffix
        resource={'id':rid,'name':'Isolated lab HTTP','cidr':'198.19.240.2/32','protocol':'tcp','ports':[18080],'enabled':True}
        check('resource_create',lambda:admin.call('/netgo/v2/admin/resources/'+rid,'PUT',resource))
        user['resources']=[rid];user['max_devices']=1
        check('grant_explicit_resource',lambda:admin.call('/netgo/v2/admin/users/'+user['id'],'PUT',user))
        check('permission_edit_invalidates_old_credential',lambda:rejected(lambda:normal.call('/netgo/v2/me'),401))
        check('relogin_same_device_after_policy_change',lambda:normal.login(test_alias,user_password))
        check('device_quota_enforced',lambda:rejected(lambda:Session(profile,'').login(test_alias,user_password),error='device_limit'))
        def exported():
            public=normal.call('/netgo/v2/site');raw=json.dumps(public)
            require(public['ID']==profile['ID'] and public['CA']==profile['CA'],'profile identity/trust differs')
            require(all(value not in raw for value in (password,user_password,admin.token,normal.token)),'secret in public profile')
        check('authenticated_profile_contains_no_secrets',exported)
        user['disabled']=True
        check('disable_account',lambda:admin.call('/netgo/v2/admin/users/'+user['id'],'PUT',user))
        check('disabled_account_cannot_refresh',lambda:rejected(normal.refresh,401))
        check('disabled_account_cannot_login',lambda:rejected(lambda:Session(profile,'').login(test_alias,user_password),401))
        user['disabled']=False;user['expires_at']='2020-01-01T00:00:00Z'
        check('expired_account_policy',lambda:admin.call('/netgo/v2/admin/users/'+user['id'],'PUT',user))
        check('expired_account_cannot_login',lambda:rejected(lambda:Session(profile,'').login(test_alias,user_password),401))
        check('last_administrator_protected',lambda:rejected(lambda:admin.call('/netgo/v2/admin/users/'+admin.user['id'],'DELETE')))
        def diagnostics():
            raw=json.dumps(admin.call('/netgo/v2/admin/diagnostics'))
            require(all(value not in raw for value in (password,user_password,admin.token,admin.refresh_token)),'secret in diagnostics')
        check('diagnostics_redacted',diagnostics)
    finally:
        cleanup_errors=[]
        if admin.token and user:
            try:check('delete_test_user',lambda:admin.call('/netgo/v2/admin/users/'+user['id'],'DELETE'))
            except BaseException as error:cleanup_errors.append(str(error))
        if admin.token and resource:
            try:check('delete_test_resource',lambda:admin.call('/netgo/v2/admin/resources/'+resource['id'],'DELETE'))
            except BaseException as error:cleanup_errors.append(str(error))
        output.parent.mkdir(parents=True,exist_ok=True)
        output.write_text(json.dumps({'schema':1,'kind':'real TLS/IdP/API lab','generated_utc':datetime.now(timezone.utc).isoformat(),
            'status':'PASS' if records and all(r['status']=='PASS' for r in records) else 'FAIL','checks':records,
            'limits':['This API suite does not prove tunnel traffic, NAT, VPS or Windows service installation']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        if cleanup_errors:raise RuntimeError('test object cleanup incomplete; see evidence report')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--site',type=Path,required=True)
    parser.add_argument('--alias',required=True)
    parser.add_argument('--password-stdin',action='store_true')
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    if args.output.exists():parser.error('use a new evidence output file')
    password=sys.stdin.readline().rstrip('\r\n') if args.password_stdin else getpass.getpass('Administrator password: ')
    try:exercise(json.loads(args.site.read_text(encoding='utf-8-sig')),args.alias,password,args.output)
    except (OSError,ValueError,AssertionError,RuntimeError) as error:
        print('Live API test failed: '+str(error),file=sys.stderr);return 1
    return 0


if __name__=='__main__':raise SystemExit(main())
