# eth100tx golden-model vectors

Emitted by TestEmitVectors. Each <case> is arp|icmp. These pin the RTL PHY
decoder stage-by-stage; the RTL must reproduce every file bit-for-bit on the
matching input.

RX pipeline order (each stage consumes the previous file):

  <case>.samples        600 MSa/s ternary amplitude codes, signed decimal,
                        one sample/line. AmpPos=+1000, 0, AmpNeg=-1000.
                        4.8 samples/symbol (floor pattern 4,5,5,5,5). This is
                        the DECODER INPUT. Slice at +/-500.
  <case>.ternary        same stream sliced to +1/0/-1 (human aid).
  <case>.symbols        CDR output: one MLT-3 ternary level (-1/0/+1) per
                        125 Mbaud symbol. len(samples) collapses to len here.
  <case>.scrambled_bits MLT-3 decode (level change=1, hold=0): 125 Mbit NRZ
                        scrambled bits, 1/line, MSB-first within code groups.
  <case>.keystream      LFSR x^11+x^9+1 keystream (k[n]=k[n-9]^k[n-11]),
                        aligned to scrambled/plain bits. RX recovers this by
                        idle-lock; provided for the descrambler stage check.
  <case>.plain_bits     descrambled NRZ bits = scrambled XOR keystream.
  <case>.code_groups    5-bit groups aligned on /J/K/: "<bbbbb> <label>" per
                        line. Labels: I,J,K,T,R = control; 0..F = data nibble.
  <case>.mii_nibbles    decoded data nibbles (hex), low-nibble-first per octet,
                        including preamble (0x55 x7) + SFD (0xD5) + frame + FCS.
  <case>.frame          final MAC frame octets + FCS octets + FCS_VALUE.

Stage boundary invariants the RTL must satisfy (all verified in Go tests):
  * RX .symbols  == TX .symbols  (CDR is exact on the clean stream)
  * RX .scrambled_bits == TX .scrambled_bits
  * RX .plain_bits[lock:] == TX .plain_bits[lock:]  (descrambler idle-lock)
  * .code_groups start with I* then J K ... T R then I*
  * FCS verifies via CRC-32/ISO-HDLC residue 0x2144DF1C over frame||FCS.
