# 11 — Host Interface

This document specifies the **external, standards-based host/remote interface** the firmware presents to
a controlling computer: the two transports (VXI-11 over LAN, USB-TMC over the device port), the
LeCroy/Siglent short-form SCPI command model spoken over both, the byte-exact `WF?` waveform transfer
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
| **USB-TMC (device port)** | USB Test & Measurement Class | USB-TMC bulk `DEV_DEP_MSG_OUT` / `REQUEST_DEV_DEP_MSG_IN` framing | Same SCPI byte stream as VXI-11. Gadget `g_usbtmc`. |

Both transports carry the **identical** SCPI request/response byte stream (§4); they differ only in the
framing that delivers it. A command is one `\n`-terminated line; a response is the header-echoed value
(§3.1) or an IEEE-488.2 definite-length binary block (§4 waveform / §6 hardcopy).

### 1.1 USB-TMC device identity

The USB-TMC gadget presents:

| Field | Value |
|---|---|
| idVendor | `0xF4EC` |
| idProduct | `0xEE3A` |
| bDeviceClass | USB-TMC (Test & Measurement Class) |
| iProduct | model string, e.g. `SDS1102CML+` |
| iSerialNumber | per-unit serial (matches `*IDN?` field 3) |
| bcdDevice | `0` |

The bulk transfer carries the SCPI line as the USB-TMC message payload: a `DEV_DEP_MSG_OUT` bulk-OUT
transfer delivers the command; a `REQUEST_DEV_DEP_MSG_IN` + bulk-IN returns the response bytes (the
response's `EOM` bit marks the last transfer of a query reply). The command/query semantics, reply
grammar, `WF?` blocks and `SCDP` image are exactly as for VXI-11 — a client library that speaks USB-TMC
gets the same instrument.

**Open:** the interface/endpoint descriptors beyond the identity above (`bMaxPacketSize`, endpoint
addresses, the USB-TMC capability/`INDICATOR_PULSE`/`bulkOut` transfer-size limits) are a gadget-config
detail not pinned here; a stock USB-TMC host stack negotiates them.

---

## 2. VXI-11 LAN transport (ONC RPC over TCP)

VXI-11 is ONC RPC (Sun RPC) carried over **TCP** and located through the portmapper. The firmware MUST:

1. Run a **portmapper** responder on TCP/UDP port **111** that answers `GETPORT` for the VXI-11
   `DEVICE_CORE` program.
2. Serve the `DEVICE_CORE` RPC program on a TCP port, executing `create_link` / `device_write` /
   `device_read` / `destroy_link`.

RPC program/version constants:

| Name | Program number | Version |
|---|---|---|
| Portmap (`PMAP_PROG`) | `100000` (`0x186A0`) | `2` |
| `DEVICE_CORE` | `0x0607AF` (395183) | `1` |
| `DEVICE_ASYNC` (abort channel) | `0x0607B0` | `1` |
| `DEVICE_INTR` (SRQ/interrupt channel) | `0x0607B1` | `1` |

`GETPORT` argument is `(prog=DEVICE_CORE, vers=1, proto=IPPROTO_TCP=6, port=0)`; the 4-byte reply is the
TCP port the core service listens on. The abort (`DEVICE_ASYNC`) channel is a **separate** program on a
separate port (returned as `abortPort` by `create_link`, §2.3); the query/waveform path does not require
it, and the interrupt (`DEVICE_INTR`/SRQ) channel is optional (§8).

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
| `23` | `destroy_link` | `uint lid` | `int error` |

- `create_link`: `device` is the string **`"inst0"`**. On success `error = 0`; `lid` is the link id to
  pass to every subsequent call; `maxRecvSize` is the largest block the client may `device_write` in one
  call — the firmware reports **`0x800000`** (8388608). `abortPort` is the `DEVICE_ASYNC` TCP port.
- `device_write`: `data` is the SCPI command **including its trailing `\n`**. `flags` bit `0x8` = `END`
  (last block of the message). `size` echoes the bytes accepted.
- `device_read`: returns up to `requestSize` bytes of the pending response. `reason` is a bitmask:
  `END` = `0x4` (response complete), `CHR` = `0x2` (`termChar` matched); `0` means more data remains —
  the client loops `device_read` until `error != 0` or a non-zero `reason`. `termChar` (used only when
  `flags` requests it) is the line terminator. This loop is how a `WF?` block larger than one read is
  reassembled.
- `destroy_link`: releases the link.

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

### 3.1 Reply grammar

A query reply is `HEADER VALUE[UNIT]`, `\n`-terminated:

- The **header** echoes the query's channel prefix and keyword (e.g. `C1:VDIV`, `TDIV`, `SARA`).
- Numeric values are **scientific notation** (e.g. `5.00E-02`), with the SI **unit suffix** appended
  where applicable (`V`, `s`, `Sa`).
- Channel-scoped commands are prefixed `Cn:` (`C1`, `C2`); global commands have no prefix.
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

`*IDN?`'s four fields are maker (`Siglent`), model (e.g. `SDS1102CML+`), serial, firmware — comma
separated with no `HEADER` prefix.

### 3.3 Setters and the wider command inventory

The dialect's namespace (short-form keywords such as `VDIV`, `OFST`, `TDIV`, `TRA`, `TRLV`, `TRMD`,
`TRSL`, `TRSE`, `CPL`, `BWL`, `SARA`, `SANU`, `SAST`, `WFSU`, `WF`, `SCDP`, `INR`) is spoken as commands
(no `?`) to *set* the corresponding instrument state. A set command maps onto the matching staging setter
in spec 09 (`SetTdiv`, `SetOffsetDAC`, `SetTrigLevel`, `SetNorm`, …), applied by the single bus owner at
the next frame boundary; a query maps onto the corresponding `Snapshot`/peek. The SCPI handler itself
**never touches the GPMC bus** (spec 09 §7 discipline).

