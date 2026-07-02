# 11 — Host Interface

This document specifies the **external, standards-based host/remote interface** the firmware presents to
a controlling computer: the three transports (VXI-11 over LAN, USB-TMC over the USB device port, and
USB-GPIB via a USB-to-GPIB adapter), the LeCroy/Siglent short-form SCPI command model spoken over all of
them, the byte-exact `WF?` waveform transfer
and its 346-byte WAVEDESC, and the `SCDP` hardcopy image. It is distinct from the firmware's *internal*
control-plane line protocol (spec 09 §7, a private TCP text interface); this spec is the interface a
third-party VXI-11/USB-TMC client sees.

The SCPI set maps onto the same staged setters and read-only snapshots the control plane already
exposes (spec 09): a `set`-style command becomes a staging setter applied at the frame boundary, and a
query becomes a lock-guarded snapshot/peek. The waveform export reads the most-recently published frame
(spec 03) and the active per-(channel, V/div) calibration (spec 10). Read spec 09 (staging model, status
snapshot) and spec 10 (code↔volts) alongside this document.

---

## 1. Transports

| Transport | Bearer | Discovery / framing | Notes |
|---|---|---|---|
| **VXI-11 (LAN)** | ONC RPC over **TCP** | portmap `GETPORT(DEVICE_CORE)` on port 111 → core service on the returned TCP port; RPC **record marking** | The **only** LAN SCPI path. There is **no** raw TCP SCPI ("port 5025") socket. |
| **USB-TMC (device port)** | USB Test & Measurement Class | USB-TMC bulk `DEV_DEP_MSG_OUT` / `REQUEST_DEV_DEP_MSG_IN` framing | Same SCPI byte stream as VXI-11. Gadget `g_usbtmc`, node `/dev/g_usbtmc`. |
| **USB-GPIB (adapter)** | IEEE-488.1 over a USB-A + USB-to-GPIB adapter | GPIB primary address (a configuration setting) | Kernel node `/dev/usb-gpib`, driver `gpib.ko`. Received bytes feed the identical §3 SCPI parser. |

All three transports carry the **identical** SCPI request/response byte stream (§4); they differ only in the
framing that delivers it. A command is one `\n`-terminated line; a response is the header-echoed value
(§3.1) or an IEEE-488.2 definite-length binary block (§4 waveform / §6 hardcopy).

SCPI over VXI-11/USB-TMC/USB-GPIB is the **only** instrument-control interface. This firmware runs no
on-device shell/login service; device management is out of band via the OTA agent and the host `otactl`
tool (see `01-system-architecture.md`), which is a separate path and MUST NOT be conflated with SCPI.

### 1.1 USB-TMC device identity

The USB-TMC gadget is brought up by loading `g_usbtmc.ko` with its identity supplied as module
parameters at runtime:

```
insmod /usr/bin/siglent/drivers/g_usbtmc.ko idVendor=0x<V> idProduct=0x<P> \
       iManufacturer=<m> iProduct=<p> iSerialNum=<s>
```

The SCPI byte stream then flows through device node **`/dev/g_usbtmc`**; the loaded parameters are
exposed read-only under **`/sys/module/g_usbtmc/parameters/`** (which also carries `bcdDevice`, `qlen`,
and `iPNPstring`). The gadget presents:

| Field | Value |
|---|---|
| idVendor | `0xF4EC` (62700) |
| idProduct | `0xEE3A` (60986) |
| iManufacturer | manufacturer string, e.g. `Siglent` (matches `*IDN?` field 1) |
| iProduct | model string, e.g. `SDS1102CML+` |
| iSerialNumber | per-unit serial, e.g. `SDS10BA2160661` (matches `*IDN?` field 3) |
| bcdDevice | `0` |
| qlen (gadget queue depth) | `160` |
| PNP identity string (`iPNPstring`) | `MFG:linux;MDL:g_usbtmc;CLS:PRINTER;SN:1;` |

The USB interface is a USB-TMC / USB488 interface; the gadget's baked device-ID (`CLS:PRINTER`) advertises
the printer class in the IEEE-1284 identity string. The numeric VID/PID and the manufacturer/product/serial
strings are **filled from device configuration at runtime** (read them from
`/sys/module/g_usbtmc/parameters/` or the enumerated descriptor); a differently-configured unit may report
different values. VISA resource form: `USB::<vid>::<pid>::<serial>::INSTR`.

