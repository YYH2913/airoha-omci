# Platform helper ABI

The daemon executes only fixed paths supplied by its administrator. OLT data
is serialized as validated arguments or JSON input; it is never interpreted
as a command name or shell fragment.

## Software helper

The executable configured with `-software-helper` implements four commands:

```text
helper state
helper download IMAGE_ID IMAGE_SIZE
helper activate IMAGE_ID ACTIVATE_FLAGS
helper commit IMAGE_ID
```

`IMAGE_ID` is 0 or 1. `download` reads exactly `IMAGE_SIZE` octets from stdin,
durably stages and validates them, then writes one JSON object to stdout:

```json
{"version":"2026.08","product_code":"XG2010G"}
```

`state` writes exactly one JSON object containing both software image MEs:

```json
{"images":[{"entity_id":0,"version":"old","product_code":"XG2010G","image_hash":"","committed":true,"active":true,"valid":true},{"entity_id":1,"version":"new","product_code":"XG2010G","image_hash":"00112233445566778899aabbccddeeff","committed":false,"active":false,"valid":true}]}
```

Image hashes are empty or 32 hexadecimal MD5 characters. Exactly one image is
active and exactly one is committed. A command reports failure with a non-zero
exit status and may write diagnostics to stderr. JSON is decoded strictly;
unknown fields or trailing values are rejected.

`activate` must arrange for the selected valid image to become active and may
reboot after returning. `commit` makes the active image persistent and removes
the automatic rollback condition. These operations must be idempotent because
an OLT may retransmit a request.

## Other helpers

The apply helper receives a complete, resolved candidate service graph on
stdin and must apply it transactionally. ABI version 1 has this top-level
shape:

```json
{
  "version": 1,
  "operation": "set-table",
  "mib_data_sync": 17,
  "service_graph": {
    "unis": [],
    "tconts": [],
    "gem_ports": [],
    "gem_interworking": [],
    "pbit_mappers": [],
    "bridges": [],
    "vlan_filters": [],
    "extended_vlans": []
  }
}
```

The graph contains validated and deterministically ordered references. Raw
managed-entity attributes are not part of this ABI; interpreting G.988 remains
the daemon's responsibility. Unknown ABI versions must be rejected before any
platform state is changed.

The control helper handles validated time and reboot operations. The event
helper streams bounded JSON lines for platform alarms and AVCs. Their schemas
are intentionally separate from the software helper so software-slot
privileges can be audited independently.

The control helper also accepts an `optical-line-supervision` action and returns
one strict JSON object containing an SFF-8472-compatible EN7572 sample:

```json
{"temperature":62976,"supply_voltage":33000,"laser_bias_current":2500,"transmit_power":10000,"receive_power":10}
```

All fields are unsigned 16-bit raw values. Temperature is signed two's
complement in 1/256 C units, supply voltage is in 100 uV units, laser bias is
in 2 uA units, and transmit/receive powers are in 0.1 uW units. The protocol
engine performs the G.988 dBu, voltage and temperature conversions. Unknown,
missing or trailing JSON fields are rejected.
