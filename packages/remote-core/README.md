# NetGo remote session core

RD1a foundation, not a remote desktop implementation. This library is not linked
into the current VPN product and does not advertise a new product capability.

The C++17 static library has no third-party runtime dependencies, network socket,
background thread, codec or platform UI. Its public C ABI exposes opaque lifetime
handles, fixed-size versioned records and explicit result codes. C and C++ callers
are compiled and linked in the tests; JNI/ObjC++ adapters have not been built.

The gate enforces target/controller/boot binding, six separate capabilities,
default local consent, generation, increasing renewal sequence, bounded 30-second
leases, non-expanding renewal permissions and terminal revocation. Both wall and
monotonic clocks can end the lease. Invalid/expired grants never open a session.
Verification executes outside the mutex, then state is checked again so an
inflight renewal cannot reopen a revoked gate. No exception crosses the C ABI.

Build and test:

```sh
cmake -S packages/remote-core -B .cache/remote-core-build -DCMAKE_BUILD_TYPE=Release
cmake --build .cache/remote-core-build
ctest --test-dir .cache/remote-core-build --output-on-failure
```

Required integration before enabling a host:

- API v2 supplies the canonical signed-ticket encoding and concrete authenticated
  verifier; the test callback only injects claims and is never a production decoder.
- The trusted host registry atomically consumes session IDs/nonces across gates and
  restarts. The per-session gate alone does not stop cross-object replay.
- The host builds the binding digest from the authenticated site, controller key,
  host key and boot instance, then configures its locally allowed capabilities.
- Use one gate per session. Join all callers before destruction; callback contexts
  must outlive the gate. Clock callbacks must not reenter the gate.
- The host polls at least every 100 ms even without input and checks the required
  capability immediately before each media/input/clipboard/file operation. A state
  transition must stop its worker, drop queued events and release pressed keys.
  Any event-loop serialization needed to order revocation with in-flight platform
  actions belongs to the host adapter and requires separate integration tests.
- Revocation, local user-switch/lock and transport loss must explicitly close the
  session. No media, crypto adapter, persistence or platform implementation is
  supplied by this package yet.

Tests cover consent, capabilities, wrong bindings, malformed/expired tickets,
replay, renewal narrowing, wall-clock rollback, suspend, monotonic-clock failure,
revocation and a deterministic race with blocked verification. These are security
state-machine checks; they do not prove screen streaming or real-device support.