The bulk transfer carries the SCPI line as the USB-TMC message payload. Each transfer is prefixed by the
standard USB-TMC bulk header:

```
u8  MsgID          # 1 = DEV_DEP_MSG_OUT, 2 = REQUEST_DEV_DEP_MSG_IN
u8  bTag           # transfer tag (1..255)
u8  ~bTag          # bitwise inverse of bTag
u8  reserved       # 0
u32 TransferSize   # little-endian payload byte count
u8  bmTransferAttributes  # bit0 = EOM (last transfer of the message)
u8  reserved[3]    # 0
<payload, padded with 0 to the next 4-byte boundary>
```

A `DEV_DEP_MSG_OUT` bulk-OUT delivers the command; a `REQUEST_DEV_DEP_MSG_IN` + bulk-IN returns the
response bytes, the reply's `EOM` bit (bit0 of `bmTransferAttributes`) marking the last transfer of a
query reply. The instrument reads with a bounded timeout and logs `usbtmc read time out error!` if the
host stalls mid-message. The command/query semantics, reply grammar, `WF?` blocks and `SCDP` image are
exactly as for VXI-11 — a client library that speaks USB-TMC gets the same instrument.

**Open:** the USB488 `GET_CAPABILITIES` control-transfer payload was not captured, and the
interface/endpoint descriptors below the identity above (`bMaxPacketSize`, endpoint addresses, the
USB-TMC `INDICATOR_PULSE` and per-transfer size limits) are a gadget-config detail not pinned here; a
stock USB-TMC host stack negotiates them at enumeration.

---

## 2. VXI-11 LAN transport (ONC RPC over TCP)

VXI-11 is ONC RPC (Sun RPC) carried over **TCP** and located through the portmapper. The firmware MUST
make a `GETPORT(DEVICE_CORE)` on port **111** resolve to its own `DEVICE_CORE` service:

1. **Register `DEVICE_CORE` with the portmap on TCP/UDP port 111** so a client's `GETPORT` returns the
   firmware's core-service port. A running system portmap on the target already owns port 111 (often
   with a stale `DEVICE_CORE` registration pointing at a dead port), so rather than bind 111 directly,
   bind `DEVICE_CORE` on an **ephemeral free TCP port**, then update the running portmap at
   `127.0.0.1:111`: `PMAPPROC_UNSET` (proc 4) the stale `DEVICE_CORE` mapping and `PMAPPROC_SET`
   (proc 1) the ephemeral port (mapping `prog=DEVICE_CORE, vers=1, prot=IPPROTO_TCP=6, port`). Classic
   portmap accepts `SET`/`UNSET` only from a **privileged (<1024) local source port**, so send these
   RPC calls from such a port. All three RPC programs are advertised on both UDP (proto 17) and
   TCP (proto 6).
2. Serve the `DEVICE_CORE` RPC program on that TCP port, executing `create_link` / `device_write` /
   `device_read` / `destroy_link`.

RPC program/version constants:

| Name | Program number | Version | Purpose |
|---|---|---|---|
| Portmap (`PMAP_PROG`) | `100000` (`0x186A0`) | `2` | port discovery |
| `DEVICE_CORE` | `0x0607AF` (395183) | `1` | links, read/write, status, lock, trigger, clear |
| `DEVICE_ASYNC` (abort channel) | `0x0607B0` (395184) | `1` | abort an in-progress operation |
| `DEVICE_INTR` (SRQ/interrupt channel) | `0x0607B1` (395185) | `1` | service-request (SRQ) delivery |

`GETPORT` argument is `(prog=DEVICE_CORE, vers=1, proto=IPPROTO_TCP=6, port=0)`; the 4-byte reply is the
TCP port the core service listens on (a representative unit returns `tcp/717`, with `udp/713` for the
abort/interrupt channels). The abort (`DEVICE_ASYNC`) channel is a **separate** program on a separate
port (returned as `abortPort` by `create_link`, §2.3); the query/waveform path does not require it, and
the interrupt (`DEVICE_INTR`/SRQ) channel is optional (§8).

### 2.1 RPC record marking

Each RPC message on the TCP stream is prefixed by a 4-byte **record mark**: a big-endian `uint32` whose
top bit (`0x80000000`) is the *last-fragment* flag and whose low 31 bits are the fragment byte length.
The firmware writes one fragment per message (`0x80000000 | len`) and, when reading, concatenates
fragments until the last-fragment bit is set.

