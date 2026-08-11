# Implementation matrix

The completion gate is interoperability with a real OLT using both baseline
and extended messages.  A successful build or reaching GPON O5 is not enough.

| Area | Required behavior | State |
| --- | --- | --- |
| OMCC transport | raw RX/TX, cancellation, frame bounds, counters | implemented; physical adapter verification pending |
| Transactions | per-priority scheduling, stop-and-wait replay and TCI validation | implemented; physical saturation verification pending |
| MIB | ONU defaults, platform-gated create/delete/set, reset, data sync | in progress |
| MIB upload | baseline fragmentation and extended multi-ME packing | in progress |
| Tables | stable Get/Get Next cache and baseline/enhanced extended Set Table | implemented; OLT verification pending |
| Notifications | alarm audit/sequence, event-driven Alarm/AVC, requested optical tests and ARC | in progress |
| Equipment | ONU-G, ONU2-G, ANI-G, four speed-specific Ethernet UNIs, software images | in progress |
| Traffic | 8 advertised T-CONTs, scheduler/queue graph, GEM CTP/IW validation and hardware apply | upstream SP/WRR/TRTCM implemented; physical verification pending |
| Performance | common 15-minute engine, GEM CTP and Ethernet UNI/frame PM | class 341, 24, 296, 321 and 322 counters, threshold data 1/2 and TCA implemented; physical verification pending |
| Ethernet service | resolved bridge, mapper, VLAN and extended VLAN graph | UNI, bridge-port, mapper, GEM-IW and static/copy tag treatment implemented; packet-dependent treatment pending |
| Multicast | multicast GEM-IW, IGMP/MLD policy, subscriber limits and live groups | class 281 and class 309/310 tables, ACL/preview enforcement, native report/query interception, proxy querier and sampled class 311 monitoring implemented; physical OLT verification pending |
| Lifecycle | reboot, time sync, software download/activate/commit | implemented; OLT verification pending |
| Platform | multi-T-CONT/GEM Airoha ABI and transactional Linux backend | GEM and 16-channel GPON QDMA QoS, UNI VLAN, bridge FDB limit and multi-profile MAC bridge implemented; Ethernet offload pending |
| OpenWrt | package, procd, UCI, rpcd/ubus and LuCI | optical configuration, live service status and automatic/manual service handover implemented |
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

The OMCC receive loop drains frames into a bounded scheduler while a platform
transaction is running. Baseline high-priority frames are selected before the
baseline-low and extended FIFO, without reordering either class internally.
Queue exhaustion terminates the daemon instead of silently dropping one-way
software sections. An OMCC session-reset event atomically clears queued frames
and advances a generation counter, so a receive started before re-ranging
cannot leak a stale frame into the new transaction replay state.

Transport validation accepts only the explicit OMCC adapter forms: a 48-byte
baseline frame, the 44-byte MIC-stripped form emitted by the protocol library,
or the 40-byte trailer-stripped form; and an extended frame whose declared
content length exactly matches the packet with or without the four-byte MIC.
For GPON, a retained baseline or extended MIC is verified as the
CRC-32/ITU-I.363.5 of all preceding OMCI bytes. MIC-stripped frames remain
valid because the kernel or MAC may have already verified and consumed it;
the protocol library's AES-CMAC mode is not used for GPON. Oversized,
truncated, unknown-format, trailing-byte and bad-MIC frames are rejected before
protocol dispatch.

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

Performance monitoring uses one synchronized 15-minute interval across all
active PM MEs. Class 341 reads per-GEM 64-bit GPON MAC counters. Classes 24 and
296 read the physical Ethernet UNI selected by their identical entity ID;
classes 321 and 322 resolve a MAC bridge port whose TP type is PPTP Ethernet
UNI and use the transmitted and received direction respectively. Bridge-port
ANI/GEM terminations are rejected because a physical PON netdev counter cannot
represent one bridge flow accurately. Get current data is limited to the
G.988 aggregate attribute size of 25 octets. Counter rollback is treated as a
hardware reset, 32-bit OMCI counters saturate, skipped intervals are emitted as
zero, and Synchronize time or MIB reset atomically restarts the interval.

The XG2010G Ethernet driver exposes a root-only counter snapshot for each
`lan1` through `lan4` netdev. Its existing GDM3/GDM4 per-NBQ MIB split keeps
multi-serdes ports independent, while runt and exact 64-octet frames are now
kept in separate buckets. The platform helper accepts only the four fixed UNI
entity IDs.

A non-zero PM threshold data 1/2 pointer must resolve to an existing threshold
data 1 ME; the same-ID threshold data 2 ME supplies optional values 8 through
14. Zero pointers and threshold values 0 or 0xFFFF disable the corresponding
TCAs.
The class-specific G.988 mapping is implemented for GEM CTP class 341 and
Ethernet classes 24, 296, 321 and 322. A counter reaching its configured value
raises its TCA bit once in the current interval; later crossings produce a
cumulative bitmap without repeating an already active bit. At each 15-minute
boundary the ONU sends the required all-clear TCA notification, including
after an explicit time synchronization. MIB reset and PM deletion discard
local state. PM instances with no enabled threshold avoid intra-interval
hardware sampling.

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
downstream frames by their receive GEM mark, and apply classic and extended
VLAN rules on UNI and ANI bridge paths. Mapper and ANI bridge-port associations
cover a complete ANI endpoint; GEM-IW associations use mark-dispatched rule
chains so multiple GEM profiles can coexist on that endpoint. Each complete
bridge profile with one ANI logical port and one or more Ethernet UNIs is
materialized as a managed Linux bridge. A
profile-specific veth pair represents its ANI port: the bridge-facing end
keeps the profile's FDB and flooding policy independent, while the PON-facing
end redirects upstream traffic to the physical `pon` and receives only the
GEM-marked downstream traffic resolved to that profile. Multiple profiles can
therefore share one physical PON without merging learning domains. FDB
learning, unknown-unicast flooding, UNI isolation and the G.988 VLAN filter
`j` action are enforced. Candidate failure restores GEM, TC, VLAN and bridge
state from the last committed graph.

