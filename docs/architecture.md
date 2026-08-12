# Architecture

## Repository boundary

The OMCI protocol engine is a standalone repository because it must be usable
outside one OpenWrt tree, have host-side protocol tests, and be versioned
independently from kernel and LuCI changes.  The OpenWrt tree contains only:

* a package recipe pinned to a released source revision;
* the Airoha kernel control ABI and firmware;
* a procd/UCI/ubus adapter;
* LuCI configuration and diagnostics.

## Components

1. `transport` binds an `AF_PACKET/SOCK_RAW` socket to the kernel `omci`
   netdev for GPON, or a capability-negotiated secure character device for
   XGS-PON. Frames contain OMCI bytes directly, without an Ethernet header;
   only the device transport can attach trusted MIC-verification metadata.
2. `engine` validates the OMCI header, de-duplicates OLT transactions,
   dispatches requests and serializes responses.
3. `mib` owns ONU-created defaults and OLT-created managed entities.  Every
   mutation is validated before it becomes visible and advances MIB data sync.
   Its versioned, type-preserving snapshot is committed in the same platform
   transaction as the resolved service graph and restored after daemon failure.
4. `backend` translates service managed entities into a desired hardware and
   Linux network state, validates the complete dependency graph, then applies
   it atomically.
5. `status` publishes bounded diagnostics for ubus and LuCI without exposing
   OMCI credentials.
6. `event` consumes validated JSON lines from a fixed-path platform helper and
   maps hardware alarm, AVC and test-result events to upstream OMCI frames.
7. `onu3` defines the stable XG2010G status-record layout. The MIB engine owns
   circular-buffer semantics; the OpenWrt command transaction provides the
   non-volatile storage boundary.

The kernel is responsible for mode-specific TC/PLOAM, secure OMCC transport
and the low-level T-CONT/GEM or XGEM control ABI. G.988 policy remains in
userspace.

The event helper is a platform adapter, not part of the protocol engine. It is
started directly without a shell and cannot choose executables or command-line
arguments from an event payload. See [the event ABI](platform-events.md).