### 2.2 RPC call and reply framing

All multi-byte RPC fields are **big-endian** (XDR). A **call** body is:

```
uint32 xid                      # client-chosen transaction id, echoed in the reply
uint32 msg_type   = 0           # CALL
uint32 rpc_vers   = 2
uint32 prog                     # PMAP_PROG or DEVICE_CORE
uint32 vers                     # 2 or 1
uint32 proc                     # procedure number (§2.3)
uint32 cred_flavor = 0          # AUTH_NULL
uint32 cred_len    = 0
uint32 verf_flavor = 0          # AUTH_NULL
uint32 verf_len    = 0
<procedure arguments, XDR-encoded>
```

The **reply** body the firmware returns is:

```
uint32 xid                      # == the call's xid
uint32 msg_type    = 1          # REPLY
uint32 reply_stat  = 0          # MSG_ACCEPTED
uint32 verf_flavor = 0          # AUTH_NULL
uint32 verf_len    = 0
uint32 accept_stat = 0          # SUCCESS
<procedure results, XDR-encoded>
```

XDR strings/opaque are length-prefixed and **padded to a 4-byte boundary**: a `uint32` length, the
bytes, then `0`-padding to the next multiple of 4.

### 2.3 DEVICE_CORE procedures

| Proc | Name | Arguments (XDR) | Results (XDR) |
|---|---|---|---|
| `10` | `create_link` | `int clientId; bool lockDevice; uint lock_timeout; string device` | `int error; uint lid; uint abortPort; uint maxRecvSize` (each field one 4-byte XDR word) |
| `11` | `device_write` | `uint lid; uint io_timeout; uint lock_timeout; int flags; opaque data` | `int error; uint size` |
| `12` | `device_read` | `uint lid; uint requestSize; uint io_timeout; uint lock_timeout; int flags; int termChar` | `int error; int reason; opaque data` |
| `13` | `device_readstb` | `uint lid; int flags; uint lock_timeout; uint io_timeout` | `int error; u8 stb` (IEEE-488.2 status byte) |
| `14` | `device_trigger` | `uint lid; int flags; …timeouts` | `int error` (asserts `*TRG`) |
| `15` | `device_clear` | `uint lid; int flags; …timeouts` | `int error` (device clear) |
| `16` / `17` | `device_remote` / `device_local` | `uint lid; …` | `int error` |
| `18` / `19` | `device_lock` / `device_unlock` | `uint lid; …` | `int error` (exclusive lock) |
| `20` | `device_enable_srq` | `uint lid; bool enable; opaque handle` | `int error` |
| `23` | `destroy_link` | `uint lid` | `int error` |
| `25` / `26` | `create_intr_chan` / `destroy_intr_chan` | interrupt-channel setup/teardown | `int error` |

The abort procedure `device_abort(uint lid)` lives on the **`DEVICE_ASYNC`** program (`0x0607B0`) at the
`abortPort` from `create_link`, and SRQ callbacks are delivered on the **`DEVICE_INTR`** program
(`0x0607B1`). The query/waveform path uses only procs 10/11/12/23.

- `create_link`: `device` is the string **`"inst0"`**. On success `error = 0`; `lid` is the link id to
  pass to every subsequent call; `maxRecvSize` is the largest block the client may `device_write` in one
  call — the firmware reports **`0x800000`** (8388608). `abortPort` is the `DEVICE_ASYNC` TCP port. The
  instrument serves a **single link**; always `destroy_link` when done, or a dropped TCP connection can
  leave the link stuck until it times out and block the next `create_link`.
- `device_write`: `data` is the SCPI command **including its trailing `\n`**. `flags` bit `0x8` = `END`
  (assert EOI on the last byte). `size` echoes the bytes accepted.
- `device_read`: returns up to `requestSize` bytes of the pending response. `flags` bit `0x80` = `TERMCHR`
  (honour `termChar` on read). `reason` is a bitmask: `END` = `0x4` (response complete), `CHR`/`TERMCHR`
  = `0x2` (`termChar` matched), `REQCNT` = `0x1`; `0` means more data remains — the client loops
  `device_read` until `error != 0` or a non-zero `reason`. This loop is how a `WF?` block larger than one
  read is reassembled.
- `destroy_link`: releases the link.
- `error` is `0` on success; non-zero VXI-11 error codes are e.g. `4` invalid link, `11` device locked,
  `15` io timeout, `23` abort.

