#!/bin/sh
# open-sds1000cml diagnostics stick: the factory firmware runs this file from
# the stick's root at boot (its own USB hook). It starts the collector in the
# background and returns at once, so the factory firmware boots as always; the
# collector waits for the factory app, then leaves one archive on the stick.
#
# It only reads, unless sds-diag/MODE says "active" (see README.txt). It never
# loads an FPGA image and never writes the instrument's flash; the only files
# it writes are on this stick.

USB=/usr/bin/siglent/usr/media/U-disk0
[ -x "$USB/sds-diag/sdsdiag" ] || USB=$(dirname "$0")
mount -o remount,rw "$USB" 2>/dev/null || mount -o remount,rw /dev/sda1 "$USB" 2>/dev/null

# The subshell keeps this shell's open descriptors: the collector reads the
# FPGA's version through the /dev/Gpmc descriptor it inherits (a fresh open of
# that device faults the bus).
(
    sleep 20
    "$USB/sds-diag/sdsdiag" -out "$USB" >> "$USB/sds-diag/run.log" 2>&1
    sync
) &
