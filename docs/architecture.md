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
   netdev.  Frames contain OMCI bytes directly, without an Ethernet header.
2. `engine` validates the OMCI header, de-duplicates OLT transactions,
   dispatches requests and serializes responses.
3. `mib` owns ONU-created defaults and OLT-created managed entities.  Every
   mutation is validated before it becomes visible and advances MIB data sync.
4. `backend` translates service managed entities into a desired hardware and
   Linux network state, validates the complete dependency graph, then applies
   it atomically.
5. `status` publishes bounded diagnostics for ubus and LuCI without exposing
   OMCI credentials.

The kernel is responsible only for GPON/PLOAM, OMCC frame transport and the
low-level T-CONT/GEM control ABI.  G.988 policy remains in userspace.