### 2.4 Concrete connect → query sequence

```
1. TCP connect to <host>:111.
2. call PMAP_PROG(v2) proc 3 GETPORT (DEVICE_CORE, 1, TCP, 0)  -> core_port
3. TCP connect to <host>:core_port.
4. call DEVICE_CORE(v1) proc 10 create_link (1, 0, 0, "inst0")
      -> error, lid, abortPort, maxRecvSize(=0x800000)
5. call DEVICE_CORE(v1) proc 11 device_write (lid, 5000, 0, END=8, "<CMD>\n")
6. loop call DEVICE_CORE(v1) proc 12 device_read (lid, maxRecvSize, 5000, 0, 0, 0)
      accumulate data until error != 0 or reason != 0
7. call DEVICE_CORE(v1) proc 23 destroy_link (lid)
```

Steps 5–6 repeat per command on the same link; `io_timeout`/`lock_timeout` are milliseconds
(`5000`/`0` are typical). A query that returns nothing (a pure setter) simply yields an empty read.

---

## 3. SCPI command model

The instrument speaks the **LeCroy/Siglent short-form** command set (not the modern `:CHANnel:…` SCPI
tree). Header mode is `CHDR SHORT`. Both transports carry the same `\n`-terminated line grammar.

- **Separators:** a colon `:` separates a compound header (`C1:VDIV`); a space separates the header from
  its argument (`TDIV 1E-3`); a semicolon `;` separates multiple commands on one line; a comma separates
  arguments.
- **Queries** end in `?` and return a `\n`-terminated response.

### 3.1 Reply grammar

A query reply is `HEADER VALUE[UNIT]`, `\n`-terminated:

- The **header** echoes the query's channel prefix and keyword (e.g. `C1:VDIV`, `TDIV`, `SARA`).
- Numeric values are **scientific notation** (`%.2E`, e.g. `5.00E-02`), with the SI **unit suffix**
  appended where applicable. The suffix case is fixed by the firmware: volts are upper-case `V`, seconds
  are **lower-case `s`** (`TDIV 5.00E-02s`), plus `Hz` and `Sa`.
- **Exception — sample-rate replies use engineering SI-prefix notation, not `%.2E`.** `SARA?` returns
  `SARA <mantissa><SI-prefix>Sa` (e.g. `SARA 12.50KSa`, i.e. `12.50` × 10³ samples), with the multiplier
  as a k/M/G prefix letter rather than a `%.2E` exponent, and the unit `Sa` (not `Sa/s`).
- Enum values are echoed **verbatim**; booleans as `ON`/`OFF`.
- Channel-scoped commands are prefixed `Cn:` (`C1`, `C2`); global commands have no prefix.
- `CHDR OFF` suppresses the header, returning just `VALUE[UNIT]`; `CHDR ON`/`SHORT` echo it. Power-on
  default is `SHORT`.
- The last `device_read` of a reply carries `reason = END (0x4)`.

### 3.2 Query set

These queries and their exact reply formats are part of the interface contract:

| Query | Reply format | Meaning |
|---|---|---|
| `*IDN?` | `Siglent,<model>,<serial>,<firmware>` | maker, model, serial, firmware (comma-separated, no header) |
| `CHDR?` | `CHDR SHORT` | header mode |
| `SARA?` | `SARA <rate>Sa` (e.g. `SARA 12.50KSa`) | current sample rate |
| `SANU? Cn` | `SANU <count>` (e.g. `SANU 20000`) | number of sample points for the channel |
| `Cn:VDIV?` | `Cn:VDIV <v>V` (e.g. `C1:VDIV 5.00E-02V`) | vertical scale, volts/div |
| `Cn:OFST?` | `Cn:OFST <v>V` (e.g. `C1:OFST 5.00E-03V`) | vertical offset, volts |
| `TDIV?` | `TDIV <s>s` (e.g. `TDIV 5.00E-02s`) | timebase, seconds/div |
| `Cn:TRA?` | `Cn:TRA ON` / `Cn:TRA OFF` | channel trace on/off |
| `SAST?` | `SAST <state>` (e.g. `SAST Ready`) | acquisition/sample status |
| `WFSU?` | `WFSU SP,<sp>,NP,<np>,FP,<fp>,SN,<sn>` | waveform-setup: sparsing, num-points, first-point, segment |
| `Cn:WF? DESC` | binary block (§4/§5) | WAVEDESC descriptor |
| `Cn:WF? DAT2` | binary block (§4) | raw 8-bit sample codes |
| `SCDP` | BMP image (§6) | screen hardcopy |

