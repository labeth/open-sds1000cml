# Integrated acquisition path

`sram_acquisition_path` connects the actual ADC interleave/precision source,
finite trigger writer, continuous ingress/controller and frozen recall to one
SRAM transport and one host buffer. External board clocks and the GPMC register
interface are not included. All acquisition commands/settings are core-domain;
host buffer reads/releases use the existing host-domain contract.

Operation 0 captures a finite record, 1 streams precision words, and 2 recalls
a finite frozen record. Starts are mutually exclusive, wait for transport and
bank ownership, and validate geometry/decimation. Streaming accepts /256 and
slower. Its acceptance invalidates any older finite record. Recall ranges are
relative to the retained finite record. Recall of a prior streaming ring is
not exposed by this wrapper yet; continuous words are delivered to the host.

Trigger mode 0 is automatic after prehistory, 1 is an edge on the chosen channel,
and 2 accepts an external combinational match aligned with source_word/valid.
The edge comparator handles both packed raw samples and full Q8.8 precision
values. Raw trigger_second distinguishes a crossing in the second packed pair.
Settings latch on accepted capture/stream starts. Decoder-specific logic and
host configuration remain to be integrated through the external match hook.

The finite frontend stays enabled through prime and drain; startup outputs
outside the record interval are discarded. Streaming consumption continues
through SRAM read excursions and stops on the controller's source boundary.
ADC faults invalidate both capture modes. Epoch faults require common reset;
host software must treat their records/banks as invalid even if already ready.

`tests.txt` uses explicit ADC/CIC mocks and real downstream RTL. It checks a
3017-word raw record with a second-sample edge and 3000-word prehistory, a
48-word precision record retaining fractional bits, both recalls, and a
6001-word stream. Every host halfword is compared to independently collected
source words. It also checks busy-start rejection, configuration changes while
active, held final banks, stale-record recall rejection and streaming ADC fault.
It does not prove actual ADC pin order, CIC arithmetic, analog performance or
sustained ARM/GPMC transfer. `start-tests.txt` checks the existing accepted-start
pipeline after the new source-fault port was propagated through wrappers.

The `build_probe.py --acquisition --cdc --seed 2` diagnostic includes real ADC
and CIC RTL and the 100 MHz ADC phase PLL. IO is virtual and unconstrained. The
existing CDC audit covers the SRAM backend only: added ADC CDC paths still need
specific constraints and review. The Icarus preflight leaves vendor PLL/FIFO
primitives unresolved and is only an interface check. No bitstream is assembled
or deployed by this diagnostic.

Current hardware build failed to fit: 12782 synthesized LEs and 750 required LABs
versus 645 available. No timing or CDC audit ran. `result.json`, frozen sources,
map/fitter reports and logs record that failure. The precision hierarchy has
4172 combinational ALUTs and 3430 registers; scheduling its slow stages is the
next area reduction to evaluate, preserving the existing arithmetic.
