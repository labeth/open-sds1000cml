# ENGMODEL-OWNER-UNIT: FU-TOOLS
# Runs sigrok protocol decoders (libsigrokdecode sources) on CSV logic data
# with the fake sigrokdecode module. Usable as a sigrok-cli stand-in:
#   srdrun.py -i wave.csv -I csv:column_formats=2l:samplerate=N -P pd:opt=v:ch=COL,pd2 -A pd2=cls
#             --protocol-decoder-samplenum
import os
import sys
import importlib

HERE = os.path.dirname(os.path.abspath(__file__))
DEC = os.environ.get('SRD_DECODERS', os.path.join(HERE, '..', 'lsd', 'decoders'))
sys.path.insert(0, HERE)
sys.path.insert(0, DEC)
import sigrokdecode as srd  # noqa: E402


class Session:
    # TRLC-LINKS: REQ-SDS-018
    def __init__(self, cols, samplerate):
        self.cols = cols  # name -> list of 0/1
        self.sr = samplerate
        self.stack = []
        self.anns = []  # (inst, ss, es, clsidx, texts)

    # TRLC-LINKS: REQ-SDS-018
    def add(self, pdid, opts):
        mod = importlib.import_module(pdid)
        D = mod.Decoder
        inst = D()
        chdefs = list(getattr(D, 'channels', ())) + list(getattr(D, 'optional_channels', ()))
        chans = []
        n = 0
        for cd in chdefs:
            col = opts.pop(cd['id'], None)
            if col is not None:
                ch = srd._Chan(self.cols[col])
                n = len(self.cols[col])
                chans.append(ch)
            else:
                chans.append(None)
        if n == 0 and self.cols:
            n = len(next(iter(self.cols.values())))
        srd.Decoder._setup(inst, self, chans, n, self.sr, opts)
        options = {}
        for o in getattr(D, 'options', ()):
            options[o['id']] = o['default']
        for k, v in opts.items():
            if k not in options:
                raise SystemExit('unknown option %s for %s' % (k, pdid))
            d = options[k]
            if isinstance(d, bool):
                v = v in ('1', 'true', 'yes')
            elif isinstance(d, int):
                v = int(v)
            elif isinstance(d, float):
                v = float(v)
            options[k] = v
        inst.options = options
        inst._pdid = pdid
        inst._inst = pdid + '-1'
        self.stack.append(inst)
        return inst

    # TRLC-LINKS: REQ-SDS-018
    def _put(self, inst, ss, es, out, data):
        if out == srd.OUTPUT_ANN:
            self.anns.append((inst, ss, es, data[0], data[1]))
        elif out == srd.OUTPUT_PYTHON:
            k = self.stack.index(inst)
            if k + 1 < len(self.stack):
                self.stack[k + 1].decode(ss, es, data)

    # TRLC-LINKS: REQ-SDS-018
    def run(self):
        for inst in self.stack:
            if hasattr(inst, 'reset'):
                pass
            if hasattr(inst, 'metadata'):
                try:
                    inst.metadata(srd.SRD_CONF_SAMPLERATE, self.sr)
                except Exception:
                    pass
            inst.start()
        try:
            self.stack[0].decode()
        except srd._EOF:
            pass
        for inst in self.stack:
            if hasattr(inst, 'end'):
                try:
                    inst.end()
                except srd._EOF:
                    pass


# TRLC-LINKS: REQ-SDS-018
def parse_spec(spec):
    out = []
    for part in spec.split(','):
        bits = part.split(':')
        opts = {}
        for kv in bits[1:]:
            k, v = kv.split('=', 1)
            opts[k] = v
        out.append((bits[0], opts))
    return out


# TRLC-LINKS: REQ-SDS-018
def read_csv(path):
    with open(path) as f:
        lines = f.read().split('\n')
    names = lines[0].strip().split(',')
    cols = {nm: [] for nm in names}
    lists = [cols[nm] for nm in names]
    for ln in lines[1:]:
        ln = ln.strip()
        if not ln:
            continue
        for k, c in enumerate(ln.split(',')):
            lists[k].append(1 if c == '1' else 0)
    return cols


# TRLC-LINKS: REQ-SDS-018
def ann_filter(inst, clsidx, want):
    # want: dict pdid -> set of names (empty = all)
    if inst._pdid not in want:
        return False
    names = want[inst._pdid]
    if not names:
        return True
    D = type(inst)
    anns = getattr(D, 'annotations', ())
    clsnames = [a[0] for a in anns]
    for nm in names:
        if nm in clsnames:
            if clsnames.index(nm) == clsidx:
                return True
            continue
        for row in getattr(D, 'annotation_rows', ()):
            if row[0] == nm and clsidx in row[2]:
                return True
    return False


# TRLC-LINKS: REQ-SDS-018
def main(argv):
    if '-L' in argv:
        print('Supported protocol decoders:')
        for d in sorted(os.listdir(DEC)):
            if os.path.exists(os.path.join(DEC, d, 'pd.py')):
                print('  %-20s %s' % (d, d))
        return 0
    inp = fmt = pd = A = None
    samplenum = False
    i = 1
    while i < len(argv):
        a = argv[i]
        if a == '-i':
            inp = argv[i + 1]; i += 2
        elif a == '-I':
            fmt = argv[i + 1]; i += 2
        elif a == '-P':
            pd = argv[i + 1]; i += 2
        elif a == '-A':
            A = argv[i + 1]; i += 2
        elif a == '--protocol-decoder-samplenum':
            samplenum = True; i += 1
        else:
            raise SystemExit('unsupported arg ' + a)
    sr = 0
    for kv in fmt.split(':')[1:]:
        k, v = kv.split('=', 1)
        if k == 'samplerate':
            sr = int(v)
    cols = read_csv(inp)
    s = Session(cols, sr)
    for pdid, opts in parse_spec(pd):
        s.add(pdid, opts)
    s.run()
    want = {}
    if A:
        for part in A.split(','):
            if '=' in part:
                p, cl = part.split('=', 1)
                want[p] = set(cl.split(':'))
            else:
                want[part] = set()
    else:
        want[s.stack[-1]._pdid] = set()
    out = []
    for inst, ss, es, cls, texts in s.anns:
        if ann_filter(inst, cls, want):
            pre = '%d-%d ' % (ss, es) if samplenum else ''
            out.append('%s%s: %s' % (pre, inst._inst, texts[0]))
    sys.stdout.write('\n'.join(out) + ('\n' if out else ''))
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