**Open:** the full write-command inventory — every settable keyword, its argument syntax, valid ranges,
defaults, and the exact reply for queries beyond §3.2 (`TRLV?`, `TRMD?`, `Cn:CPL?`, `INR?`, …) — is not
pinned in this document. Implement the header-echo grammar of §3.1 for them and treat the argument
range/default table as a per-keyword follow-up.

---

## 4. Waveform transfer (`Cn:WF?`)

`Cn:WF?` has two data selectors, each returning an IEEE-488.2 **definite-length arbitrary block** after
a short `Cn:WF ALL,` header:

```
Cn:WF ALL,#9<9-digit byte-length><that many bytes>
```

`#9` means: `#`, one digit (`9`) giving the number of length digits that follow, then a 9-digit decimal
byte count, then exactly that many payload bytes (the reply is then `\n`-terminated). Reassemble the
block across multiple `device_read` calls until `reason = END`.

- **`Cn:WF? DAT2`** — payload is the **raw 8-bit sample codes**, one byte per sample, **no descriptor**.
  A full deep record is 20480 codes. Code → volts requires the WAVEDESC scaling (§5.1). The codes are the
  same 8-bit ADC codes the acquisition engine drains (spec 03); `WFSU` (`SP`/`NP`/`FP`/`SN`) selects
  sparsing/point-count/first-point/segment before the transfer.
- **`Cn:WF? DESC`** — payload is the **346-byte WAVEDESC** block (§5). Its length prefix is
  `#9000000346`.

---

## 5. WAVEDESC binary layout

The WAVEDESC is a **346-byte** block using the LeCroy **`DSO`** template, **little-endian**
(`COMM_ORDER = 1` = LOFIRST). Established field offsets (byte offset from the start of the block):

