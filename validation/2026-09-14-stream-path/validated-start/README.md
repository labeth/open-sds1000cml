# Validated starts — rejected timing experiment

START_VALIDATED bypassed repeated child checks only in the integrated path;
standalone default remained validating. Integration and standalone fault tests
passed. Negative tests confirmed both finite/backend caller-contract assertions
fire on an invalid qualified start. Run test_contract.py against the archived
RTL; it deliberately omits child datapaths and does not prove transport behavior.

Balanced seed-2 build: 9381 LEs, 7720 registers, 44 M9Ks, 645/645 LABs.
Setup -3.853 ns; recovery -4.481 ns; CDC audit failed. Worst path remained
host packer fault through acceptance into backend launch_stream. Compared with
the committed launch baseline (-3.284 ns, 642 LABs), this is a regression.
All 31 build source hashes were verified before archiving.

The four RTL edits were reverted to the committed launch baseline. These
combinational simplifications do not solve the acceptance path; registered
command/acceptance stages are the next architectural direction to investigate.
No FPGA image was deployed and no scope permanent storage was written.
