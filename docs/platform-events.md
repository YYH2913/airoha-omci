# Platform event ABI

The daemon may start a fixed executable with `-event-helper PATH`. The helper
writes one UTF-8 JSON object per stdout line. Blank lines and lines beginning
with `#` are ignored. A malformed line terminates the helper session so procd
can restart the complete control plane instead of silently losing alarms.

The default format is baseline. An event may set `"format":"extended"` when
the payload requires the extended message set.

## Alarm condition

An alarm event changes one bit without replacing other active alarms for the
same managed entity. Duplicate state is suppressed. The engine validates the
bit against the managed entity's G.988 alarm map, updates the alarm audit table
and emits the complete 224-bit bitmap with the next ONU-wide alarm sequence
number.

```json
{"type":"alarm","class_id":263,"entity_id":32769,"alarm_bit":2,"active":true}
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
