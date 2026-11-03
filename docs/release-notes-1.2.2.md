# open-sds1000cml 1.2.2

Open replacement firmware for the Siglent SDS1000CML+ two-channel oscilloscope, developed and
validated on the SDS1102CML+. The firmware is unchanged from [1.2](release-notes-1.2.md); this
release corrects two verdicts of the [1.2.1](release-notes-1.2.1.md) diagnostics stick.

> Experimental firmware with no warranty. Read [SAFETY.txt](../SAFETY.txt) before installing.

## What is new since 1.2.1

* **The diagnostics stick no longer reports a working SRAM as partial.** A complete run on an
  SDS1102CML+ read one word of 524,288 wrong once. A missing SRAM breaks every word and a stuck
  address line thousands, so a few isolated errors now count as fitted.
* **The SRAM data lines take any level in the census.** Like the SRAM counter's clock and enable,
  they keep the level the factory image last drove, which differs from one session to the next;
  whether the SRAM is fitted is the SRAM test's verdict.

Both were found by a complete run of the 1.2.1 stick (the full settings sweep, the takeover, the
census, the board tests and this firmware's app) on an SDS1102CML+ booted cold under the factory
firmware; with them, that run judges every part present.

## Install, update, remove

As for [1.2](release-notes-1.2.md#install). Use `open-sds1000cml-v1.2.2-diagnostics.zip` rather
than the 1.2.1 one.
