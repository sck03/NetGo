#include "connection.hpp"
#include <openssl/evp.h>

namespace netgo {
static std::unique_ptr<EVP_PKEY,decltype(&EVP_PKEY_free)> key(const std::string& seed) {
 if(seed.size()!=32) throw Error("invalid_device_key");
 auto p=EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,reinterpret_cast<const unsigned char*>(seed.data()),seed.size());
 if(!p) throw Error("device_key_failed");
 return {p,EVP_PKEY_free};
}
std::string publicKey(const std::string& seed) {
 auto k=key(seed);unsigned char b[32];size_t n=sizeof(b);
 if(EVP_PKEY_get_raw_public_key(k.get(),b,&n)!=1) throw Error("device_key_failed");
 return base64url(b,n);
}
std::string signature(const std::string& seed,const std::string& s) {
 auto k=key(seed);std::unique_ptr<EVP_MD_CTX,decltype(&EVP_MD_CTX_free)> ctx(EVP_MD_CTX_new(),EVP_MD_CTX_free);
 unsigned char b[64];size_t n=sizeof(b);
 if(!ctx||EVP_DigestSignInit(ctx.get(),nullptr,nullptr,nullptr,k.get())!=1||EVP_DigestSign(ctx.get(),b,&n,reinterpret_cast<const unsigned char*>(s.data()),s.size())!=1) throw Error("signature_failed");
 return base64url(b,n);
}
std::string signedMessage(const std::string& method,const std::string& path,const std::string& stamp,const std::string& nonce,const std::string& body,const std::string& token) {
 return method+"\n"+path+"\n"+stamp+"\n"+nonce+"\n"+sha256(body)+"\n"+sha256(token);
}
}
