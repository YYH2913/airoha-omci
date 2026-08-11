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

The apply helper receives a complete, resolved candidate service graph and its
corresponding MIB state on stdin and must commit them transactionally. ABI
version 6 has this top-level
shape:

```json
{
  "version": 6,
  "operation": "set-table",
  "mib_data_sync": 17,
  "mib_state": {
    "version": 1,
    "mib_data_sync": 17,
    "instances": [{
      "class_id": 2,
      "entity_id": 0,
      "origin": 0,
      "attributes": [{
        "name": "MibDataSync",
        "kind": "uint8",
        "unsigned": 17
      }]
    }]
  },
  "service_graph": {
    "unis": [],
    "tconts": [{
      "entity_id": 32768,
      "alloc_id": 1024,
      "scheduler_policy": 2,
      "scheduler_weight": 0,
      "queue_entities": [32768,32769,32770,32771,32772,32773,32774,32775],
      "queue_weights": [1,1,2,2,4,4,8,8]
    }],
    "traffic_descriptors": [{
      "entity_id": 34816,
      "cir": 125000000,
      "pir": 312500000,
      "cbs": 65536,
      "pbs": 131072,
      "colour_mode": 0,
      "ingress_colour_marking": 0,
      "egress_colour_marking": 0,
      "meter_type": 0
    }],
    "dot1_rate_limiters": [{
      "entity_id": 35072,
      "parent_me": 256,
      "tp_type": 1,
      "upstream_unicast_flood_traffic_descriptor": 34816,
      "upstream_broadcast_traffic_descriptor": 65535,
      "upstream_multicast_payload_traffic_descriptor": 65535
    }],
    "gem_ports": [],
    "gem_interworking": [],
    "multicast_gem_interworking": [],
    "multicast_operations_profiles": [],
    "multicast_subscribers": [],
    "pbit_mappers": [],
    "bridges": [],
    "vlan_filters": [],
    "extended_vlans": []
  }
}
```

The graph contains validated and deterministically ordered references. The
versioned `mib_state` is an opaque daemon recovery record; the platform backend
must not interpret it as hardware configuration. Attribute values carry an
explicit scalar, octet, integer-array or table type so JSON cannot erase their
G.988 representation. Extended VLAN tables are emitted in `service_graph` as
named filter and treatment fields, including enhanced row keys and directions;
no encoded table rows cross the hardware boundary. ABI 6 adds the normalized
class-298 parent and class-280 traffic-descriptor pointers. It retains the class
309 ACLs and class 310 service-package/allowed-preview rows introduced by ABI 4
as normalized logical entries. Set
control, row-part, test and reserved bits never cross the boundary. Unknown ABI
versions must be rejected before any platform state is changed.

Each `unis[].administrative_state` is the effective hardware state, not merely
the Ethernet PPTP attribute. It is locked when any of ONU-G, the containing
circuit pack, the PPTP or the same-ID UNI-G is locked. The individual G.988
attributes remain distinct in `mib_state`, so removing a parent lock restores
the child's independently provisioned state. The helper applies this field only
to `lan1` through `lan4`; it must not take the PON or OMCC transport down.

For Create, Set, Set table, Delete, Reset and autonomous service changes, the
OpenWrt helper applies the candidate graph and atomically replaces
`desired.json` only after hardware programming succeeds. The `command`
operation records software-image lifecycle MIB changes by atomically replacing
the document without reapplying its unchanged graph. On daemon startup, a
committed state is accepted only when its ONU vendor/serial, factory MEs,
MIB-data-sync value and graph reconstructed from the MIB all match. Otherwise
the daemon commits a factory reset, which also removes the stale platform graph.

`airoha-mcastd -validate FILE` applies the same strict JSON decoder, graph
resolution and G.988 multicast policy compiler without opening packet sockets
or changing Linux state. The transactional OpenWrt apply helper runs this
validation before programming a candidate graph.

At runtime the multicast daemon watches the root-only, atomically replaced
`/var/run/airoha-omcid/desired.json`. For each configured subscriber it writes
one atomic class-311 monitor document to
`/var/run/airoha-omci/multicast/ENTITY.json`:

