// ENGMODEL-OWNER-UNIT: FU-OTA-DIAG

package diag

import (
	"fmt"
	"strings"
	"time"

	"github.com/labeth/open-sds1000cml/ota/internal/vxi11"
)

// stateQueries read the factory app's settings: the identity first, then
// every setting the sweep changes, so the answers can be sent back as
// commands to put the scope as it was.
var stateQueries = []string{
	"*IDN?", "*OPT?",
	"C1:VDIV?", "C2:VDIV?", "C1:OFST?", "C2:OFST?", "C1:CPL?", "C2:CPL?", "C1:ATTN?", "C2:ATTN?",
	"BWL?", "C1:TRA?", "C2:TRA?", "TDIV?", "TRDL?", "TRMD?", "TRSE?", "C1:TRLV?", "C2:TRLV?",
	"C1:TRSL?", "C2:TRSL?", "C1:TRCP?", "C2:TRCP?", "ACQW?", "AVGA?", "MSIZ?",
}

// infoQueries are read once for the record and never restored.
var infoQueries = []string{"SARA?", "SAST?", "SANU? C1", "SANU? C2", "CHDR?", "INR?", "ILVD?", "XYDS?"}

// Step is one setting of the active sweep: the command and what it changes.
type Step struct {
	Group string `json:"group"`
	Cmd   string `json:"cmd"`
}

// vdivs are the V/div detents the sweep sets at the probe's 1x and 10x.
var vdivs = []string{"2MV", "5MV", "10MV", "20MV", "50MV", "100MV", "200MV", "500MV", "1V", "2V", "5V", "10V"}

// tdivs are the timebases the sweep sets: each band of the acquisition.
var tdivs = []string{"1NS", "2NS", "5NS", "10NS", "20NS", "50NS", "100NS", "200NS", "500NS", "1US", "10US", "100US", "1MS", "10MS", "100MS", "1S", "10S"}

// SweepSteps is the active sweep: every V/div at both probe factors, the
// couplings, the bandwidth limit, offsets and the channel on and off for each
// channel, then the timebases, the trigger and the acquisition modes. Each
// step's register traffic in the trace is what that setting does on this
// board's population.
// TRLC-LINKS: REQ-SDS-285
func SweepSteps() []Step {
	var s []Step
	add := func(g, f string, args ...any) { s = append(s, Step{g, fmt.Sprintf(f, args...)}) }
	for _, ch := range []string{"C1", "C2"} {
		add("channel", "%s:TRA ON", ch)
		add("channel", "%s:CPL D1M", ch)
		for _, att := range []string{"1", "10"} {
			add("vdiv", "%s:ATTN %s", ch, att)
			for _, v := range vdivs {
				add("vdiv", "%s:VDIV %s", ch, v)
			}
		}
		add("vdiv", "%s:ATTN 1", ch)
		add("vdiv", "%s:VDIV 1V", ch)
		for _, c := range []string{"A1M", "GND", "D1M"} {
			add("coupling", "%s:CPL %s", ch, c)
		}
		for _, o := range []string{"-3V", "-1V", "-0.2V", "0V", "0.2V", "1V", "3V", "0V"} {
			add("offset", "%s:OFST %s", ch, o)
		}
		add("bwl", "BWL %s,ON", ch)
		add("bwl", "BWL %s,OFF", ch)
		add("channel", "%s:TRA OFF", ch)
		add("channel", "%s:TRA ON", ch)
	}
	for _, t := range tdivs {
		add("tdiv", "TDIV %s", t)
	}
	add("tdiv", "TDIV 1US")
	for _, ch := range []string{"C1", "C2"} {
		add("trigger", "TRSE EDGE,SR,%s,HT,OFF", ch)
		for _, l := range []string{"-1V", "0V", "1V"} {
			add("trigger", "%s:TRLV %s", ch, l)
		}
		add("trigger", "%s:TRSL NEG", ch)
		add("trigger", "%s:TRSL POS", ch)
		for _, c := range []string{"AC", "HFREJ", "LFREJ", "DC"} {
			add("trigger", "%s:TRCP %s", ch, c)
		}
	}
	for _, m := range []string{"NORM", "SINGLE", "AUTO"} {
		add("trigger", "TRMD %s", m)
	}
	for _, d := range []string{"-2US", "0S", "2US", "0S"} {
		add("trigger", "TRDL %s", d)
	}
	for _, q := range []string{"PEAK_DETECT", "AVERAGE,16", "SAMPLING"} {
		add("acquire", "ACQW %s", q)
	}
	return s
}

// scpiState is the answers to stateQueries and infoQueries.
type scpiState struct {
	Answers map[string]string `json:"answers"`
	Order   []string          `json:"order"`
}

// dialFactory connects to the factory app's VXI-11 server on this scope,
// waiting up to wait for it to come up after boot.
// TRLC-LINKS: REQ-SDS-284
func dialFactory(wait time.Duration) (*vxi11.Client, error) {
	end := time.Now().Add(wait)
	for {
		c, err := vxi11.Dial("127.0.0.1", 5*time.Second)
		if err == nil {
			return c, nil
		}
		if time.Now().After(end) {
			return nil, err
		}
		time.Sleep(3 * time.Second)
	}
}

// readState asks the factory app every state and info query.
// TRLC-LINKS: REQ-SDS-284
func readState(c *vxi11.Client) scpiState {
	st := scpiState{Answers: map[string]string{}}
	for _, q := range append(append([]string(nil), stateQueries...), infoQueries...) {
		ans, err := c.Query(q)
		if err != nil {
			ans = "[" + err.Error() + "]"
		}
		st.Answers[q] = strings.TrimSpace(ans)
		st.Order = append(st.Order, q)
	}
	return st
}

// restoreCmds are the commands that put back the settings in st: each
// state answer of a setting the sweep changes is a valid command as it stands.
// TRLC-LINKS: REQ-SDS-285
func restoreCmds(st scpiState) []string {
	var out []string
	for _, q := range stateQueries {
		ans := st.Answers[q]
		if strings.HasPrefix(q, "*") || ans == "" || strings.HasPrefix(ans, "[") {
			continue
		}
		out = append(out, ans)
	}
	return out
}
