# Separate seek/read command counts

The recall reader retains read_count from PREP and selects it or distance using
the registered command_discard bit. This removes distance==0 from the wide
command-count load without adding command cycles or changing command semantics.

Passed both continuation modes: empty, partial/wrapping, one-word, zero-seek
17-word and 8192-word records; invalid geometry/freeze rejection and injected
host/lost-freeze/short/extra-read faults. Deep integration passes raw and
fractional capture/recall. Full AW=19 simulation also passes: 524288 words (2 MiB), exact host readback,
unchanged modeled SRAM and final bank release. This proves modeled physical
address width, not electrical operation on the device.

Balanced seed 2 real-ADC diagnostic: 7576 LE, 626 LAB, 26 M9K, setup -2.090 ns,
recovery -3.610 ns. CDC audit passes. Previous baseline 5ccd992: 7579 LE,
627 LAB, setup -2.046 ns, recovery -3.326 ns. The worst path moved from recall
command-count selection to host packer data_candidate. Overall timing remains
unclosed; this is not a deployable bitstream. Sources other than finite_recall.v
match 5ccd992; source hashes are retained in result.json.
