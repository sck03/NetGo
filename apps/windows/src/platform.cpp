#include "common.hpp"
#include <wincred.h>
#include <wincrypt.h>
#include <bcrypt.h>
#include <sddl.h>
#include <aclapi.h>
#include <fstream>

namespace netgo {
std::wstring wide(const std::string& s) {
 if(s.empty()) return {};
 int n=MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,s.data(),int(s.size()),nullptr,0);
 if(!n) throw Error("invalid_utf8");
 std::wstring v(n,L'\0'); MultiByteToWideChar(CP_UTF8,MB_ERR_INVALID_CHARS,s.data(),int(s.size()),v.data(),n); return v;
}
std::string utf8(const std::wstring& s) {
 if(s.empty()) return {};
 int n=WideCharToMultiByte(CP_UTF8,WC_ERR_INVALID_CHARS,s.data(),int(s.size()),nullptr,0,nullptr,nullptr);
 if(!n) throw Error("invalid_unicode");
 std::string v(n,'\0'); WideCharToMultiByte(CP_UTF8,WC_ERR_INVALID_CHARS,s.data(),int(s.size()),v.data(),n,nullptr,nullptr); return v;
}
std::string sid(HANDLE process) {
 HANDLE raw=nullptr;
 if(!OpenProcessToken(process,TOKEN_QUERY,&raw)) throw Error("user_identity_unavailable");
 Handle token(raw); DWORD bytes=0; GetTokenInformation(token,TokenUser,nullptr,0,&bytes);
 std::vector<unsigned char> b(bytes);
 if(!GetTokenInformation(token,TokenUser,b.data(),bytes,&bytes)) throw Error("user_identity_unavailable");
 LPWSTR value=nullptr;
 if(!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(b.data())->User.Sid,&value)) throw Error("user_identity_unavailable");
 std::string result=utf8(value); LocalFree(value); return result;
}
std::filesystem::path dataRoot() {
 if(std::filesystem::is_regular_file(executableDir()/L"netgo-portable.json"))return executableDir()/L"data";
 wchar_t p[32768]; DWORD n=GetEnvironmentVariableW(L"LOCALAPPDATA",p,32768);
 if(!n || n>=32768) throw Error("local_appdata_unavailable");
 return std::filesystem::path(p)/L"NetGoNative";
}
std::filesystem::path executableDir() {
 std::wstring p(32768,L'\0'); DWORD n=GetModuleFileNameW(nullptr,p.data(),DWORD(p.size()));
 if(!n || n==p.size()) throw Error("executable_path_unavailable");
 p.resize(n); return std::filesystem::path(p).parent_path();
}
void secureDirectory(const std::filesystem::path& p) {
 std::filesystem::create_directories(p);
 PSECURITY_DESCRIPTOR desc=nullptr;
 auto s=wide("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;"+sid()+")");
 if(!ConvertStringSecurityDescriptorToSecurityDescriptorW(s.c_str(),SDDL_REVISION_1,&desc,nullptr)) throw Error("storage_acl_failed");
 BOOL present=FALSE,defaulted=FALSE; PACL acl=nullptr; GetSecurityDescriptorDacl(desc,&present,&acl,&defaulted);
 DWORD e=SetNamedSecurityInfoW(const_cast<wchar_t*>(p.c_str()),SE_FILE_OBJECT,DACL_SECURITY_INFORMATION|PROTECTED_DACL_SECURITY_INFORMATION,nullptr,nullptr,acl,nullptr);
 LocalFree(desc); if(e!=ERROR_SUCCESS) throw Error("storage_acl_failed");
}
std::string readFile(const std::filesystem::path& p,size_t limit) {
 std::ifstream in(p,std::ios::binary); if(!in) throw Error("file_unavailable");
 std::string result; char b[4096];
 while(in) {in.read(b,sizeof(b)); result.append(b,size_t(in.gcount())); if(result.size()>limit) throw Error("file_too_large");}
 return result;
}
void atomicFile(const std::filesystem::path& p,const std::string& value) {
 auto temp=p; temp+=wide("."+randomID()+".tmp");
 {std::ofstream out(temp,std::ios::binary|std::ios::trunc); if(!out) throw Error("storage_write_failed"); out.write(value.data(),std::streamsize(value.size())); out.flush(); if(!out) {std::filesystem::remove(temp);throw Error("storage_write_failed");}}
 if(!MoveFileExW(temp.c_str(),p.c_str(),MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)) {std::filesystem::remove(temp);throw Error("storage_commit_failed");}
}
void wipe(std::string& s) {if(!s.empty()) SecureZeroMemory(s.data(),s.size());s.clear();}
static std::wstring credentialTarget(const std::string& target) {
 // Portable folders have separate trust configuration; never reuse another
 // installation's remembered password merely because a site ID is the same.
 auto scope=sha256(utf8(dataRoot().wstring())).substr(0,32);
 return wide("NetGo2/"+scope+"/"+target);
}
std::string credentialRead(const std::string& target) {
 PCREDENTIALW c=nullptr;
 if(!CredReadW(credentialTarget(target).c_str(),CRED_TYPE_GENERIC,0,&c)) {
  if(GetLastError()==ERROR_NOT_FOUND) return {};
  throw Error("credential_read_failed");
 }
 std::string result(reinterpret_cast<char*>(c->CredentialBlob),c->CredentialBlobSize); CredFree(c); return result;
}
void credentialWrite(const std::string& target,const std::string& value) {
 if(value.size()>CRED_MAX_CREDENTIAL_BLOB_SIZE) throw Error("credential_too_large");
 std::wstring name=credentialTarget(target); CREDENTIALW c{};
 c.Type=CRED_TYPE_GENERIC;c.TargetName=name.data();c.CredentialBlobSize=DWORD(value.size());
 c.CredentialBlob=reinterpret_cast<LPBYTE>(const_cast<char*>(value.data()));c.Persist=CRED_PERSIST_LOCAL_MACHINE;
 if(!CredWriteW(&c,0)) throw Error("credential_write_failed");
}
void credentialDelete(const std::string& target) {
 if(!CredDeleteW(credentialTarget(target).c_str(),CRED_TYPE_GENERIC,0)&&GetLastError()!=ERROR_NOT_FOUND) throw Error("credential_delete_failed");
}
std::string base64url(const unsigned char* data,size_t size) {
 if(size==0)return {};
 if(size>MAXDWORD)throw Error("encoding_too_large");
 DWORD count=0;constexpr DWORD flags=CRYPT_STRING_BASE64|CRYPT_STRING_NOCRLF;
 if(!CryptBinaryToStringA(data,DWORD(size),flags,nullptr,&count))throw Error("encoding_failed");
 std::string result(count,'\0');
 if(!CryptBinaryToStringA(data,DWORD(size),flags,result.data(),&count))throw Error("encoding_failed");
 result.resize(count);
 for(char& c:result){if(c=='+')c='-';else if(c=='/')c='_';}
 while(!result.empty()&&result.back()=='=')result.pop_back();
 return result;
}
std::string randomID() {
 unsigned char bytes[32];
 if(BCryptGenRandom(nullptr,bytes,sizeof(bytes),BCRYPT_USE_SYSTEM_PREFERRED_RNG)<0)throw Error("random_failed");
 return base64url(bytes,sizeof(bytes));
}
std::string sha256(const std::string& value,bool url) {
 if(value.size()>MAXDWORD)throw Error("hash_input_too_large");
 unsigned char digest[32];
 if(BCryptHash(BCRYPT_SHA256_ALG_HANDLE,nullptr,0,reinterpret_cast<PUCHAR>(const_cast<char*>(value.data())),ULONG(value.size()),digest,sizeof(digest))<0)throw Error("hash_failed");
 if(url)return base64url(digest,sizeof(digest));
 constexpr char hex[]="0123456789abcdef";std::string result(64,'0');
 for(size_t i=0;i<sizeof(digest);++i){result[2*i]=hex[digest[i]>>4];result[2*i+1]=hex[digest[i]&15];}
 return result;
}
}
