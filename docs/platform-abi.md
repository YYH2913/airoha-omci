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
stdin and must apply it transactionally. ABI version 2 has this top-level
shape:

```json
{
  "version": 2,
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
the daemon's responsibility. Extended VLAN tables are emitted as named filter
and treatment fields, including enhanced row keys and directions; no encoded
table rows cross this boundary. Unknown ABI versions must be rejected before
any platform state is changed.

VLAN tagging filter data is similarly normalized. `forward_operation` is kept
for diagnostics, while `tagged_action` contains the G.988 action letter
(`a`, `c`, `g`, `h` or `j`), `tagged_criterion` is `none`, `vid`, `priority` or
`tci`, and `untagged_action` is `a` or `c`. Reserved forward-operation values
are rejected before the platform helper is called.

MAC bridge profiles carry their learning, spanning-tree, port-bridging,
unknown-MAC, timer and learning-depth policy. Each bridge port carries its
termination type and pointer, priority, path cost, spanning-tree state,
traffic-descriptor pointers and learning depth. A platform may reject a graph
that cannot be represented without loss, but it must do so before changing
hardware state. The XG2010G backend currently accepts one active bridge profile
with one ANI logical port and one or more Ethernet UNIs. It never removes an
interface from a non-OMCI Linux bridge implicitly.

XG2010G Ethernet PPTP UNI instances carry a fixed, validated Linux interface
name in the graph. The mapping is `0x0101` to `lan1`, `0x0102` to `lan2`,
`0x0103` to `lan3`, and `0x0104` to `lan4`. The platform helper must reject a
different name for one of these entity IDs rather than treating an OLT value as
an interface name.

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
