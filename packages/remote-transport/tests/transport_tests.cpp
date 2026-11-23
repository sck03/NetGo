#include "remote_transport.hpp"
#include <openssl/evp.h>
#ifdef _WIN32
#include <winsock2.h>
#endif
#include <atomic>
#include <chrono>
#include <future>
#include <iostream>
#include <stdexcept>
#include <thread>

using namespace netgo::remote;
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
 std::cout<<"PASS real TLS 1.3 loopback, device-key pin, exporter binding, bounded frames, cancellation and interface-only binding; no overlay/media acceptance\n";
 }catch(const std::exception& e){std::cerr<<e.what()<<"\n";return 1;}
#ifdef _WIN32
 WSACleanup();
#endif
 return 0;
}