`*IDN?`'s four fields are maker, model, serial, firmware — comma separated with no `HEADER` prefix
(e.g. `Siglent,SDS1102CML+,SDS10BA2160661,6.01.01.21R2`). The maker token is the literal string
`Siglent`; the model derives from the unit's model code (spec 01), the serial is read from
`usr/system/system_info.dat` (the same store that holds the MAC at offset `0x14`), and the firmware
string is configuration-derived.

Any query keyword from §3.3 not tabulated above answers in the §3.1 header-echo grammar for its Kind:
a Float in `%.2E`+unit (e.g. `TRLV?` → `TRLV 1.00E-01V`), an enum verbatim (e.g. `TRMD?` → `TRMD AUTO`,
`Cn:CPL?` → `C1:CPL A1M`), a bool as `ON`/`OFF`.

### 3.3 Write-command inventory

Every keyword below is spoken as a command (no `?`) to *set* the corresponding state and, where a query
form exists, with `?` to read it. A set maps onto the matching staging setter (spec 09: `SetTdiv`,
`SetOffsetDAC`, `SetTrigLevel`, `SetNorm`, …), applied by the single bus owner at the next frame
boundary; a query maps onto the corresponding `Snapshot`/peek. The SCPI handler itself **never touches
the GPMC bus** (spec 09 §7 discipline). A pure set is silent (no reply until the next query); a rejected
argument returns the §3.4 error token.

| Family | Commands (short form) | Argument / notes |
|---|---|---|
| Common (IEEE-488.2) | `*IDN?` `*RST` `*CLS` `*OPC` `*STB?` `*ESR?` `*ESE` `*SRE` `*WAI` `*TST?` `*CAL?` `*SAV` `*RCL` | `*RST` = Default Setup; `*SAV <n>`/`*RCL <n>` setup memory |
| Comm / util | `CHDR ON\|OFF\|SHORT` `BUZZ ON\|OFF` `INR?` `CMR?` `ALST?` | header echo, buzzer, internal/comm/all status registers |
| Channel (`Cn:…`) | `Cn:VDIV <v>` `Cn:OFST <v>` `Cn:CPL <enum>` `Cn:ATTN <x>` `Cn:BWL ON\|OFF` `Cn:TRA ON\|OFF` `Cn:UNIT <enum>` `Cn:SKEW <s>` `Cn:INVS ON\|OFF` | `n`∈{1,2}. `VDIV`/`OFST` in volts (sci/SI accepted). `CPL` coupling (`A1M`/`D1M`/`GND` form). `ATTN` probe factor. `BWL` 20 MHz limit. `TRA` trace, `INVS` invert |
| Timebase | `TDIV <s>` `TRDL <s>` | seconds/div; trigger delay (position) |
| Trigger | `TRMD <mode>` `TRSE <src>` `TRSL <slope>` `TRCP <cpl>` `TRLV <v>` `TRLV2 <v>` | `TRMD`∈{`AUTO`,`NORM`,`SINGLE`,`STOP`}; `TRSL`∈{`POS`,`NEG`,`WINDOW`}; `TRLV`/`TRLV2` level in volts |
| Run-state verbs | `ARM` `STOP` `FRTR` `ASET` (`AUTO_SETUP`) | value-less momentary actions (no query form): arm/run, stop, force-trigger, auto-setup |
| Acquire | `ACQW <mode>` `AVGA <n>` `SAST?` `SARA?` `SANU?` `XYDS ON\|OFF` | acq mode (sample/peak/average/eres); `AVGA` average count; X-Y display |
| Waveform | `WFSU <pairs>` `Cn:WF? DAT2\|DESC` | §4 / §3.5 |
| Measure / cursor | `PACU` `PAVA?` `CRMS` `CRVA?` `CRST` `MEAD?` | parameter/cursor measurement |
| Display | `GRDS` `INTS` `PESU` `MENU` | grid / intensity / persistence / menu |
| Save / recall | `*SAV` `*RCL` `PNSU` `STPN` `RCPN` | setups & panel memory |
| Pass/Fail | `PFDD` `PFDS` `PFCT` `PFST` `PFSL` `PFCM` | mask test display/source/control/state/select/compare |
| Hardcopy | `SCDP` `HCSU` | screen dump (§6), hardcopy setup |
| Serial / maintenance | `SRLN` / `SRLN?` `SGLT-UPGRADE` `SGLT-UPGRADE_CFG` `IDN-SGLT-PRI` `MD5_SRLN?` `MAC_GET` `LOAD:CALI:FILE?` | privileged; `SRLN?` replies `SRLN <Default,\|Changed,…>`. Out of scope for a v1 controller |

