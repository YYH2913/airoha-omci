# Dependency notes

The repository vendors `github.com/opencord/omci-lib-go/v2` v2.2.4 so the
wire definition used by tests, host builds and OpenWrt cross-builds is
identical. The vendored Class 441 definition has a narrow local correction for
ITU-T G.988 (11/2022) ONU3-G:

* the supported message types are Get, Get Next and Set;
* Status snapshot record table is a table with 25-byte vendor records;
* Number of valid status snapshots and Next status snapshot index, but not the
  static Total number, are marked AVC-capable.

Upstream v2.2.4 and its current source revision define only Get, encode the
record table as one fixed octet string and omit both AVC declarations. That
prevents normal baseline/extended Set and Get Next decoding. When updating the
dependency, compare
`vendor/github.com/opencord/omci-lib-go/v2/generated/onu3-g.go` with these
requirements before removing the local correction.
