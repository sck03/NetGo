#include "remote_transport.hpp"
#include <openssl/evp.h>
#include <openssl/ssl.h>
#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
#else
#include <sys/socket.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/resource.h>
#endif
#include <atomic>
#include <chrono>
#include <future>
#include <iostream>
#include <stdexcept>
#include <thread>

using namespace netgo::remote;
namespace {
void require(bool ok,const char* why){if(!ok)throw std::runtime_error(why);}
#ifdef _WIN32
using Socket=SOCKET;constexpr Socket invalid=INVALID_SOCKET;
void closeSocket(Socket value){closesocket(value);}
#else
using Socket=int;constexpr Socket invalid=-1;
void closeSocket(Socket value){::close(value);}
#endif
struct RawTLS {
 Socket socket=invalid;std::unique_ptr<SSL_CTX,decltype(&SSL_CTX_free)> ctx{SSL_CTX_new(TLS_method()),SSL_CTX_free};std::unique_ptr<SSL,decltype(&SSL_free)> tls{nullptr,SSL_free};
 explicit RawTLS(uint16_t port){
  require(bool(ctx),"raw context");SSL_CTX_set_min_proto_version(ctx.get(),TLS1_3_VERSION);SSL_CTX_set_max_proto_version(ctx.get(),TLS1_3_VERSION);SSL_CTX_set_verify(ctx.get(),SSL_VERIFY_NONE,nullptr);
  socket=::socket(AF_INET,SOCK_STREAM,IPPROTO_TCP);require(socket!=invalid,"raw socket");sockaddr_in address{};address.sin_family=AF_INET;address.sin_port=htons(port);inet_pton(AF_INET,"127.0.0.1",&address.sin_addr);
  require(::connect(socket,reinterpret_cast<sockaddr*>(&address),sizeof(address))==0,"raw connect");tls.reset(SSL_new(ctx.get()));require(tls&&SSL_set_fd(tls.get(),int(socket))==1&&SSL_connect(tls.get())==1,"raw handshake");
 }
 ~RawTLS(){tls.reset();if(socket!=invalid)closeSocket(socket);}
 void write(const std::uint8_t* bytes,size_t size){size_t n=0;require(SSL_write_ex(tls.get(),bytes,size,&n)==1&&n==size,"raw write");}
};
std::array<std::uint8_t,12> header(Kind kind,unsigned size){return {'N','G','R','2',0,0,0,uint8_t(kind),uint8_t(size>>24),uint8_t(size>>16),uint8_t(size>>8),uint8_t(size)};}
void partialFrames(const Key& seed){
 auto live=[]{return true;};Listener listener("127.0.0.1",0,seed,live);
 auto peer=std::async(std::launch::async,[&]{return listener.accept(5000);});RawTLS raw(listener.port());auto connection=peer.get();require(bool(connection),"partial accept");
 auto h=header(Kind::video,8);Message message{Kind::ping,{99}};
 raw.write(h.data(),5);auto start=std::chrono::steady_clock::now();
 require(!connection->receive(message,0),"incomplete header delivered");require(std::chrono::steady_clock::now()-start<std::chrono::milliseconds(200),"partial header stalled media");
 require(message.kind==Kind::ping&&message.body==std::vector<uint8_t>{99},"partial read changed caller data");
 raw.write(h.data()+5,7);uint8_t body[8]{1,2,3,4,5,6,7,8};raw.write(body,4);start=std::chrono::steady_clock::now();
 require(!connection->receive(message,20),"incomplete body delivered");require(std::chrono::steady_clock::now()-start<std::chrono::milliseconds(200),"partial body stalled media");
 raw.write(body+4,4);require(connection->receive(message,1000)&&message.kind==Kind::video&&message.body==std::vector<uint8_t>(body,body+8),"partial frame reconstruction");
 auto empty=header(Kind::ping,0);raw.write(empty.data(),empty.size());raw.write(h.data(),h.size());raw.write(body,8);
 require(connection->receive(message,1000)&&message.kind==Kind::ping&&message.body.empty(),"empty frame");require(connection->receive(message,1000)&&message.body.size()==8,"back-to-back frames");
 raw.write(h.data(),1);require(!connection->receive(message,0),"slow header");
 for(int i=0;i<2;++i){std::this_thread::sleep_for(std::chrono::milliseconds(1100));raw.write(h.data()+i+1,1);require(!connection->receive(message,0),"slow incomplete header");}
 std::this_thread::sleep_for(std::chrono::milliseconds(1100));bool expired=false;try{connection->receive(message,0);}catch(const std::exception& e){expired=std::string(e.what())=="remote_transport_timeout";}
 require(expired,"slow sender reset the total message deadline");
}
void rejectOldFrames(const Key& seed){
 auto allowed=[]{return true;};Listener listener("127.0.0.1",0,seed,allowed);
 auto peer=std::async(std::launch::async,[&]{return listener.accept(5000);});RawTLS raw(listener.port());auto connection=peer.get();require(bool(connection),"old-frame accept");
 auto old=header(Kind::ping,0);old[3]='1';raw.write(old.data(),old.size());Message message{Kind::ping,{99}};bool rejected=false;
 try{connection->receive(message,1000);}catch(const std::exception& e){rejected=std::string(e.what())=="remote_invalid_message";}
 require(rejected&&message.body==std::vector<uint8_t>{99},"old protocol was accepted or changed caller data");
}
void brokenPeer(const Key& seed,const Key& publicKey){
 auto allowed=[]{return true;};Listener listener("127.0.0.1",0,seed,allowed);
 auto peer=std::async(std::launch::async,[&]{return listener.accept(5000);});
 auto client=Connection::connect("127.0.0.1",listener.port(),publicKey,allowed);auto server=peer.get();require(bool(server),"broken-peer accept");server.reset();
 bool rejected=false;for(int i=0;i<10&&!rejected;++i){try{client.send({Kind::video,std::vector<uint8_t>(256*1024,1)});}catch(const std::exception&){rejected=true;}}
 require(rejected,"closed peer did not report failure");
}
void highDescriptors(const Key& seed,const Key& publicKey){
#ifndef _WIN32
 struct Pad {
  std::vector<int> fds;rlimit limits{};bool restore=false;
  ~Pad(){for(auto fd:fds)::close(fd);if(restore)setrlimit(RLIMIT_NOFILE,&limits);}
 } pad;
 require(getrlimit(RLIMIT_NOFILE,&pad.limits)==0,"fd limit");
 if(pad.limits.rlim_max<1200)return;
 if(pad.limits.rlim_cur<1200){auto adjusted=pad.limits;adjusted.rlim_cur=1200;require(setrlimit(RLIMIT_NOFILE,&adjusted)==0,"fd test limit");pad.restore=true;}
 for(int i=0;i<1200;++i){auto fd=open("/dev/null",O_RDONLY|O_CLOEXEC);require(fd>=0,"fd pressure");pad.fds.push_back(fd);if(fd>=1050)break;}
 require(pad.fds.back()>=1050,"high fd unavailable");auto allowed=[]{return true;};Listener listener("127.0.0.1",0,seed,allowed);
 auto peer=std::async(std::launch::async,[&]{return listener.accept(5000);});auto client=Connection::connect("127.0.0.1",listener.port(),publicKey,allowed);auto server=peer.get();require(bool(server),"high fd accept");
 client.send({Kind::ping,{}});Message message;require(server->receive(message,1000)&&message.kind==Kind::ping,"high fd TLS exchange");
 std::cout<<"PASS real TLS with descriptors above 1024\n";
#else
 (void)seed;(void)publicKey;
#endif
}
}
int main(){
#ifdef _WIN32
 WSADATA w{};if(WSAStartup(MAKEWORD(2,2),&w))return 2;
#endif
 try{
 Key seed{},pub{};seed.fill(0x71);auto key=EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519,nullptr,seed.data(),seed.size());std::size_t size=pub.size();if(!key||EVP_PKEY_get_raw_public_key(key,pub.data(),&size)!=1)throw std::runtime_error("key");EVP_PKEY_free(key);
 std::atomic_bool alive=true;auto allowed=[&]{return alive.load();};Listener listener("127.0.0.1",0,seed,allowed);
 auto server=std::async(std::launch::async,[&]{auto c=listener.accept(5000);if(!c)throw std::runtime_error("accept");Message m;if(!c->receive(m,2000)||m.kind!=Kind::hello||m.body!=std::vector<std::uint8_t>{1,2,3})throw std::runtime_error("message");auto channel=c->exporter();c->send({Kind::challenge,{channel.begin(),channel.end()}});if(!c->receive(m,2000)||m.kind!=Kind::video||m.body.size()!=1024*1024)throw std::runtime_error("bounded media");c->send({Kind::accepted,{}});return channel;});
 auto client=Connection::connect("127.0.0.1",listener.port(),pub,allowed);client.send({Kind::hello,{1,2,3}});Message m;if(!client.receive(m,2000)||m.kind!=Kind::challenge)throw std::runtime_error("challenge");auto channel=client.exporter();if(m.body!=std::vector<std::uint8_t>(channel.begin(),channel.end()))throw std::runtime_error("exporter mismatch");client.send({Kind::video,std::vector<std::uint8_t>(1024*1024,7)});if(!client.receive(m,2000)||m.kind!=Kind::accepted||server.get()!=channel)throw std::runtime_error("exchange");
 bool rejected=false;try{client.send({Kind::video,std::vector<std::uint8_t>(max_message+1)});}catch(...){rejected=true;}if(!rejected)throw std::runtime_error("oversize accepted");
 auto peer=std::async(std::launch::async,[&]{return listener.accept(5000);});Key wrong{};wrong.fill(0x19);rejected=false;try{auto c=Connection::connect("127.0.0.1",listener.port(),wrong,allowed);}catch(const std::exception& e){rejected=std::string(e.what())=="remote_host_identity_failed";}auto accepted=peer.get();if(!accepted||!rejected)throw std::runtime_error("pin bypass");
 alive=false;rejected=false;try{client.send({Kind::ping,{}});}catch(const std::exception& e){rejected=std::string(e.what())=="remote_closed";}if(!rejected)throw std::runtime_error("revocation bypass");
 alive=true;rejected=false;try{Listener wildcard("0.0.0.0",0,seed,allowed);}catch(...){rejected=true;}if(!rejected)throw std::runtime_error("wildcard bind");
 partialFrames(seed);
 rejectOldFrames(seed);
 brokenPeer(seed,pub);
 highDescriptors(seed,pub);
 std::cout<<"PASS real TLS 1.3 loopback, device-key pin, exporter binding, bounded frames, cancellation and interface-only binding; no overlay/media acceptance\n";
 }catch(const std::exception& e){std::cerr<<e.what()<<"\n";return 1;}
#ifdef _WIN32
 WSACleanup();
#endif
 return 0;
}
