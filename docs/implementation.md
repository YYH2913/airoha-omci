# Implementation matrix

The completion gate is interoperability with a real OLT using both baseline
and extended messages.  A successful build or reaching GPON O5 is not enough.

| Area | Required behavior | State |
| --- | --- | --- |
| OMCC transport | raw RX/TX, cancellation, frame bounds, counters | implemented; MIC/adapter verification pending |
| Transactions | per-priority stop-and-wait replay and TCI validation | implemented; queue/MIC integration pending |
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

Acknowledged commands follow the G.988 stop-and-wait rules: baseline low and
high priority retain independent last-command TCI/response state, while the
extended format uses one priority class. Only the last response in each class
is replayed, including when a retransmitted frame differs after the header;
older reused TCIs are executed as new commands. Immediate duplicate
unacknowledged software sections are suppressed separately.

Transport validation accepts only the explicit OMCC adapter forms: a 48-byte
baseline frame, the 44-byte MIC-stripped form emitted by the protocol library,
or the 40-byte trailer-stripped form; and an extended frame whose declared
content length exactly matches the packet with or without the four-byte MIC.
Oversized, truncated, unknown-format and trailing-byte frames are rejected
before protocol dispatch.

MIB upload uses an immutable per-message-set snapshot with the G.988 one-minute
inactivity limit. Each valid MIB upload next request, including a retransmission,
refreshes the deadline. An expired or out-of-range baseline request receives a
zero class/entity/mask body, while its extended equivalent receives a zero-length
message contents field. Managed entity/attribute definition MEs, table attributes
and PM measurement counters are omitted from the snapshot.

MIB data sync follows the command outcome rather than the arrival of a command.
Create, delete, Set and SetTable advance the counter only when the committed MIB
changes. An OLT Set of ONU data MIB data sync to `N` atomically commits `N+1`,
with 255 wrapping to 1; a platform apply failure leaves both the MIB and counter
unchanged.

The fixed-path event helper now maps XG2010G BOSA LOS, GPON activation state,
optical diagnostics and the four Ethernet carrier states into validated
Alarm/AVC state. ANI-G optical line supervision tests return all five G.988
result types in baseline and extended format. Dynamic ANI-G optical attributes,
OLT and internal thresholds, clear hysteresis, ARC suppression/cancellation and
ARC-aware alarm audits are implemented; physical OLT verification remains.

Software download now implements baseline and extended sections, negotiated
maximum windows, shorter OLT-selected windows and complete-window retry without
committing partial data. Baseline final padding must be zero, while extended
sections may not overrun the declared image. Immediate duplicate no-response
sections are suppressed. End download validates the G.988 CRC-32A and image
size, returns `device busy` while non-volatile validation runs asynchronously,
and accepts continued matching End requests until a stable result is available.
The single-target response omits the optional parallel-download result list.

Start download rejects active or committed targets. Activating the already
active image still performs the required soft restart, and Commit accepts any
valid target, including an inactive image selected for the next boot. Start,
successful End, Activate and Commit update all affected software image MEs
atomically and advance MIB data sync once per command when their state changes.
The XG2010G backend stages an OpenWrt FIT in an inactive UBI volume and uses a
boot guard to roll back an activated but uncommitted image or restartably select
a committed inactive image at the next boot. Physical OLT software download and
deliberate power-loss testing remain required.

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
