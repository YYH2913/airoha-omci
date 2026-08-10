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
transactional MIB, baseline table transfer and core MIB audit operations are
implemented. Driver-originated alarm/AVC events have a validated upstream
notification path. Software download, CRC-32A and ImageHash validation,
activation and commit are implemented through a fixed platform helper ABI.
It is not yet ready for operator deployment because multi-GEM, complete
alarm/ARC coverage and real OLT interoperability are incomplete; see
[the implementation matrix](docs/implementation.md).

## Platform boundary

OMCI is required in addition to the xPON kernel data path. The daemon owns the
G.988 protocol, ONU MIB and OLT-facing state machines. Platform helpers own
only privileged Airoha and OpenWrt operations: GEM/T-CONT programming,
Ethernet/VLAN application, alarms, reboot/time control and persistent software
slots. This keeps proprietary SDK source out of the protocol repository while
still allowing the required hardware ABI to be implemented independently.

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
encoding.  No proprietary SDK source is included.
