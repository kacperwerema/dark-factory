package trace

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Stage is one status summed over spans: every visit to it.
type Stage struct {
	Status string `json:"status"`
	Visits int    `json:"visits"`
	// Tokens sums the visits whose tokens are known; TokensKnown counts them.
	Tokens      int64   `json:"tokens"`
	TokensKnown int     `json:"tokens_known"`
	WallSec     float64 `json:"wall_sec"`
	CLICalls    int     `json:"cli_calls"`
	// Gates are the visits that left with a known try count; FirstTry the
	// ones that passed first time.
	Gates    int `json:"gates"`
	FirstTry int `json:"first_try"`
	Back     int `json:"back"`
	// BriefingBytes sums the visits' first briefings.
	BriefingBytes int `json:"briefing_bytes"`
}

// ByStage sums spans by status, in the order each status first appears.
func ByStage(spans []Span) []Stage {
	var out []Stage
	at := map[string]int{}
	for _, s := range spans {
		i, ok := at[s.Status]
		if !ok {
			i = len(out)
			at[s.Status] = i
			out = append(out, Stage{Status: s.Status})
		}
		st := &out[i]
		st.Visits++
		st.WallSec += s.WallSec
		st.CLICalls += s.CLICalls
		if s.Tokens.Known {
			st.Tokens += s.Tokens.Total()
			st.TokensKnown++
		}
		if s.Gate != nil && s.Gate.FirstTry != nil {
			st.Gates++
			if *s.Gate.FirstTry {
				st.FirstTry++
			}
		}
		if s.Back {
			st.Back++
		}
		if s.Briefing != nil {
			st.BriefingBytes += s.Briefing.Bytes
		}
	}
	return out
}

// Unknown is how a value nobody recorded is shown.
const Unknown = "—"

// TokensText is t as "in / out / cached", Unknown when not known.
func TokensText(t Tokens) string {
	if !t.Known {
		return Unknown
	}
	return tokens.Format(t.In) + " / " + tokens.Format(t.Out) + " / " + tokens.Format(t.CacheRead+t.CacheWrite)
}

// BriefingText is b's size, "12.3 KB · 3.1k tok", Unknown for none.
func BriefingText(b *tracker.Briefing) string {
	if b == nil {
		return Unknown
	}
	return fmt.Sprintf("%.1f KB · %s tok", float64(b.Bytes)/1024, tokens.Format(int64(b.Tokens)))
}

// GateText is the gate's result: "✓ first try", "✗ 3 tries", "open".
func GateText(s Span) string {
	switch {
	case s.Gate == nil:
		return "open"
	case s.Gate.FirstTry == nil:
		return "→ " + s.Gate.To + " " + Unknown
	case *s.Gate.FirstTry:
		return "→ " + s.Gate.To + " ✓ first try"
	default:
		return fmt.Sprintf("→ %s ✗ %d tries", s.Gate.To, s.Gate.Tries)
	}
}

// CallsText is the visit's calls, "12 (2 refused)", Unknown without a clilog.
func CallsText(s Span) string {
	if !s.CallsKnown {
		return Unknown
	}
	if s.CLIRefused > 0 {
		return fmt.Sprintf("%d (%d refused)", s.CLICalls, s.CLIRefused)
	}
	return fmt.Sprint(s.CLICalls)
}

// BackText is "back", "came back", both or "".
func BackText(s Span) string {
	var parts []string
	if s.EnteredBack {
		parts = append(parts, "came back")
	}
	if s.Back {
		parts = append(parts, "went back")
	}
	return strings.Join(parts, ", ")
}

// AgentText is the visit's agents and models.
func AgentText(s Span) string {
	a := strings.Join(s.Agents, "+")
	if len(s.Models) > 0 {
		a += " (" + strings.Join(s.Models, ", ") + ")"
	}
	if a == "" {
		return Unknown
	}
	return a
}

// Wall is seconds as "1h05m", "12m", "40s".
func Wall(sec float64) string {
	switch {
	case sec >= 3600:
		return fmt.Sprintf("%dh%02dm", int(sec)/3600, int(sec)%3600/60)
	case sec >= 60:
		return fmt.Sprintf("%dm", int(sec)/60)
	default:
		return fmt.Sprintf("%ds", int(sec))
	}
}

// Table writes spans as a table by stage, one row a visit.
func Table(w io.Writer, spans []Span) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tAGENT\tBRIEFING\tTOKENS in/out/cached\tWALL\tCALLS\tGATE\tBACK")
	for _, s := range spans {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Status, AgentText(s), BriefingText(s.Briefing),
			TokensText(s.Tokens), Wall(s.WallSec), CallsText(s), GateText(s), BackText(s))
	}
	tw.Flush()
}
