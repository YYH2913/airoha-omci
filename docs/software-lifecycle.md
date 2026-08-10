# XG2010G software lifecycle

The XG2010G OpenWrt backend maps the two G.988 Software Image MEs to UBI
volumes. The active image is always named `fit`; the inactive image is named
`omci_fit_0` or `omci_fit_1`. UCI retains version, product code, MD5 ImageHash
and validity metadata, while U-Boot environment variables retain active,
committed and pending boot state.

## Download

The OMCI engine validates section order, the negotiated window, exact image
length and G.988 CRC-32A. The helper creates a static inactive UBI volume and
streams the image directly into it. It then exposes the volume through
`ubiblock`, verifies all FIT hashes, checks OpenWrt device metadata, and records
the MD5 ImageHash. A partial or rejected download remains invalid and its
volume is removed.

## Activate and rollback

Activation atomically renames `fit` to `omci_fit_old` and the staged image to
`fit`, records a pending boot, and reboots. On the first candidate boot U-Boot
arms the guard. A second boot before Commit restores `omci_fit_old` to `fit`.
The rollback sequence is restartable at each UBI rename boundary, so another
power loss cannot turn an intermediate volume name into a committed state.

Commit records the candidate as committed, clears the guard, and renames the
old image into the inactive slot. The helper completes an interrupted cleanup
the next time its state is read.

## Storage prerequisite

The stock layout lets `rootfs_data` consume every free UBI eraseblock. The
XG2010G U-Boot environment therefore caps it at `0x10000000` (256 MiB), leaving
space for a candidate FIT while retaining a substantially larger overlay than
the stock 1710G layout. An already installed device needs the updated U-Boot
and one normal sysupgrade before OMCI software download; that sysupgrade
recreates `rootfs_data` with the cap. Changing the environment variable alone
does not shrink an existing UBI volume.

Real hardware verification must include power removal before and after every
rename/environment update, automatic rollback after an uncommitted boot, and
successful Commit followed by repeated cold boots.
