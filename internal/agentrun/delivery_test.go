package agentrun

import (
	"strings"
	"testing"
	"time"
)

// claudePane is a Claude Code screen: the conversation, the input box
// holding box, and the footer.
func claudePane(conversation, box string) string {
	return conversation + "\n" +
		"────────────────────────────────────────\n" +
		"❯ " + box + "\n" +
		"────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle)\n"
}

func TestPromptPlace(t *testing.T) {
	const marker = "Approved for in progress"
	for _, tc := range []struct {
		name, pane  string
		inBox, sent bool
	}{
		{"typed, not sent", claudePane("⏺ Waiting for approval.", "Approved for in progress: go on"), true, false},
		{"collapsed paste in the box", claudePane("⏺ Waiting.", "[Pasted text #1 +3 lines]"), true, false},
		{"sent", claudePane("> Approved for in progress: go on\n\n⏺ Thanks.", ""), false, true},
		{"sent before, typed again", claudePane("> Approved for in progress", "Approved for in progress"), true, false},
		{"old boxed input", "> Approved for in progress\n╭──────╮\n│ > Approved for in progress │\n╰──────╯\n", true, false},
		{"not there yet", claudePane("⏺ Waiting.", ""), false, false},
		{"no input box", "  Approved for in progress\n● working\n", false, true},
	} {
		inBox, sent := PromptPlace(tc.pane, marker)
		if inBox != tc.inBox || sent != tc.sent {
			t.Errorf("%s: inBox=%v sent=%v, want %v %v", tc.name, inBox, sent, tc.inBox, tc.sent)
		}
	}
}

// stubPane makes CapturePane answer from pane and records the keys pressed.
func stubPane(t *testing.T, pane func(enters int) string) *int {
	t.Helper()
	origCap, origKey, origTimeout, origPoll := CapturePane, SendKey, PromptDeliveryTimeout, promptDeliveryPoll
	t.Cleanup(func() {
		CapturePane, SendKey, PromptDeliveryTimeout, promptDeliveryPoll = origCap, origKey, origTimeout, origPoll
	})
	// A few polls of a stubbed pane fit many times over in the timeout,
	// however slowly the machine wakes the poller.
	PromptDeliveryTimeout, promptDeliveryPoll = 3*time.Second, 10*time.Millisecond
	enters := 0
	CapturePane = func(string) (string, error) { return pane(enters), nil }
	SendKey = func(_, key string) error {
		if key == "Enter" {
			enters++
		}
		return nil
	}
	return &enters
}

// The bug: Claude Code took the first Enter as part of the paste, so the
// text sat in the input box and the check, finding its last line in the
// pane, called it delivered. Now it presses Enter again and waits for the
// conversation.
func TestPromptDeliveryStep_PressesEnterAgainWhileTextSitsInBox(t *testing.T) {
	const text = "Approved for in progress: go on"
	enters := stubPane(t, func(enters int) string {
		if enters == 0 {
			return claudePane("⏺ Waiting for approval.", text)
		}
		return claudePane("> "+text+"\n\n⏺ Continuing.", "")
	})

	var steps []Step
	PromptDeliveryStep(&steps, "t", text, "")
	if *enters != 1 || len(steps) != 1 || steps[0].Name != "Prompt delivered" || !strings.Contains(steps[0].Detail, "Enter pressed again once") {
		t.Fatalf("enters=%d steps=%+v", *enters, steps)
	}
}

func TestPromptDeliveryStep_StuckInBoxIsReported(t *testing.T) {
	const text = "Approved for in progress: go on"
	enters := stubPane(t, func(int) string { return claudePane("⏺ Waiting.", text) })

	var steps []Step
	PromptDeliveryStep(&steps, "t", text, "issue-cli dispatch x --reprompt")
	if *enters != promptResubmits || len(steps) != 1 || steps[0].Status != "error" ||
		!strings.Contains(steps[0].Detail, "still typed in the input box") || !strings.Contains(steps[0].Detail, "issue-cli dispatch x --reprompt") {
		t.Fatalf("enters=%d steps=%+v", *enters, steps)
	}
}

// Nudge on a private tmux server: a pane that echoes the text gets it, one
// that shows nothing reports it undelivered.
func TestNudgeOnPrivateTmux(t *testing.T) {
	isolateTmux(t)
	orig := PromptDeliveryTimeout
	t.Cleanup(func() { PromptDeliveryTimeout = orig })
	tmuxRun(t, "new-session", "-d", "-s", "echo", "cat")
	tmuxRun(t, "new-session", "-d", "-s", "mute", "/bin/sh", "-c", "stty -echo; cat >/dev/null")

	// Delivery returns as soon as the text shows, so its wait can be long;
	// the mute pane waits it out, so that one stays short.
	PromptDeliveryTimeout = 15 * time.Second
	if err := Nudge("echo", "Approved for in progress: go on"); err != nil {
		t.Fatalf("echo: %v", err)
	}
	PromptDeliveryTimeout = 2 * time.Second
	err := Nudge("mute", "Approved for in progress: go on")
	if err == nil || !strings.Contains(err.Error(), "not delivered to mute") {
		t.Fatalf("mute: %v", err)
	}
	if err := Nudge("gone", "x"); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("gone: %v", err)
	}
}
