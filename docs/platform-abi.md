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

The apply helper receives a complete candidate service state on stdin and must
apply it transactionally. The control helper handles validated time and reboot
operations. The event helper streams bounded JSON lines for platform alarms
and AVCs. Their schemas are intentionally separate from the software helper so
software-slot privileges can be audited independently.
