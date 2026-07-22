// Package fpga is the root of the owned-bitstream module (open-sds/fpga). The
// module holds the standard acquisition bitstream (standard/), its generated
// register header + decode include (standard/regs.vh, standard/regmux.vh —
// produced by the separate open-sds/codegen module), the generated register-map
// reference (standard/docs/), and the Phase-B build tooling (internal/quartus,
// cmd/buildacq).
//
// There is no Go code to run here in Phase A; this file gives the module a
// package so `go vet ./...` and `go test ./...` are green before the Phase-B
// build tooling lands. It is not imported by anything.
package fpga
