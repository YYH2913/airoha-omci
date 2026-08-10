# Implementation matrix

The completion gate is interoperability with a real OLT using both baseline
and extended messages.  A successful build or reaching GPON O5 is not enough.

| Area | Required behavior | State |
| --- | --- | --- |
| OMCC transport | raw RX/TX, cancellation, frame bounds, counters | in progress |
| Transactions | duplicate replay, TCI validation, bounded response cache | in progress |
| MIB | ONU defaults, platform-gated create/delete/set, reset, data sync | in progress |
| MIB upload | baseline fragmentation and extended multi-ME packing | in progress |
| Tables | stable Get/Get Next cache and baseline/enhanced extended Set Table | implemented; OLT verification pending |
| Notifications | alarm audit/sequence, event-driven Alarm/AVC, requested optical tests and ARC | in progress |
| Equipment | ONU-G, ONU2-G, ANI-G, four speed-specific Ethernet UNIs, software images | in progress |
| Traffic | 8 T-CONTs/schedulers, 96 queues, GEM CTP/IW validation and hardware apply | in progress |
| Ethernet service | resolved bridge, mapper, VLAN and extended VLAN graph | common UNI/ANI bridge service implemented; advanced associations pending |
| Lifecycle | reboot, time sync, software download/activate/commit | implemented; OLT verification pending |
| Platform | multi-T-CONT/GEM Airoha ABI and transactional Linux backend | GEM, UNI VLAN and single-profile MAC bridge implemented; hardware offload pending |
| OpenWrt | package, procd, UCI, rpcd/ubus and LuCI | optical configuration and live service status implemented; mode integration pending |
| Verification | unit/fuzz/race/cross-build plus physical OLT traces | pending |

## Current interoperability limits

The daemon now provides valid error responses for unknown managed entities,
transactional platform commits, baseline table transfer, extended MIB upload
packing, alarm audits, time synchronization and scheduled reboot. The factory
MIB advertises the two 10G, one 2.5G and one 1G Ethernet UNIs independently.

The fixed-path event helper now maps XG2010G BOSA LOS, GPON activation state,
optical diagnostics and the four Ethernet carrier states into validated
Alarm/AVC state. ANI-G optical line supervision tests return all five G.988
result types in baseline and extended format. Dynamic ANI-G optical attributes,
OLT and internal thresholds, clear hysteresis, ARC suppression/cancellation and
ARC-aware alarm audits are implemented; physical OLT verification remains.

Software download now implements baseline and extended sections, negotiated
windows, duplicate no-response section replay, G.988 CRC-32A, MD5 ImageHash,
and persistent activate/commit through the software helper. The XG2010G
backend stages an OpenWrt FIT in an inactive UBI volume and uses a boot guard
to roll back an activated but uncommitted image. Physical OLT software
download and deliberate power-loss testing remain required.

The Airoha Ethernet metadata ABI now exposes an atomic GEM/channel/direction
table. Receive packets carry a reserved skb mark containing the GEM Port-ID;
transmit uses the same mark to select the upstream GEM and T-CONT channel. An
unmarked transmit packet is accepted only when exactly one upstream GEM is
configured, preventing ambiguous service selection.

The OpenWrt backend programs all OLT-provisioned GEM CTPs. TC classifiers set
the reserved skb mark for P-bit, DSCP and default mapper branches, dispatch
downstream frames by their receive GEM mark, and apply UNI-side classic and
extended VLAN rules. A complete bridge profile with one ANI logical port and
one or more Ethernet UNIs is materialized as a managed Linux bridge; FDB
learning, unknown-unicast flooding, UNI isolation and the G.988 VLAN filter
`j` action are enforced. Candidate failure restores GEM, TC, VLAN and bridge
state from the last committed graph.

XG2010G defaults keep `lan1` through `lan4` in the user's `br-lan` and expose
`pon` as a routed WAN. Transparent PPTP-UNI service therefore requires the
selected LAN ports to be explicitly released from their existing network
bridge. The platform helper reports `bridge-conflict` and leaves that network
untouched if any requested LAN or PON port has a non-OMCI master.

The remaining Ethernet blockers are multiple simultaneous bridge profiles on
one physical PON port, VLAN associations on ANI/mapper/GEM bridge ports, full
extended-VLAN DEI/copy/inverse behavior, learning-depth and traffic-descriptor
enforcement, and native Airoha offload. Transactional crash recovery and
physical baseline/extended OLT interoperability also remain completion gates.
