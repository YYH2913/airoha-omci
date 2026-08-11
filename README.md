# Airoha OMCI

`airoha-omci` is an ONU-side ITU-T G.988 control plane for Airoha EN7581
devices.  It is intentionally separate from the OpenWrt target tree: the
protocol engine and its tests have an independent release cycle, while the
OpenWrt package supplies the platform backend, service supervision, UCI and
LuCI integration.

The daemon consumes raw OMCI frames from the Linux `omci` netdev exposed by
the Airoha GPON driver.  It owns the ONU MIB and translates OLT-created
T-CONT, GEM, bridge, VLAN and UNI managed entities into atomic platform
configuration transactions. Platform changes are committed to the MIB only
after the configured fixed-path helper accepts the complete candidate state.

## Status

This repository is under active development. The raw OMCC transport,
bounded priority scheduler, transactional MIB, baseline table transfer and
core MIB audit operations are implemented. Classes 287-289 expose the explicit
XG2010G ME, action, attribute, value-domain, alarm, AVC and live-instance
capability surface; every advertised enumeration has explicit code points and
device-specific fixed/ranged values are enforced before platform application;
known but unimplemented G.988 classes are rejected as unknown entities rather
than inherited from the protocol library. Driver-originated alarm/AVC events
have a validated upstream notification path. Software download, CRC-32A and
ImageHash validation, activation and commit are implemented through a fixed
platform helper ABI. Software slot changes use prepare/commit/abort transactions
so a rejected OMCI action cannot leave the boot slot ahead of the committed MIB.
ANI-G optical alarms and ARC, extended SetTable handling, the resolved
Ethernet service graph, and the common multi-profile Linux bridge/VLAN/TC
data path are implemented. Class 312 FEC, per-GEM and Ethernet UNI performance
monitoring share a synchronized 15-minute history and TCA engine. ONU3-G
advertises enhanced VLAN processing and implements persistent circular status
snapshots, Snap/Reset actions, Get/Get Next and M/K AVCs in both message sets.
The native
multicast runtime parses IGMPv1/v2/v3
and MLDv1/v2, enforces class 309/310 policy, runs the proxy querier and delayed
last-member state machine, and publishes live class 311 state. The daemon is
not yet ready for operator deployment
because advanced traffic-descriptor colour handling, hardware offload and real OLT
interoperability are incomplete; see
[the implementation matrix](docs/implementation.md).

## Platform boundary

OMCI is required in addition to the xPON kernel data path. The daemon owns the
G.988 protocol, ONU MIB and OLT-facing state machines. Platform helpers own
only privileged Airoha and OpenWrt operations: GEM/T-CONT programming,
Ethernet/VLAN application, alarms, reboot/time control and persistent software
slots. This keeps proprietary SDK source out of the protocol repository while
still allowing the required hardware ABI to be implemented independently.

XG2010G advertises ONU-G traffic-management option 0 deliberately. Its native
QDMA meter is shared by every GEM on a T-CONT, so advertising option 2 would
incorrectly promise per-connection upstream shaping. The platform still applies
compatible OLT traffic descriptors as per-T-CONT upstream meters, per-GEM
downstream red-drop policers and per-MAC-bridge-port aggregate policers without
overstating that hardware capability.

The helper contracts are documented in
[the platform ABI](docs/platform-abi.md). XG2010G software-slot storage and
power-loss recovery are described in
[the software lifecycle](docs/software-lifecycle.md).

## Development

```sh
go test ./...
go test -race ./...
```

The project is Apache-2.0.  It uses the Apache-2.0
`github.com/opencord/omci-lib-go/v2` package for message and managed-entity
encoding. The dependency is vendored because v2.2.4 has an incorrect ONU3-G
definition; the local correction is recorded in
[the dependency notes](docs/dependencies.md). No proprietary SDK source is
included.