```json
{
  "multicast_subscriber_id": 1280,
  "current_bandwidth": 1000000,
  "join_messages": 2,
  "bandwidth_exceeded": 0,
  "groups": [{
    "source": "0.0.0.0",
    "group": "239.1.1.1",
    "client": "192.0.2.10",
    "uni_tagged": true,
    "uni_vlan": 100,
    "ani_vlan": 200,
    "profile_id": 1792,
    "acl_row_key": 1,
    "gem_port_id": 201,
    "imputed_bandwidth": 1000000,
    "time_since_join": 15
  }]
}
```

The control helper verifies the requested entity ID against the document and
the daemon-side controller rejects unknown, missing or trailing JSON fields.

OMCI traffic management is part of the same transaction. The XG2010G OpenWrt
backend applies the following native mapping atomically before acknowledging
the OMCI mutation:

| G.988 object | EN7581/QDMA object | Required operation |
| --- | --- | --- |
| T-CONT (class 262) | GPON Alloc-ID and WAN channel | allocate or release the T-CONT slot |
| GEM CTP (class 268) | GPON GEM table and channel direction | enable GEM, encryption and GEM/mark mapping |
| Priority queue (class 277) | QDMA WAN channel queue 0-7 | set SP/WRR mode and weight |
| Traffic descriptor (class 280) | QDMA TRTCM meter, PON ingress GEM policer or Linux bridge-port policer | set CIR/PIR/CBS/PBS and enable/disable |
| Dot1 rate limiter (class 298) | Linux bridge ANI egress flower/police rules | police unknown-unicast flood, broadcast and multicast payload independently |

The SDK names these capabilities `qdmamgr_lib_set_channel_qos`,
`qdmamgr_lib_set_ratectl_trtcm_mode_*` and `gpon_flow_public` flow records.
Those proprietary headers are reference material only; they are not copied
into this Apache-2.0 repository. The OpenWrt adapter exposes an equivalent
complete-table ABI with prepare/apply/rollback behavior. It supports 16 GPON
QDMA channels; the current factory MIB advertises eight T-CONTs. Channels 0
through 3 share allocation state with regular Linux qdiscs, while GPON-only
channels 4 through 15 do not consume one of the four ordinary netdev QoS
slots.

Scheduler policy 0 (null) and 1 (strict priority) map to EN7581 SP; policy 2
maps to WRR8 and requires at least one non-zero queue weight. Class-280 CIR and
PIR values are G.988 bytes/s and are converted by the driver to kbit/s with
`bytes/s * 8 / 1000`; CBS and PBS remain bytes. One egress TRTCM meter exists
per T-CONT channel, so all GEMs sharing a T-CONT must use the same upstream
traffic descriptor. The factory MIB therefore advertises ONU-G
traffic-management option 0; option 2 would incorrectly claim per-connection
upstream shaping. A downstream GEM descriptor is selected by the receive GEM
Port-ID in the reserved skb mark and applies a `pon` ingress PIR/PBS red-drop
policer before bridge or direct delivery. A zero PIR selects the unlimited
factory policy. When a non-zero CIR or PIR has a zero burst, the XG2010G
factory burst is one maximum Ethernet frame (2000 bytes). Colour-aware marking
or remarking and RFC 4115 coupling are rejected explicitly because the current
adapter cannot represent them faithfully.

MAC bridge port class-47 outbound and inbound traffic-descriptor pointers use
the same supported class-280 profile. `outbound` limits traffic leaving the
bridge on the resolved UNI or profile ANI endpoint and maps to interface
egress; `inbound` limits traffic entering the bridge and maps to ingress. The
platform ABI emits `port-meter INTERFACE HOOK PIR:PBS`. A zero PIR omits the
meter. The class-47 wire/default null pointer `0` is normalized to the platform
ABI null value `0xffff`. Logical ANI ports collapsed onto one endpoint must
carry identical descriptor pointers.

Class 298 is resolved against either a MAC bridge service profile or an IEEE
802.1p mapper. At most one limiter may reference a given parent, and every
non-null category pointer must resolve to class 280. For an active XG2010G MAC
bridge, the limiter runs on the profile-specific `omaXXXX` egress endpoint after
the Linux bridge has made its FDB/MDB decision. Unknown unicast matches
`l2_miss=1` with a unicast destination, broadcast matches the exact all-ones MAC
with `l2_miss=0`, and multicast payload matches `l2_miss=1` with a group
destination. These matches are disjoint. The target kernel enables
`CONFIG_NET_TC_SKB_EXT`, and the package requires the flower and police actions.

