// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// layerNameTables are the name tables the web page's layers read too, by
// the name layers.js knows them under.
var layerNameTables = map[string]map[int]string{
	"cc1101Regs": cc1101Regs, "cc1101Strobes": cc1101Strobes, "nrf24Regs": nrf24Regs,
	"enc28j60Bank0": enc28j60Bank0, "enc28j60Bank1": enc28j60Bank1, "enc28j60Bank2": enc28j60Bank2,
	"enc28j60Bank3": enc28j60Bank3, "mrf24Short": mrf24Short, "mrf24Long": mrf24Long, "st25Cmds": st25Cmds,
	"st25RegsA": st25RegsA, "st25RegsB": st25RegsB, "st25RegsTest": st25RegsTest, "adxl345Regs": adxl345Regs,
	"adns5020Regs": adns5020Regs, "ade7758Regs": ade7758Regs, "sdCmdNames": sdCmdNames, "sdACmdNames": sdACmdNames,
	"max72xxRegs": max72xxRegs, "lm75Regs": lm75Regs, "mlxRegs": mlxRegs, "atshaOps": atshaOps,
	"atshaStatus": atshaStatus, "hdcpRegs": hdcpRegs, "hdcpMsgs": hdcpMsgs, "ltcCmds": ltcCmds, "ltcDACs": ltcDACs,
	"tcaRegs": tcaRegs, "mcp23008Regs": mcp23008Regs, "mcp23017Regs": mcp23017Regs, "mxcRegs": mxcRegs,
	"itmExceptions": itmExceptions, "cfpModules": cfpModules, "usbRequests": usbRequests, "usbDescs": usbDescs,
}

// LayerTablesJS is layer_tables.js: the layers' name and command tables as
// one JavaScript object, so the web page's layers name what the scope's do.
// TRLC-LINKS: REQ-SDS-241
func LayerTablesJS() string {
	obj := map[string]any{}
	for name, t := range layerNameTables {
		obj[name] = jsKeyed(t)
	}
	ints := map[string]any{}
	for k, v := range nrf24Size {
		ints[fmt.Sprint(k)] = v
	}
	obj["nrf24Size"] = ints
	ints = map[string]any{}
	for k, v := range ade7758Bits {
		ints[fmt.Sprint(k)] = v
	}
	obj["ade7758Bits"] = ints
	flash := map[string]any{}
	for k, c := range flashCmds {
		flash[fmt.Sprint(k)] = []any{c.name, c.addr, c.dummy, c.rest, c.erase}
	}
	obj["flashCmds"] = flash
	amulet := map[string]any{}
	for k, c := range amuletCmds {
		amulet[fmt.Sprint(k)] = []string{c[0], c[1]}
	}
	obj["amuletCmds"] = amulet
	ow := map[string]any{}
	for k, c := range owROMCmds {
		ow[fmt.Sprint(k)] = []any{c.name, c.rom}
	}
	obj["owROMCmds"] = ow
	var sb strings.Builder
	sb.WriteString("// ENGMODEL-OWNER-UNIT: FU-WEB-PACKETS\n")
	sb.WriteString("// layer_tables.js - the layers' name and command tables, written from the scope's own\n")
	sb.WriteString("// (internal/analysis LayerTablesJS; TestLayerTablesJS keeps this file equal to them).\n")
	sb.WriteString("\"use strict\";\n")
	sb.WriteString("const LAYER_TABLES = {\n")
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		b, _ := json.Marshal(obj[k])
		sb.WriteString("  " + k + ": " + string(b))
		if i+1 < len(keys) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("};\n")
	sb.WriteString("if (typeof module !== \"undefined\" && module.exports) module.exports = { LAYER_TABLES };\n")
	return sb.String()
}

// jsKeyed is a table keyed by its numbers as decimal strings.
// TRLC-LINKS: REQ-SDS-241
func jsKeyed(t map[int]string) map[string]string {
	out := map[string]string{}
	for k, v := range t {
		out[fmt.Sprint(k)] = v
	}
	return out
}