`*RST` restores the standard "Default Setup". Channel suffixes are range-checked to `C1`/`C2` (a `C9`
yields the §3.4 suffix error).

### 3.4 Parser error tokens

Malformed input is rejected with these exact `\n`-terminated tokens (a device-side implementer emits
them; a controller may receive them):

| Condition | Token |
|---|---|
| Bad/oversized mnemonic, bad argument/keyword | `Command header error` |
| Bad `:`/space separation (e.g. trailing colon) | `Header separator error` |
| Unknown command / unmapped verb | `Undefined header` |
| Channel suffix out of range (e.g. `C9`) | `Header suffix out of range` |
| Argument value outside range | `Data out of range` |
| Unknown macro/`*DDT` header | `Macro header not found` |

The full IEEE-488.2 error-token set (`Command error`, `Numeric data error`, `Query INTERRUPTED`,
`Query UNTERMINATED`, the `Macro …` family, …) is also available to the same parser.

### 3.5 Waveform-setup keyword ranges (`WFSU`)

`WFSU` configures the next `Cn:WF?` transfer; it takes any subset of `keyword,value` pairs, in any order:

| Keyword | Meaning | Parser range | Notes |
|---|---|---|---|
| `SP` | sparsing (decimation) | `0–255` | output every `SP`-th point; `0`/`1` = no decimation |
| `NP` | number of points | `0–81920` | output count; `0` = all remaining from `FP`. Auto-clamped to `min(NP, ceil((recordLen − FP)/SP))` |
| `FP` | first point (0-based) | `0–81920` | start offset into the record |
| `SN` | segment number | `≥ 0` | for sequence/segmented acquisition |
| `TYPE` | transfer type | `0` or `1` | boolean mode flag |

`WFSU?` → `WFSU SP,<sp>,NP,<np>,FP,<fp>,SN,<sn>`; power-on default `SP,1,NP,0,FP,0,SN,0`. An
out-of-range value is rejected and the setting left unchanged (no transfer occurs).

> **Hard limit — clamp `FP` client-side.** The `WFSU` parser accepts `FP` up to `81920`, but the `WF?`
> readout asserts `FP ≤ recordLen − 1` (e.g. `≤ 20479`) and **crashes the firmware process** if
> violated. Read the current record length first (`WAVE_ARRAY_COUNT` / `LAST_VALID_PNT+1`, or `SANU?`)
> and never let `FP` reach it; the record length varies slightly per acquisition, so re-read it.

---

## 4. Waveform transfer (`Cn:WF?`)

`Cn:WF?` has two data selectors, each returning an IEEE-488.2 **definite-length arbitrary block** after
a short `Cn:WF ALL,` header:

```
Cn:WF ALL,#9<9-digit byte-length><that many bytes>
```

`#9` means: `#`, one digit (`9`) giving the number of length digits that follow, then a 9-digit decimal
byte count, then exactly that many payload bytes (the reply is then `\n`-terminated). Reassemble the
block across multiple `device_read` calls until `reason = END`. A full ~20 k-point record fits in one
`device_read` given `maxRecvSize = 0x800000`; paging (§3.5) is for windowing/decimation.

- **`Cn:WF? DAT2`** — payload is the **8-bit sample codes**, one byte per sample, **no descriptor**.
  A full deep record is 20480 codes. Code → volts requires the WAVEDESC scaling (§5.1). These are the
  **deep-frame / `WF?`-scale codes** (`VERTICAL_GAIN = Vdiv/50`, unsigned byte centred at 128), the same
  8-bit ADC codes the deep acquisition engine drains (spec 03). **They are NOT the live-roll codes**
  (`0x41/0x59`, ~`Vdiv/25`, half-scale, config-dependent centre): presenting raw roll codes here would
  read exactly **half** the true voltage under the DESC's `Vdiv/50` gain. `WFSU` (`SP`/`NP`/`FP`/`SN`)
  selects sparsing/point-count/first-point/segment before the transfer, and the block header reports the
  actual transferred count.
