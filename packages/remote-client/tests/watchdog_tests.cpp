#include "netgo_remote_watchdog.hpp"
#include <atomic>
#include <iostream>
#include <stdexcept>
#include <thread>
std::atomic<std::uint64_t> now{1000};
std::uint64_t clockSample() { return now.load(); }
void check(bool value) { if (!value) throw std::runtime_error("watchdog invariant failed"); }
int main() { try {
 using netgo::remote::Watchdog;
 Watchdog normal(clockSample);now=6999;check(normal.live());check(normal.refresh());
 now=12998;check(normal.live());now=12999;check(!normal.live());check(!normal.refresh());
 now=1000;Watchdog late(clockSample);now=7000;check(!late.refresh());check(!late.live());
 now=1000;Watchdog suspended(clockSample);now=61000;check(!suspended.refresh());
 now=1000;Watchdog backwards(clockSample);now=999;check(!backwards.refresh());now=1001;check(!backwards.live());
 now=UINT64_MAX-1;Watchdog overflow(clockSample);check(!overflow.live());
 for (int i=0;i<100;++i) {
  now=1000;Watchdog closed(clockSample);now=2000;
  std::thread response([&]{closed.refresh();});closed.close();response.join();
  check(!closed.live());check(!closed.refresh());
 }
 std::cout << "PASS expiry boundary, delayed response, suspend, clock rollback, overflow and concurrent close\n";
 return 0;
 } catch (const std::exception& e) { std::cerr << e.what() << '\n';return 1; }
}
