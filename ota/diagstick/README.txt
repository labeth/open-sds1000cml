open-sds1000cml diagnostics stick
=================================

This stick tells the open-sds1000cml project what your oscilloscope is and how
its factory firmware drives it, so the project can support your model (for
example an SDS1000DL+ or another SDS1000 variant).

It runs alongside the factory firmware, and at the end takes the scope over for
a few minutes. It does not install anything and does not write to the
oscilloscope's own memory: every file it writes is on this stick, and the FPGA
images it loads last only until the power goes off. Switch the scope off and
on with the stick out and it is as before.

How to use it
-------------
1. Format a USB stick as MBR + FAT32 (one partition) and extract this zip onto
   its root, so that startup.sh and the sds-diag folder are at the top.
2. With the scope off and nothing on the probes, plug the stick in, power
   the scope on and leave it until sds-diag-status.txt says DONE (about 30
   minutes; see "What it does" below). Open the file on a computer to look:
   take the stick out only when the scope is off, or when it says DONE.
3. Turn the scope off and take the stick out. On the stick you find:
     sds-diag-<mac>-auto-1.tar.gz      the file to send
     sds-diag-<mac>-auto-1.txt         a short summary
     sds-diag-status.txt               what it did (ends with DONE)
4. Send the .tar.gz to the project, privately (an issue on
   https://github.com/labeth/open-sds1000cml/issues says how). It holds copies
   of your scope's firmware and calibration files.

What it does, in one boot
-------------------------
sds-diag/MODE says how far it goes. It comes set to "auto":

1. The read-only stage (about a minute): the board's processor, kernel,
   devices and pin multiplexing, the factory app's devices and memory map,
   the factory firmware files (with the FPGA image and the calibration), the
   factory app's settings and its screen.
2. A check that the scope is one the next stage was made for: an AM335x
   running the SDS1000 factory app, the FPGA answering a register read, a
   Siglent SDS1000-series identity, settings answered in the expected form.
   If any check fails it stops here, and the summary says which one.
3. The active stage: it records which FPGA registers the factory firmware
   writes for each setting, while the stick steps the scope through every
   V/div on both channels, couplings, offsets, bandwidth limit, timebases up
   to 100 ms/div, trigger and acquisition modes. The relays click, the screen
   changes and the scope answers its keys slowly for about twenty minutes
   (never more than twenty-five); then it puts your settings back.
4. A second check: the factory app took every step and runs on, the FPGA's
   configuration port is where this project loads its images, the factory
   FPGA image is for the same FPGA. If any check fails it stops here.
5. The takeover stage (about five minutes): it stops the factory app, loads
   the project's probe image into the FPGA (every ball an input but the
   processor's bus and the converters' clocks) and records what each ball of
   the FPGA is connected to: which converters are fitted, whether each
   channel's front end moves them, the clocks, the memory and panel lines.
   Then it runs the project's own firmware for a minute and a half and
   records what it does. The screen freezes, then shows the project's
   firmware; the keys do nothing.

Leave the scope alone until sds-diag-status.txt says DONE: switching off
earlier loses the file and can leave your settings changed. Have nothing
connected to the probes. After DONE, switch the scope off, take the stick
out, and the factory firmware starts as always at the next power-on.

To stop after the active stage, replace "auto" in sds-diag/MODE with "active"
in a text editor before you boot; to run only the read-only stage, with
"passive".
