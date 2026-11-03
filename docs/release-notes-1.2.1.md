# open-sds1000cml 1.2.1

Open replacement firmware for the Siglent SDS1000CML+ two-channel oscilloscope, developed and
validated on the SDS1102CML+. The firmware itself is unchanged from [1.2](release-notes-1.2.md);
this release adds a diagnostics stick for other SDS1000 models and makes the release rebuild byte for
byte anywhere.

> Experimental firmware with no warranty. Read [SAFETY.txt](../SAFETY.txt) before installing.

## What is new since 1.2

* **A diagnostics stick for other SDS1000 models.** `open-sds1000cml-v1.2.1-diagnostics.zip` is a
  stick of its own for the owner of another SDS1000 variant (an SDS1000DL+, or another population of
  the board). Booted under the factory firmware it installs nothing and writes only to the stick; it
  leaves one archive, built in three stages, each run only when checks find what it expects:
  * **read-only:** the scope's processor, kernel, devices and pin multiplexing, the factory app's
    devices and memory map, the factory firmware files (with the FPGA image and calibration), the
    factory app's settings and its screen;
  * **active:** which FPGA registers the factory firmware writes for each setting while the stick
    steps the factory app through them, summarised as a register map, with the settings put back
    afterwards;
  * **takeover:** the stick stops the factory app, loads a probe image into the FPGA and judges which
    converter cores, channel front ends and clocks the board has, and how its other balls compare
    with the SDS1102CML+; then it runs this firmware's app on the board for a minute and a half.

  Its `README.txt` says how to use it and what to send. All three stages were run on an
  SDS1102CML+ under the factory firmware, which they judged fully fitted; this firmware's app came
  up healthy in 2 s.
* **The release rebuilds byte for byte.** The binaries carry no VCS stamp, so a checkout, a worktree
  or a plain copy of the tag build the same files as the release.
* **The OTA agent's restore-factory command is gone.** It relaunched the factory app in place, which
  never drove the scope; the factory firmware comes back from a power-on with the stick out.

## Install, update, remove

As for [1.2](release-notes-1.2.md#install): download `open-sds1000cml-v1.2.1-usb.zip`, extract it onto
the root of an MBR + FAT32 stick, and boot the scope with it. Updating from 1.2 needs nothing else:
the app and the agent are the same apart from the removed command.

## Known limitations

As in [1.2](release-notes-1.2.md#known-limitations). The diagnostics stick's active stage keeps the
scope slow to answer its keys for about twenty minutes, and stops at 100 ms/div: traced at 1 s/div
the factory app of an SDS1102CML+ exits. After the stick's takeover stage the factory firmware
needs a power-cycle to come back.