The current policer preserves the class-280 G.988 byte units until apply, then
uses `PIR * 8` bit/s and PBS bytes as the red-drop bucket. It accepts only
colour-blind descriptors without ingress or egress remarking and rejects RFC
4115 coupling. CIR/CBS remain available to the native QDMA TRTCM path but Linux
does not preserve a green/yellow colour for later queues. A direct mapper with
a non-null class-298 pointer is materialized as a private two-port Linux bridge
between its Ethernet UNI and an internal ANI. Upstream mapper classification
sets the GEM mark before bridge lookup; the ANI egress then supplies the same
`l2_miss` metadata as an explicit MAC bridge. Downstream GEM traffic enters via
the ANI peer. The private `ommXXXX` bridge and `omxXXXX`/`omyXXXX` veth pair are
transactional platform objects and are not advertised as OMCI MEs. This avoids
reinterpreting all direct unicast as unknown or letting broadcast consume a
second multicast meter. Non-zero PIR and PBS are required because Linux cannot
infer the ONU's G.988 factory meter policy. Class-298 wire/default null pointer
`0` is normalized to the platform ABI null value `0xffff`.

The standalone OMCI daemon still owns the corresponding G.988 entities,
attribute validation, MIB data-sync and response status. Therefore migrating
the SDK monitor or flow parser alone is insufficient: the OMCI protocol model
and the hardware adapter must be migrated together, while keeping the
proprietary implementation behind this boundary.

VLAN tagging filter data is similarly normalized. `forward_operation` is kept
for diagnostics, while `tagged_action` contains the G.988 action letter
(`a`, `c`, `g`, `h` or `j`), `tagged_criterion` is `none`, `vid`, `priority` or
`tci`, and `untagged_action` is `a` or `c`. Reserved forward-operation values
are rejected before the platform helper is called.

Extended VLAN entries retain the G.988 treatment selectors in this JSON ABI.
The XG2010G platform compiler retains a `fixed`, received-`outer` or
received-`inner` source for every treatment PCP, VID, TPID and DEI field. A
single physical received tag occupies the G.988 inner filter fields but maps to
the kernel action's outer snapshot. The compiler derives the transmitted stack
as added tags followed by received tags that were not removed. Its inverse can
therefore match exact depths from zero through four and remove inserted tags
without discarding retained tags. Fixed transformations use the existing
POP/MODIFY/PUSH sequence. A transformation with any packet-dependent output
field is emitted as one software-only `tc vlan transform` action that snapshots
both received tags before removing or adding either tag. This supports copies
into genuinely pushed tags, copies from a tag removed earlier in the operation,
and independent two-tag restoration in downstream modes 3/4/6/7. For three-
and four-tag downstream frames only the outer tag is used when inner packet tag
information is unavailable, in accordance with G.988.

A priority-10 treatment uses the exported 64-entry `dscp_to_pbit` map to
produce ordered IPv4 and IPv6 classifiers, with the inverse direction reduced
to its distinct resulting P-bit matches. Outer and inner DEI criteria use the
platform's `vlan_dei` and `cvlan_dei` flower keys in those same classifiers. A
dynamic transform that references a received tag absent at run time drops the
packet. The action is deliberately rejected by flow offload because the Linux
flow-action ABI cannot describe its per-field sources.

MAC bridge profiles carry their learning, spanning-tree, port-bridging,
unknown-MAC, timer and learning-depth policy. Each bridge port carries its
termination type and pointer, priority, path cost, spanning-tree state,
traffic-descriptor pointers and learning depth. A platform may reject a graph
that cannot be represented without loss, but it must do so before changing
hardware state. The XG2010G backend accepts multiple active bridge profiles,
each with one ANI logical port and one or more Ethernet UNIs. It materializes a
profile-specific veth ANI endpoint and dispatches that endpoint's downstream
traffic by the GEM mark supplied by the PON driver. The physical `pon` remains
outside the per-profile Linux bridges. A UNI may belong to only one active
profile, and the backend never removes an interface from a non-OMCI Linux
bridge implicitly.

