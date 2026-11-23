#include "netgo_remote_ticket.h"
#include <fstream>
#include <string>
#include <vector>
#include <map>
#include <iostream>
#include <stdexcept>
int main(int argc,char** argv){
 try{
  if(argc!=2)return 2;
  std::ifstream file(argv[1]);
  std::map<std::string,std::vector<uint8_t>> v;
  std::string name,hex;
  while(file>>name>>hex){if(hex.size()%2)throw std::runtime_error("hex length");std::vector<uint8_t>b;for(size_t i=0;i<hex.size();i+=2)b.push_back(uint8_t(std::stoul(hex.substr(i,2),nullptr,16)));if(!v.emplace(name,b).second)throw std::runtime_error("duplicate");}
  auto verify=[&](){return ng_remote_possession_verify(v.at("key").data(),v.at("ticket").data(),v.at("ticket").size(),v.at("challenge").data(),v.at("channel").data(),v.at("proof").data());};
  if(v.at("key").size()!=32||v.at("ticket").size()!=168||v.at("challenge").size()!=32||v.at("channel").size()!=32||v.at("proof").size()!=64||verify()!=NG_REMOTE_OK)throw std::runtime_error("Go/C++ possession mismatch");
  unsigned checks=1;for(auto& [key,value]:v)for(auto& byte:value){byte^=1;if(verify()==NG_REMOTE_OK)throw std::runtime_error("accepted altered "+key);byte^=1;checks++;}
  if(ng_remote_possession_verify(nullptr,nullptr,0,nullptr,nullptr,nullptr)!=NG_REMOTE_INVALID_ARGUMENT)throw std::runtime_error("null inputs");
  std::cout<<"PASS "<<checks+1<<" possession assertions: ticket, challenge, TLS exporter, controller key and signature binding\n";return 0;
 }catch(const std::exception& e){std::cerr<<e.what()<<"\n";return 1;}
}