- **`Cn:WF? DESC`** — payload is the **346-byte WAVEDESC** block (§5). Its length prefix is
  `#9000000346`.

---

## 5. WAVEDESC binary layout

The WAVEDESC is a **346-byte** block using the LeCroy **`DSO`** template, **little-endian**
(`COMM_ORDER = 1` = LOFIRST). Field offsets (byte offset from the start of the block):

| Offset | Field | Type | Value / meaning |
|---|---|---|---|
| `0` | `DESCRIPTOR_NAME` | 16-byte string | `"WAVEDESC"` |
| `16` | `TEMPLATE_NAME` | 16-byte string | `"DSO"` |
| `32` | `COMM_TYPE` | i16 | `0` = **8-bit** samples (matches the DAT2 byte codes) |
| `34` | `COMM_ORDER` | i16 | `1` = **LOFIRST** (little-endian) |
| `36` | `WAVE_DESCRIPTOR` | i32 | descriptor length = `346` |
| `60` | `WAVE_ARRAY_1` | i32 | byte length of the sample array (invariant: `= WAVE_ARRAY_COUNT × sizeof(sample)`) |
| `116` | `WAVE_ARRAY_COUNT` | i32 | sample count of the **full** record (e.g. `20480`) |
| `120` | `PNTS_PER_SCREEN` | i32 | points across the screen (e.g. `20478`) |
| `124` | `FIRST_VALID_PNT` | i32 | index of the first valid sample |
| `128` | `LAST_VALID_PNT` | i32 | index of the last valid sample (`WAVE_ARRAY_COUNT − 1`) |
| `132` | `FIRST_POINT` | i32 | = the `FP` set via `WFSU` (§3.5) |
| `136` | `SPARSING_FACTOR` | i32 | = the `SP` set via `WFSU` |
| `140` | `SEGMENT_INDEX` | i32 | = the `SN` set via `WFSU` |
| `144` | `SUBARRAY_COUNT` | i32 | segment/subarray count |
| `156` | `VERTICAL_GAIN` | f32 | volts per code = `(Vdiv / 50) · probe_factor` (**50 codes/div**; `probe_factor` = `1` for 1×, `10` for 10×) |
| `160` | `VERTICAL_OFFSET` | f32 | vertical offset, volts (= the `Cn:OFST` value) |
| `164` | `MAX_VALUE` | f32 | `+127` (signed-8-bit code ceiling, grid top) |
| `168` | `MIN_VALUE` | f32 | `−128` (signed-8-bit code floor, grid bottom) |
| `176` | `HORIZ_INTERVAL` | f32 | seconds per sample (`= 1 / SARA`) |
| `180` | `HORIZ_OFFSET` | f64 | time of the first sample = `−(WAVE_ARRAY_COUNT · HORIZ_INTERVAL) / 2` (trigger at frame centre, so negative) |
| `188` | `PIXEL_OFFSET` | f64 | first-pixel time offset |
| `196` | `VERTUNIT` | 48-byte unit | vertical unit (`V`) |
| `244` | `HORUNIT` | 48-byte unit | horizontal unit (`s`) |
| `296` | `TRIGGER_TIME` | `time_stamp` (16 bytes) | acquisition trigger timestamp |
| `344` | `WAVE_SOURCE` | i16 (enum) | source channel |

`COMM_TYPE = 0` ↔ the 8-bit DAT2 codes; `WAVE_ARRAY_COUNT`/`LAST_VALID_PNT` ↔ the **full** record (the
transferred slice length is the `#9` block byte count); `HORIZ_INTERVAL` ↔ the `SARA?` rate. The firmware
fills `VERTICAL_GAIN`/`VERTICAL_OFFSET` from the active per-(channel, V/div) calibration and offset
(spec 10 / spec 06), `HORIZ_INTERVAL`/`WAVE_ARRAY_COUNT` from the current timebase/record (spec 04), and
`FIRST_POINT`/`SPARSING_FACTOR`/`SEGMENT_INDEX` from the `WFSU` read window.

**Open:** the enumerated code meanings of the DSO-template enum fields (`WAVE_SOURCE` channel codes, the
timebase/vertical-coupling/record-type enums that occupy other DSO-template slots) and the internal
byte layout of the `TRIGGER_TIME` `time_stamp` are not pinned from the sources; a client that needs only
code→volts and the time axis uses the four scaling fields and the counts above.

### 5.1 Reconstruction

