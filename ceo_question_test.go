package main

import (
	"os"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// testdata/ceo-widget holds AskUserQuestion widgets captured from a real
// Claude Code 2.1.314 lead (capture-pane -p -J), from the owner's last
// prompt down: the first question (single choice), the second (several
// choices) fresh, ticked, with Other typed, and the review tab.
func widgetPane(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/ceo-widget/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return cleanPane("⏺ Here are three fruits.\n\n" + string(b))
}

func TestParsePaneAsk(t *testing.T) {
	cp := parseCEOPane(widgetPane(t, "q1-single"))
	a := cp.Ask
	if a == nil {
		t.Fatal("no widget read")
	}
	if a.Question != "Which color do you prefer?" || a.Multi || a.Cursor != 1 || len(a.Tabs) != 2 || a.Tabs[0].Label != "Color" || a.Tabs[0].Done {
		t.Errorf("q1 = %+v", a)
	}
	if len(a.Options) != 2 || a.Options[1].Label != "Blue (cool)" || a.Options[1].Description != "A cool color" || a.Other.Key != 3 || a.Other.Label != "" {
		t.Errorf("q1 options = %+v other = %+v", a.Options, a.Other)
	}
	if cp.Prompt != nil {
		t.Error("the widget was also read as a permission menu")
	}
	for _, turn := range cp.Turns {
		for _, p := range turn.Text {
			if strings.Contains(p, "☐") || strings.Contains(p, "Chat about this") || strings.Contains(p, "Red (warm)") {
				t.Errorf("widget text in the conversation: %q", p)
			}
		}
	}

	a = parseCEOPane(widgetPane(t, "q2-ticked")).Ask
	if a == nil || a.Question != "Which pets do you like?" || !a.Multi || !a.Tabs[0].Done || len(a.Options) != 3 {
		t.Fatalf("q2 = %+v", a)
	}
	if !a.Options[0].Checked || a.Options[1].Checked || !a.Options[2].Checked || a.Options[0].Description != "Cats as pets" || a.Other.Checked {
		t.Errorf("q2 ticks = %+v other = %+v", a.Options, a.Other)
	}

	a = parseCEOPane(widgetPane(t, "q2-other")).Ask
	if a == nil || a.Other.Label != "Birds" || !a.Other.Checked || a.Cursor != 4 {
		t.Errorf("q2 with Other = %+v", a)
	}

	a = parseCEOPane(widgetPane(t, "review")).Ask
	if a == nil || !a.Review || len(a.Answers) != 2 || a.Answers[1].Answer != "Cats, Fish, Birds" || a.Answers[0].Question != "Which color do you prefer?" {
		t.Errorf("review = %+v", a)
	}

	if parseCEOPane(leadChatPaneText).Ask != nil {
		t.Error("a pane without the widget has no question")
	}
}

func TestPaneAnswerSteps(t *testing.T) {
	steps := func(fixture string, picks []int, other string) string {
		a := parseCEOPane(widgetPane(t, fixture)).Ask
		st, why := paneAnswerSteps(a, picks, other)
		if why != "" {
			return "refused: " + why
		}
		var out []string
		for _, s := range st {
			if s.Text != "" {
				out = append(out, "text:"+s.Text)
			} else {
				out = append(out, s.Key)
			}
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		fixture string
		picks   []int
		other   string
		want    string
	}{
		// A single choice is its digit; Other its digit, the text, Enter.
		{"q1-single", []int{2}, "", "2"},
		{"q1-single", nil, "Green", "3 text:Green Enter"},
		{"q1-single", []int{1, 2}, "", "refused: pick one answer"},
		{"q1-single", []int{1}, "Green", "refused: pick one answer"},
		{"q1-single", []int{3}, "", "refused: there is no option 3"},
		// Several choices: digits for what changes, Down to Submit, Enter.
		{"q2-multi", []int{1, 3}, "", "1 3 Down Down Down Down Enter"},
		{"q2-ticked", []int{1, 3}, "", "Down Down Down Down Enter"},
		{"q2-ticked", []int{2}, "", "1 2 3 Down Down Down Down Enter"},
		// Other: Down to its row and the text (typing ticks it).
		{"q2-multi", []int{1}, "Birds", "1 Down Down Down text:Birds Down Enter"},
		{"q2-other", []int{1, 3}, "Birds", "Down Enter"},
		{"q2-other", []int{1, 3}, "", "4 Down Enter"},
		{"q2-other", []int{1}, "Hamsters", "refused: Other already has text on the CEO's screen: change it in the Terminal view"},
		{"q2-multi", nil, "", "refused: pick at least one answer"},
		{"q2-multi", nil, strings.Repeat("x", 501), "refused: the Other answer is over 500 characters"},
	} {
		if got := steps(c.fixture, c.picks, c.other); got != c.want {
			t.Errorf("%s %v %q: steps = %q, want %q", c.fixture, c.picks, c.other, got, c.want)
		}
	}
}

func TestLeadAnswerEndpoint(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	url := ts.URL + "/p/agents/lead/answer"
	body := `{"question":"Which pets do you like?","picks":[1,3],"other":"Birds"}`

	// The pane doesn't show the widget: nothing is pressed.
	code, out := leadChatPost(t, url, "application/json", body)
	if code != 409 || out["status"] != "moved-on" || len(st.keys) != 0 {
		t.Errorf("without the widget: %d %v keys=%v", code, out, st.keys)
	}
	// Another question on screen: refused.
	st.pane = widgetPane(t, "q1-single")
	if code, out = leadChatPost(t, url, "application/json", body); code != 409 || out["status"] != "moved-on" {
		t.Errorf("another question: %d %v", code, out)
	}
	st.pane = widgetPane(t, "q2-multi")
	if code, out = leadChatPost(t, url, "application/json", `{"question":"Which pets do you like?","picks":[9]}`); code != 400 {
		t.Errorf("bad pick: %d %v", code, out)
	}
	if len(st.keys) != 0 {
		t.Fatalf("refused answers pressed keys: %v", st.keys)
	}
	if code, out = leadChatPost(t, url, "application/json", body); code != 200 || out["status"] != "sent" {
		t.Fatalf("answer: %d %v", code, out)
	}
	target := st.asked[len(st.asked)-1]
	want := []string{"1", "3", "Down", "Down", "Down", "text:Birds", "Down", "Enter"}
	for i := range want {
		want[i] = target + " " + want[i]
	}
	if strings.Join(st.keys, "|") != strings.Join(want, "|") {
		t.Errorf("keys = %q, want %q", st.keys, want)
	}
	// The review tab is answered with its own keys, not here.
	st.pane = widgetPane(t, "review")
	if code, _ = leadChatPost(t, url, "application/json", body); code != 409 {
		t.Errorf("review tab: %d", code)
	}
}

func TestCEOChatQuestionCard(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	st.pane = widgetPane(t, "q2-ticked")
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/lead")
	for _, want := range []string{
		`data-ask-url="/p/agents/lead/answer" data-ask-question="Which pets do you like?" data-multi="1"`,
		`<span class="wb-chip wb-chip--idle">✓ Color</span>`,
		`aria-pressed="true" data-ask-pick="1" data-testid="lead_chat.ask_card.opt.1"`,
		`aria-pressed="false" data-ask-pick="2"`,
		"<small>Dogs as pets</small>",
		`data-testid="lead_chat.ask_card.other"`,
		`data-testid="lead_chat.question.ask"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, `data-testid="lead_chat.prompt"`) {
		t.Error("the widget also showed as a permission menu")
	}

	st.pane = widgetPane(t, "review")
	_, html = getBody(t, ts.URL+"/p/agents/lead")
	for _, want := range []string{`data-testid="lead_chat.ask_card.review"`, "→ Cats, Fish, Birds", `data-key="1" data-testid="lead_chat.ask_card.send"`} {
		if !strings.Contains(html, want) {
			t.Errorf("review card lacks %q", want)
		}
	}
}
