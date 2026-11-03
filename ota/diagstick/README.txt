open-sds1000cml diagnostics stick
=================================

This stick tells the open-sds1000cml project what your oscilloscope is and how
its factory firmware drives it, so the project can support your model (for
example an SDS1000DL+ or another SDS1000 variant).

It runs alongside the factory firmware. It does not install anything, does not
load an FPGA image and does not write to the oscilloscope's own memory: every
file it writes is on this stick. Take the stick out and the scope is as before.

How to use it
-------------
1. Format a USB stick as MBR + FAT32 (one partition) and extract this zip onto
   its root, so that startup.sh and the sds-diag folder are at the top.
2. With the scope off, plug the stick in, power the scope on and leave it for
   five minutes. The scope boots and works as usual meanwhile.
3. Turn the scope off and take the stick out. On the stick you find:
     sds-diag-<mac>-passive-1.tar.gz   the file to send
     sds-diag-<mac>-passive-1.txt      a short summary
     sds-diag-status.txt               what it did (ends with DONE)
4. Send the .tar.gz to the project, privately (an issue on
   https://github.com/labeth/open-sds1000cml/issues says how). It holds copies
   of your scope's firmware and calibration files.

The active stage (only when the project asks for it)
----------------------------------------------------
Once the project has looked at the first file, it may ask for the active
stage. It records which registers the factory firmware writes for each
setting, while the stick steps the scope through its settings by itself:
every V/div on both channels, couplings, offsets, bandwidth limit,
timebases, trigger and acquisition modes. The relays click and the screen
changes for about four minutes. Afterwards it puts your settings back.

To run it, open sds-diag/MODE on the stick in a text editor, replace
"passive" with "active", save, and repeat steps 2-4 with the scope's inputs
unconnected (nothing on the probes). The file is then named ...-active-1.tar.gz.

Change MODE back to "passive" afterwards.