For sample index `n`, the DAT2 payload byte `raw_n` is **unsigned, centred at 128**; center it to the
signed code the descriptor scales (`MAX_VALUE = +127`, `MIN_VALUE = −128`):

```
signed_n = raw_n − 128                              # −128 … +127
volts_n  = signed_n · VERTICAL_GAIN − VERTICAL_OFFSET
t_n      = HORIZ_OFFSET + n · HORIZ_INTERVAL
```

Both `VERTICAL_GAIN` and `VERTICAL_OFFSET` are in the descriptor, so a DAT2 code array plus its DESC
block fully reconstructs the calibrated waveform. This is the **only** endianness/format contract a host
needs: little-endian descriptor fields, 8-bit codes centred at 128, `VERTICAL_GAIN = (Vdiv/50)·probe`
(50 codes/div — the deep-frame/`WF?` scale), the linear transfer above.

---

## 6. Hardcopy image (`SCDP`)

`SCDP` returns a **Windows BMP** of the full screen. The image is the RGB565 framebuffer (spec 07)
serialized as a `BI_BITFIELDS` 16-bpp bitmap:

| BMP field | Value |
|---|---|
| Magic | `"BM"` |
| Pixel-data offset | `66` |
| DIB header | `BITMAPINFOHEADER` (40 bytes) |
| Width | `800` |
| Height | `−480` (negative = **top-down** row order) |
| Planes | `1` |
| Bits/pixel | `16` |
| Compression | `3` (`BI_BITFIELDS`) |
| Red mask | `0xF800` (bits 15:11) |
| Green mask | `0x07E0` (bits 10:5) |
| Blue mask | `0x001F` (bits 4:0) |

The 14-byte `BITMAPFILEHEADER` + 40-byte `BITMAPINFOHEADER` + 12-byte bitfield masks place pixel data at
offset **66**; pixels are the raw 800×480 RGB565 words (no RLE). The channel order matches the display
framebuffer exactly (spec 07: R[15:11] G[10:5] B[4:0], no byte swap), so `SCDP` is a direct dump of the
rendered screen.

`SCDP` emits only the uncompressed `BI_BITFIELDS` bitmap above; there is no RLE-compressed hardcopy
variant in this contract.

---

## 7. Mapping onto the firmware

| Host action | Firmware path |
|---|---|
| `set`-style SCPI command | staging setter (spec 09 §2), applied by the bus owner at the frame boundary |
| `?` query of a live setting | lock-guarded snapshot/peek (spec 09 §8) — no bus access |
| `Cn:WF? DAT2` | copy of the most-recently published frame's channel codes on the deep-frame/`WF?` scale (`Vdiv/50`, centred at 128 — **not** the half-scale live-roll codes; spec 03 arena), sparsed per `WFSU` |
| `Cn:WF? DESC` | WAVEDESC assembled from the active timebase (spec 04) + per-(channel, V/div) cal (spec 10) |
| `SCDP` | serialize the current framebuffer (spec 07) as the §6 BMP |

The SCPI/VXI-11/USB-TMC servers run in their own workers and are **producers/consumers only**: they call
staging setters and read-only snapshots, exactly like the internal line protocol (spec 09 §7). They never
issue a GPMC access (spec 01 single-owner discipline).

---

## 8. Open

- **VXI-11 async/interrupt channels.** The procedure numbers are pinned (§2.3: `device_abort` on
  `DEVICE_ASYNC`, `create_intr_chan`/`destroy_intr_chan`/`device_enable_srq` for `DEVICE_INTR`), but
  SRQ delivery and abort are not required for the query/waveform path; their full service is optional.
- **USB-TMC capability detail** (§1.1): the USB488 `GET_CAPABILITIES` payload and the endpoint/packet-size
  descriptors were not captured.
- **WAVEDESC enum-code meanings** (§5): the enumerated values of `WAVE_SOURCE` and the DSO-template
  coupling/timebase/record enums, plus the `TRIGGER_TIME` `time_stamp` internal layout.
- **Per-keyword argument ranges/defaults** (§3.3): the full inventory of settable keywords is pinned, but
  the exact numeric ranges/defaults for each (beyond the `WFSU` keywords in §3.5) are a per-keyword
  follow-up; queries beyond §3.2 answer in the §3.1 header-echo grammar.
- **`SGLT-*` maintenance framing** (§3.3): the on-wire payload framing and authorization gate of the
  privileged serial/upgrade commands are out of scope for a v1 controller.