On XG2010G, a profile's non-zero learning depth is programmed as the Linux
bridge-wide dynamic FDB limit. Each bridge port's non-zero learning depth is
programmed as an additional per-port dynamic FDB limit; zero means unlimited.
Creation, aging, MAC roaming, userspace takeover and hardware external learning
all participate in the same accounting. Logical unicast and multicast ANI
ports that share one profile-specific endpoint must specify the same per-port
depth, otherwise the graph is rejected before apply.

Extended VLAN associations to a MAC bridge ANI port, 802.1p mapper or GEM
interworking termination are resolved onto that profile-specific ANI endpoint.
Upstream rules run on ANI egress and downstream rules run on ANI ingress.
Mapper and bridge-port associations cover the complete ANI, while a GEM-IW
association is selected by the reserved receive/transmit GEM skb mark. Multiple
GEM-IW profiles may therefore coexist on one ANI without sharing VLAN rule
chains.

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

For GEM performance monitoring, the control helper accepts:

```json
{"action":"gem-port-counters","gem_port_id":42}
```

and returns four cumulative 64-bit hardware counters:

```json
{"gem_port_id":42,"received_gem_frames":11,"received_payload_bytes":22,"transmitted_gem_frames":33,"transmitted_payload_bytes":44}
```

For FEC performance monitoring, the request contains the fixed XG2010G ANI-G
entity ID. No register address crosses the helper ABI.

```json
{"action":"fec-counters","ani_entity_id":32769}
```

The response contains five cumulative unsigned 64-bit values:

```json
{"ani_entity_id":32769,"corrected_bytes":11,"corrected_codewords":22,"uncorrectable_codewords":33,"total_codewords":44,"fec_seconds":55}
```

The GPON driver snapshots the EN7581 PHY counters at `phy-csr` offsets `0xba8`,
`0xb28`, `0xb24`, `0xba4` and `0xbbc`, respectively. This order and mapping
match the vendor GPON FEC statistics API `0x800d`; the SDK binary was used only
to establish the hardware ABI. The helper reads the read-only `fec_counters`
attribute and rejects an unknown ANI-G, a missing field, a reordered kernel
snapshot or a non-decimal value.

For Ethernet performance monitoring, the request contains only the fixed
G.988 PPTP Ethernet UNI entity ID. The XG2010G helper maps `0x0101` through
`0x0104` to `lan1` through `lan4`; no interface name crosses this ABI.

```json
{"action":"ethernet-counters","ethernet_entity_id":257}
```

The response contains cumulative 64-bit GDM MIB counters. `received` is the
CPE-to-ONU direction and `transmitted` is the ONU-to-CPE direction. The six
size buckets are exactly 64, 65-127, 128-255, 256-511, 512-1023 and 1024-1518
octets.

```json
{
  "ethernet_entity_id": 257,
  "received": {
    "frames": 1, "octets": 2, "drop_events": 3,
    "broadcast_frames": 4, "multicast_frames": 5, "crc_errors": 6,
    "buffer_overflows": 7, "internal_errors": 8,
    "undersize_frames": 9, "fragments": 10, "jabbers": 11,
    "oversize_frames": 12, "size_buckets": [13, 14, 15, 16, 17, 18]
  },
  "transmitted": {
    "frames": 21, "octets": 22, "drop_events": 23,
    "broadcast_frames": 24, "multicast_frames": 25, "crc_errors": 0,
    "buffer_overflows": 23, "internal_errors": 0,
    "undersize_frames": 26, "fragments": 0, "jabbers": 0,
    "oversize_frames": 27, "size_buckets": [28, 29, 30, 31, 32, 33]
  }
}
```

The Airoha driver keeps runt frames separate from the 64-octet bucket and
uses the existing per-NBQ MIB split for multi-serdes ports. Collision, SQE,
carrier-sense and alignment counters are zero because all four advertised
XG2010G UNIs are full-duplex and the GDM does not expose those legacy MAC
counters. GDM OK frame/octet counters do not include damaged-frame octets;
the resulting class 296 packet/octet values require physical OLT validation
against this hardware limitation. Unknown fields, missing fields, a mismatched
entity ID or an array other than six buckets are rejected.
