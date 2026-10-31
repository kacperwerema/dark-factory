package share

import (
	"sort"
	"time"
)

// Summary is what the owner's view shows from a collector's batches.
type Summary struct {
	Batches  int
	Installs int // distinct installs in the window
	First    string
	Last     string
	// PerDay is distinct installs per day, oldest first, every day in the
	// window (zero days included).
	PerDay []DayCount
	// Versions, Commands, Features, Templates, OS: installs or calls per
	// name, largest first.
	Versions  []NameCount // installs whose latest batch in the window ran it
	Commands  []NameCount // calls
	Features  []NameCount // installs with it on
	Templates []NameCount // installs using it
	Platforms []NameCount // installs per os/arch
	// Errors is per command, largest error rate first among commands with
	// at least one failure.
	Errors []ErrorRate
	// Calls and Fails over all commands.
	Calls, Fails int
}

// DayCount is a day's distinct installs.
type DayCount struct {
	Day      string
	Installs int
}

// NameCount is a name and its count.
type NameCount struct {
	Name  string
	Count int
}

// ErrorRate is one command's failures.
type ErrorRate struct {
	Command  string
	Calls    int
	Fails    int
	Pct      float64
	TopClass string
}

// Summarize aggregates the batches of the last days days before now
// (days <= 0: all).
func Summarize(batches []Batch, now time.Time, days int) Summary {
	var from string
	if days > 0 {
		from = now.UTC().AddDate(0, 0, -days).Format("2006-01-02")
	}
	var s Summary
	perDay := map[string]map[string]bool{}
	installs := map[string]bool{}
	latest := map[string]Batch{} // install → its newest batch
	calls := map[string]int{}
	type ec struct {
		calls, fails int
		classes      map[string]int
	}
	errs := map[string]*ec{}
	for _, b := range batches {
		if from != "" && b.Day < from {
			continue
		}
		s.Batches++
		if s.First == "" || b.Day < s.First {
			s.First = b.Day
		}
		if b.Day > s.Last {
			s.Last = b.Day
		}
		if perDay[b.Day] == nil {
			perDay[b.Day] = map[string]bool{}
		}
		perDay[b.Day][b.Install] = true
		installs[b.Install] = true
		if l, ok := latest[b.Install]; !ok || b.Day >= l.Day {
			latest[b.Install] = b
		}
		for _, c := range b.Commands {
			k := c.Key()
			calls[k] += c.Calls
			s.Calls += c.Calls
			s.Fails += c.Fails
			e := errs[k]
			if e == nil {
				e = &ec{classes: map[string]int{}}
				errs[k] = e
			}
			e.calls += c.Calls
			e.fails += c.Fails
			for cl, n := range c.Errors {
				e.classes[cl] += n
			}
		}
	}
	s.Installs = len(installs)
	if s.First != "" {
		start, _ := time.Parse("2006-01-02", s.First)
		end, _ := time.Parse("2006-01-02", s.Last)
		if from != "" {
			start, _ = time.Parse("2006-01-02", from)
		}
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			k := d.Format("2006-01-02")
			s.PerDay = append(s.PerDay, DayCount{Day: k, Installs: len(perDay[k])})
		}
	}
	vers, feats, tmpls, plats := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	for _, b := range latest {
		vers[b.Ver]++
		plats[b.OS+"/"+b.Arch]++
		for _, f := range b.Features {
			feats[f]++
		}
		for _, t := range b.Templates {
			tmpls[t]++
		}
	}
	s.Versions = ranked(vers)
	s.Features = ranked(feats)
	s.Templates = ranked(tmpls)
	s.Platforms = ranked(plats)
	s.Commands = ranked(calls)
	for k, e := range errs {
		if e.fails == 0 || e.calls == 0 {
			continue
		}
		top := ""
		for cl, n := range e.classes {
			if top == "" || n > e.classes[top] || (n == e.classes[top] && cl < top) {
				top = cl
			}
		}
		s.Errors = append(s.Errors, ErrorRate{Command: k, Calls: e.calls, Fails: e.fails,
			Pct: 100 * float64(e.fails) / float64(e.calls), TopClass: top})
	}
	sort.Slice(s.Errors, func(i, j int) bool {
		if s.Errors[i].Pct != s.Errors[j].Pct {
			return s.Errors[i].Pct > s.Errors[j].Pct
		}
		return s.Errors[i].Command < s.Errors[j].Command
	})
	return s
}

func ranked(m map[string]int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for k, v := range m {
		out = append(out, NameCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}
