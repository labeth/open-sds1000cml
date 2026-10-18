# ENGMODEL-OWNER-UNIT: FU-TOOLS
# Minimal pure-Python stand-in for libsigrokdecode's `sigrokdecode` module,
# enough to run API-3 protocol decoders on sample arrays.
import bisect

OUTPUT_ANN = 0
OUTPUT_PYTHON = 1
OUTPUT_BINARY = 2
OUTPUT_META = 3
SRD_CONF_SAMPLERATE = 10000


class _EOF(Exception):
    pass


class _Chan:
    """One channel: values plus rise/fall index lists for fast condition search."""

    # TRLC-LINKS: REQ-SDS-018
    def __init__(self, vals):
        self.v = vals
        self.rise = []
        self.fall = []
        self.edge = []
        for i in range(1, len(vals)):
            if vals[i] != vals[i - 1]:
                self.edge.append(i)
                (self.rise if vals[i] else self.fall).append(i)

    # TRLC-LINKS: REQ-SDS-018
    def next_in(self, lst, cur):
        k = bisect.bisect_left(lst, cur)
        return lst[k] if k < len(lst) else None

    # TRLC-LINKS: REQ-SDS-018
    def first(self, term, cur, n):
        if cur >= n:
            return None
        if term == 'r':
            return self.next_in(self.rise, cur)
        if term == 'f':
            return self.next_in(self.fall, cur)
        if term == 'e':
            return self.next_in(self.edge, cur)
        if term in ('l', 'h'):
            want = 1 if term == 'h' else 0
            if self.v[cur] == want:
                return cur
            return self.next_in(self.rise if want else self.fall, cur)
        if term == 's':
            # stable: no edge at i
            i = cur
            while i < n and i in self._edgeset():
                i += 1
            return i if i < n else None
        raise ValueError('bad term %r' % term)

    # TRLC-LINKS: REQ-SDS-018
    def _edgeset(self):
        if not hasattr(self, '_es'):
            self._es = set(self.edge)
        return self._es

    # TRLC-LINKS: REQ-SDS-018
    def holds(self, term, i):
        v = self.v
        prev = v[i - 1] if i > 0 else v[0]
        if term == 'l':
            return v[i] == 0
        if term == 'h':
            return v[i] == 1
        if term == 'r':
            return prev == 0 and v[i] == 1
        if term == 'f':
            return prev == 1 and v[i] == 0
        if term == 'e':
            return prev != v[i]
        if term == 's':
            return prev == v[i]
        raise ValueError(term)


class Decoder:
    # TRLC-LINKS: REQ-SDS-018
    def __init__(self):
        pass

    # set up by the session
    # TRLC-LINKS: REQ-SDS-018
    def _setup(self, session, chans, n, samplerate, options):
        self._session = session
        self._chans = chans  # list of _Chan or None (unassigned)
        self._n = n
        self.samplenum = 0
        self._first = True
        self.matched = None
        self.options = {}
        for o in getattr(self, 'options_spec', ()):
            pass
        self._outputs = []
        self._samplerate = samplerate

    # TRLC-LINKS: REQ-SDS-018
    def register(self, output_type, meta=None, proto_id=None):
        self._outputs.append(output_type)
        return output_type

    # TRLC-LINKS: REQ-SDS-018
    def has_channel(self, idx):
        return idx < len(self._chans) and self._chans[idx] is not None

    # TRLC-LINKS: REQ-SDS-018
    def put(self, ss, es, output_id, data):
        self._session._put(self, ss, es, output_id, data)

    # TRLC-LINKS: REQ-SDS-018
    def _pins(self, i):
        return tuple((c.v[i] if c is not None else 0xff) for c in self._chans)

    # TRLC-LINKS: REQ-SDS-018
    def wait(self, conds=None):
        n = self._n
        if conds is None:
            conds = [{'skip': 1}]
        if isinstance(conds, dict):
            conds = [conds]
        if len(conds) == 0:
            conds = [{'skip': 1}]
        base = -1 if self._first else self.samplenum
        start = base + 1
        self._first = False
        for c in conds:
            if c.get('skip', None) == 0 and len(c) == 1:
                cur = max(base, 0)
                self.samplenum = cur
                self.matched = tuple(len(cc) == 1 and cc.get('skip', None) == 0 for cc in conds)
                return self._pins(cur)
        best = None
        for c in conds:
            idx = self._first_match(c, start, base, n)
            if idx is not None and (best is None or idx < best):
                best = idx
        if best is None:
            self.samplenum = n
            raise _EOF()
        self.samplenum = best
        self.matched = tuple(self._holds_all(c, best, base) for c in conds)
        return self._pins(best)

    # TRLC-LINKS: REQ-SDS-018
    def _holds_all(self, c, i, base):
        for k, term in c.items():
            if k == 'skip':
                if i != base + term:
                    return False
            else:
                ch = self._chans[k]
                if ch is None or not ch.holds(term, i):
                    return False
        return True

    # TRLC-LINKS: REQ-SDS-018
    def _first_match(self, c, cur, base, n):
        if len(c) == 0:
            return cur if cur < n else None
        while cur < n:
            m = cur
            for k, term in c.items():
                if k == 'skip':
                    t = base + term
                    f = t if t >= cur else None
                else:
                    ch = self._chans[k]
                    if ch is None:
                        return None
                    f = ch.first(term, cur, n)
                if f is None or f >= n:
                    return None
                if f > m:
                    m = f
            if self._holds_all(c, m, base):
                return m
            cur = m + 1
        return None
