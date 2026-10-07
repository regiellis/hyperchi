---
title: ZFS scrub and disk replacement
tags: zfs, storage, disks
---

# ZFS scrub and disk replacement

The pool `tank` is two mirrored pairs. A scrub runs on the first Sunday of
each month from the `zfsutils` cron entry.

## Read the scrub result

```sh
zpool status -v tank
```

`scrub repaired 0B ... with 0 errors` is the good case. Any `CKSUM` count above
zero on one disk means that disk is suspect, even when the scrub repaired it.

## Replace a failing disk

1. Note the failing disk's serial from `zpool status` and `ls -l /dev/disk/by-id/`.
2. Offline it: `zpool offline tank ata-WDC_WD80EFZZ_SERIAL1`.
3. Swap the drive, then attach the new one by its by-id path:
   `zpool replace tank ata-WDC_WD80EFZZ_SERIAL1 ata-WDC_WD80EFZZ_SERIAL9`.
4. Watch the resilver with `zpool status tank`. It takes about six hours for
   an 8 TB disk.

Do not reboot during a resilver unless you have to; it restarts from the top.
