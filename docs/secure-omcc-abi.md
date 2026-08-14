# Secure OMCC device ABI

XGS-PON OMCI must not trust a userspace assertion that a downstream MIC was
verified. The kernel or hardware driver owns the active OMCI integrity keys,
verifies downstream AES-CMAC before delivery and signs upstream frames before
transmission. `airoha-omcid` uses `/dev/airoha-xgs-omcc` only when the device
proves both directions through ABI version 3.

## Capability query

The character device implements this ioctl:

```c
#define AIROHA_XGS_OMCC_GET_INFO _IOR('X', 0, __u32)
```

The returned `__u32` is:

| Bits | Meaning |
| --- | --- |
| 7:0 | ABI version, exactly `3` |
| 8 | downstream MIC is verified before `read()` |
| 9 | upstream MIC is generated after `write()` |
| 31:10 | reserved, zero |

The daemon rejects the device unless bits 8 and 9 are both set. It also rejects
regular files, unknown ABI versions and unknown capability bits.

## Record format

Every successful `read()` returns exactly one complete record. Every
successful `write()` consumes exactly one complete record; partial records are
not permitted.

```c
struct airoha_xgs_omcc_record {
	__be32 magic;       /* 0x584f4d43, "XOMC" */
	__u8 abi_version;   /* 3 */
	__u8 direction;     /* 1: kernel to daemon, 2: daemon to kernel */
	__be16 flags;
	__be16 length;      /* bare OMCI bytes following this header */
	__be16 reserved;    /* zero */
	__be64 instance_generation; /* random non-zero driver-instance nonce */
	__be64 session_generation; /* trusted kernel OMCC/key session */
	__u8 contents[];
} __packed;
```

For downstream records, flags must be exactly `BIT(0) | BIT(1)`:

- bit 0: the active-key AES-CMAC was verified by the trusted driver;
- bit 1: the mode-specific security trailer was removed;
- all other bits are reserved and zero.

For upstream records, flags are zero. The driver rejects transmission unless
it owns a valid active key and can append the correct security trailer and MIC.
Downstream records carry a random non-zero `instance_generation` selected at
driver probe and the `session_generation`, which advances whenever that driver
instance invalidates the OMCC/key session. The daemon treats any instance
change as a hard replay/counter boundary and applies monotonic ordering only to
sessions from the same instance. A single open character-device stream belongs
to one driver instance; module removal invalidates that stream. Upstream records
must set both generation fields to zero. The content length is between 4 and 1976 bytes; the XGS-PON wire frame reserves
the remaining four bytes of the 1980-byte limit for MIC. Records with a wrong magic,
version, direction, flags, reserved field or length are rejected by the daemon.

Opening this device does not authorize optical TX. XGS activation, PLOAM key
exchange, calibration validation and rogue-ONU protection remain independent
prerequisites in the kernel driver.
