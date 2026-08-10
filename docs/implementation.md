# Implementation matrix

The completion gate is interoperability with a real OLT using both baseline
and extended messages.  A successful build or reaching GPON O5 is not enough.

| Area | Required behavior | State |
| --- | --- | --- |
| OMCC transport | raw RX/TX, cancellation, frame bounds, counters | in progress |
| Transactions | duplicate replay, TCI validation, bounded response cache | pending |
| MIB | ONU defaults, atomic create/delete/set/get, reset, data sync | in progress |
| MIB upload | baseline fragmentation and extended packing | pending |
| Tables | Get Next and Set Table session caches | pending |
| Notifications | alarm table, alarm sequence, AVC and test results | pending |
| Equipment | ONU-G, ONU2-G, ANI-G, four Ethernet UNIs, software images | in progress |
| Traffic | T-CONT, scheduler, priority queues, GEM CTP/IW | pending |
| Ethernet service | bridge, mapper, VLAN and extended VLAN rules | pending |
| Lifecycle | reboot, time sync, software download/activate/commit | pending |
| Platform | multi-T-CONT/GEM Airoha ABI and transactional Linux backend | pending |
| OpenWrt | package, procd, UCI, ubus and LuCI | pending |
| Verification | unit/fuzz/race/cross-build plus physical OLT traces | pending |