| Offset | Field | Type | Value / meaning |
|---|---|---|---|
| `0` | `DESCRIPTOR_NAME` | 16-byte string | `"WAVEDESC"` |
| `16` | `TEMPLATE_NAME` | 16-byte string | `"DSO"` |
| `32` | `COMM_TYPE` | i16 | `0` = **8-bit** samples (matches the DAT2 byte codes) |
| `34` | `COMM_ORDER` | i16 | `1` = **LOFIRST** (little-endian) |
| `36` | `WAVE_DESCRIPTOR` | i32 | descriptor length = `346` |
| `116` | `WAVE_ARRAY_COUNT` | i32 | sample count (e.g. `20480`) |
| `120` | `PNTS_PER_SCREEN` | i32 | points across the screen (e.g. `20478`) |
| `156` | `VERTICAL_GAIN` | f32 | volts per code |
| `160` | `VERTICAL_OFFSET` | f32 | vertical offset, volts |
| `176` | `HORIZ_INTERVAL` | f32 | seconds per sample (`= 1 / SARA`) |
| `180` | `HORIZ_OFFSET` | f64 | time of the first sample, seconds (usually negative) |

`COMM_TYPE = 0` ↔ the 8-bit DAT2 codes; `WAVE_ARRAY_COUNT` ↔ the DAT2 block byte count; `HORIZ_INTERVAL`
↔ the `SARA?` rate. The firmware fills `VERTICAL_GAIN`/`VERTICAL_OFFSET` from the active per-(channel,
V/div) calibration and offset (spec 10 / spec 06) and `HORIZ_INTERVAL`/`WAVE_ARRAY_COUNT` from the
current timebase/record (spec 04).

**Open:** the remaining LeCroy DSO WAVEDESC fields (instrument-name, trigger-time, fixed/variable-gain
exponents, timebase/vertical-coupling enums, etc.) occupy the rest of the 346 bytes but only the offsets
above are pinned; populate the others per the DSO template as a follow-up. A client that needs only
code→volts and the time axis uses the four scaling fields and the two counts above.

### 5.1 Reconstruction

For sample index `n` with raw code `code_n` (unsigned 0–255):

```
volts_n = code_n · VERTICAL_GAIN − VERTICAL_OFFSET
t_n     = HORIZ_OFFSET + n · HORIZ_INTERVAL
```

Both `VERTICAL_GAIN` and `VERTICAL_OFFSET` are in the descriptor, so a DAT2 code array plus its DESC
block fully reconstructs the calibrated waveform. This is the **only** endianness/format contract a host
needs: little-endian descriptor fields, unsigned 8-bit codes, the linear transfer above.

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

**Open:** an RLE-compressed hardcopy variant is not part of this contract; `SCDP` emits the uncompressed
`BI_BITFIELDS` bitmap above.

---

## 7. Mapping onto the firmware

| Host action | Firmware path |
|---|---|
| `set`-style SCPI command | staging setter (spec 09 §2), applied by the bus owner at the frame boundary |
| `?` query of a live setting | lock-guarded snapshot/peek (spec 09 §8) — no bus access |
| `Cn:WF? DAT2` | copy of the most-recently published frame's channel codes (spec 03 arena), sparsed per `WFSU` |
| `Cn:WF? DESC` | WAVEDESC assembled from the active timebase (spec 04) + per-(channel, V/div) cal (spec 10) |
| `SCDP` | serialize the current framebuffer (spec 07) as the §6 BMP |

The SCPI/VXI-11/USB-TMC servers run in their own workers and are **producers/consumers only**: they call
staging setters and read-only snapshots, exactly like the internal line protocol (spec 09 §7). They never
issue a GPMC access (spec 01 single-owner discipline).

---

## 8. Open

- **VXI-11 async/interrupt channels.** The `DEVICE_ASYNC` (abort) program is reachable via the
  `create_link` `abortPort`, but `device_abort` and the `DEVICE_INTR` SRQ/interrupt channel
  (`create_intr_chan`, service-request delivery) are not required for the query/waveform path and their
  service is optional.
- **USB-TMC descriptor detail** (§1.1) beyond the identity fields.
- **Full write-command inventory** (§3.3): the settable-keyword syntax/ranges/defaults and the reply
  formats for queries beyond §3.2.
- **Remaining WAVEDESC fields** (§5): only the ten offsets in the table are pinned.
