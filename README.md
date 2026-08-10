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
implemented. It is not yet ready for operator deployment because multi-GEM,
autonomous notifications, tests, software lifecycle and real OLT
interoperability are incomplete; see [the implementation matrix](docs/implementation.md).

## Development

```sh
go test ./...
go test -race ./...
```

The project is Apache-2.0.  It uses the Apache-2.0
`github.com/opencord/omci-lib-go/v2` package for message and managed-entity
encoding.  No proprietary SDK source is included.
