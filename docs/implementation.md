# Implementation matrix

The completion gate is interoperability with a real OLT using both baseline
and extended messages.  A successful build or reaching GPON O5 is not enough.

| Area | Required behavior | State |
| --- | --- | --- |
| OMCC transport | raw RX/TX, cancellation, frame bounds, counters | in progress |
| Transactions | duplicate replay, TCI validation, bounded response cache | in progress |
| MIB | ONU defaults, platform-gated create/delete/set, reset, data sync | in progress |
| MIB upload | baseline fragmentation and extended multi-ME packing | in progress |
| Tables | stable Get/Get Next cache and extended Set Table | in progress |
| Notifications | alarm audit/sequence, event-driven Alarm/AVC, requested optical tests and ARC | in progress |
| Equipment | ONU-G, ONU2-G, ANI-G, four speed-specific Ethernet UNIs, software images | in progress |
| Traffic | 8 T-CONTs/schedulers, 96 queues, GEM CTP/IW validation and hardware apply | in progress |
| Ethernet service | bridge, mapper, VLAN and extended VLAN rules | pending |
| Lifecycle | reboot, time sync, software download/activate/commit | implemented; OLT verification pending |
| Platform | multi-T-CONT/GEM Airoha ABI and transactional Linux backend | in progress |
| OpenWrt | package, procd, UCI, rpcd/ubus and LuCI | in progress |
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

The current Airoha Ethernet metadata ABI exposes only one data GEM to Linux.
The OpenWrt backend records all OLT-provisioned GEM CTPs but selects one
bidirectional GEM for the `pon` netdev and reports the limitation through
ubus/LuCI. Multi-GEM representors and physical baseline/extended OLT
interoperability remain completion blockers.
