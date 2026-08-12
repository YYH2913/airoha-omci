# Platform event ABI

The daemon may start a fixed executable with `-event-helper PATH`. The helper
writes one UTF-8 JSON object per stdout line. Blank lines and lines beginning
with `#` are ignored. A malformed line terminates the helper session so procd
can restart the complete control plane instead of silently losing alarms.

The default format is baseline. An event may request `"format":"extended"`
when the payload requires the extended message set. The engine emits baseline
until the OLT has sent the first valid extended message in the current OMCC
session, as required by G.988.

## OMCC session reset

The platform helper emits this event when the raw OMCI carrier drops. It clears
message-set negotiation, transaction replay, upload caches and the alarm
sequence number without changing the ONU MIB or current alarm conditions.

```json
{"type":"omcc-session-reset"}
```

## Alarm condition

An alarm event changes one bit without replacing other active alarms for the
same managed entity. Duplicate state is suppressed. The engine validates the
bit against the managed entity's G.988 alarm map, updates the alarm audit table
and emits the complete 224-bit bitmap with the next ONU-wide alarm sequence
number.

```json
{"type":"alarm","class_id":11,"entity_id":257,"alarm_bit":0,"active":true}
```

## Attribute value change

An AVC event updates the ONU-owned MIB state without changing MIB data sync or
calling the service applier. Only attributes marked AVC-capable by the managed
entity definition are transmitted. Other valid read attributes are stored for
subsequent Get operations. Oversized baseline attribute sets are split only at
attribute boundaries.

```json
{"type":"avc","class_id":11,"entity_id":257,"attributes":{"OperationalState":0}}
```

Numeric values are converted to the width declared by the G.988 attribute.
Fixed octet/string fields accept a UTF-8 string, a `hex:`-prefixed string, or
an exact-length JSON byte array.

## Test result

Test result payloads are hexadecimal. A self-initiated result uses transaction
ID zero. A result caused by an OLT Test request retains that request's TCI.

```json
{"type":"test-result","class_id":256,"entity_id":0,"transaction_id":4660,"payload":"010203"}
```

## Optical sample

One coherent EN7572 diagnostics sample updates the ANI-G receive and transmit
optical levels. The protocol engine, rather than the platform helper, applies
the current OLT thresholds, the XG2010G module's internal SFF-8472 thresholds,
0.5 dB clear hysteresis and ARC suppression. All fields use the raw units
defined by the control helper ABI.

```json
{"type":"optical-sample","class_id":263,"entity_id":32769,"temperature":62976,"supply_voltage":33000,"laser_bias_current":2500,"transmit_power":10000,"receive_power":10}
```

The helper may suppress identical consecutive samples. Threshold changes are
re-evaluated against the last sample retained by the engine.

The EN7572 `los` indication is not mapped to ANI-G SF or SD. LOS and LOF remain
separate GPON PHY link-state inputs for activation and fibre-loss recovery.

## BER sample

The EN7581 driver handles the OLT BER Interval PLOAM, snapshots and clears the
GPON BIP counter at that interval, and sends the upstream REI PLOAM. Each new
snapshot is emitted once with a monotonic driver-local sequence number:

```json
{"type":"ber-sample","class_id":263,"entity_id":32769,"sequence":7,"bip_count":25000,"interval_ms":1000,"boot_id":"01234567-89ab-cdef-0123-456789abcdef"}
```

The protocol engine evaluates ANI-G SF and SD from the current OLT-writable
`10^-N` thresholds and the 2.48832 Gbit/s GPON downstream rate. A threshold
change re-evaluates the most recent sample. A zero interval and a repeated or
older sequence from the same Linux boot ID are rejected. A changed boot ID
starts a new sequence generation after system restart; an OMCC carrier drop
clears the sample before re-ranging or driver restart. LOS is never accepted as
a BER sample.