Multicast GEM interworking termination points support the class 281 IPv4 and
IPv6 address tables, including keyed replacement and deletion. MAC bridge port
TP type 6 joins a multicast GEM-IW to the same profile-specific ANI endpoint
used by unicast service. Address-table GEM Port-IDs that have no explicit GEM
CTP are programmed as downstream-only flows, and their receive marks are
dispatched to that ANI endpoint. The OpenWrt backend disables Linux bridge
multicast snooping so the native G.988 runtime is the sole membership authority.

The native `airoha-mcastd` backend consumes the committed ABI 4 graph and
intercepts IGMPv1/v2/v3 and MLDv1/v2 reports before Linux bridge snooping. It
enforces class 309 dynamic/static ACL ranges and class 310 service-package,
allowed-preview, group and imputed-bandwidth policy per UNI. Static ranges and
accepted dynamic streams are compiled into a default-deny tc egress chain;
source-specific entries remain source-specific. Upstream tag-control modes
0-3 preserve up to two subscriber VLAN tags and upstream rate limits are
applied before reports are reinjected through the profile ANI. Downstream
tag-control modes 0-7 use tc VLAN actions; the Airoha kernel/iproute2 ABI
extends those actions with explicit DEI push/modify handling.

The runtime aggregates first-join/final-leave reports for SPR/proxy profiles,
tracks each client independently, expires preview delivery, and atomically
publishes the live class 311 tables. Proxy profiles originate periodic general
queries and age silent listeners using the RFC membership interval. A non-fast
leave sends the configured robustness count of group or group-and-source
specific queries before replication and the aggregate upstream membership are
withdrawn. A zero robustness value follows QRV learned from valid downstream
IGMPv3/MLDv2 queries and otherwise uses the RFC default. OLT-originated queries
are admitted by the default-deny tc chain and receive class-309 downstream tag
control; locally generated queries use a reserved skb mark to avoid applying
the transformation twice.

Class-311 current bandwidth uses sampled tc action byte counters when available,
and falls back immediately to the configured imputed bandwidth when a counter
read is unavailable or a rule is rebuilt. Class-310 allowed-preview `time-left`
is written back from the real deadline and expires autonomously; direct mapper
attachments select a validated upstream GEM and send reports through the marked
PON path. Physical OLT and sustained multicast traffic verification remain.
Real baseline and extended OLT traces and sustained multicast traffic tests
remain deployment gates.

XG2010G defaults keep `lan1` through `lan4` in the user's `br-lan` and expose
`pon` as a routed WAN. Transparent PPTP-UNI service therefore requires the
selected LAN ports to be explicitly released from their existing network
bridge. The platform helper reports `bridge-conflict` and leaves that network
untouched if any requested LAN or PON port has a non-OMCI master.

The GPON configuration service records the exact legacy `data_gem` setting it
installs in manual mode. Switching back to automatic OMCI provisioning removes
only that owned setting. If the OMCI backend has already replaced it with a
multi-GEM configuration during the transition, the configuration service leaves
the new state intact. This prevents a stale manual channel from competing with
the OLT while avoiding an implicit teardown of a newly provisioned service.

GEM CTP upstream and downstream traffic-descriptor pointers are checked as part
of every graph commit. A non-null pointer must resolve to class 280. The
XG2010G backend maps one active T-CONT to one of 16 GPON QDMA channels and
programs its eight queues and egress TRTCM meter atomically with the GEM table.
The factory MIB currently advertises eight T-CONTs. G.988 scheduler policies 0
and 1 use strict priority and policy 2 uses WRR8. CIR/PIR retain their G.988
bytes-per-second units until the driver converts them to kbit/s; CBS/PBS are
bytes. Multiple GEMs sharing a T-CONT must select the same upstream traffic
descriptor because EN7581 exposes one egress meter per channel.

Downstream per-GEM traffic descriptors still require an ingress hardware
backend and are rejected. Colour-aware marking/remarking and RFC 4115 meter
coupling are also rejected rather than silently approximated. These limits and
real OLT/optical-module testing remain explicit deployment blockers.

Extended-VLAN treatment now supports fixed PCP/VID, exact-filter copies,
same-tag PCP/VID/TPID/DEI preservation, constant DSCP mappings and explicit
DEI 0/1. Single-tag downstream modes 3/6 and 4/7 invert only VID or PCP and
preserve the other TCI fields. The Airoha kernel/iproute2 VLAN action extension
allows each MODIFY field to be omitted; partial MODIFY is deliberately kept in
software because the flow-offload ABI has no field mask. DEI matching uses a
classic-BPF gate before the existing VLAN action chain.

The remaining Ethernet blockers are packet-dependent DSCP mapping, wildcard
copies into a newly added tag, double-tag and non-replacement partial inverse,
per-port learning-depth, and native Airoha offload. These unsupported forms are
rejected before platform state changes. Transactional crash recovery and
physical baseline/extended OLT interoperability also remain completion gates.
