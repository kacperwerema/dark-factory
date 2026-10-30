#!/usr/bin/env bash
# issue: factory/ceo-chat-whole-messages-from-the-transcript-question-cards-answered-questions-drop-off
# The CEO chat read from the lead's transcript, on a throwaway project: this branch's viewer on a free port,
# a stand-in lead that is a real Claude Code session (Haiku) in a private tmux, so its transcript and its
# AskUserQuestion widget are the real ones. Never the real projects.yaml, issues tree or tmux server.
#
# Boxes: TP2 (sandbox) when every step passes; TP3 (manual checks as a script) via summary --all.
# shellcheck source=/dev/null disable=SC2034,SC2154
source "${CHECKS_LIB:?run me with: issue-cli check-script <slug>}"

checks_intro "The CEO chat now reads the lead's Claude Code transcript: one bubble per reply, question cards" \
	"the page can answer, answered questions drop off. These steps start a throwaway project, a Haiku lead in a" \
	"private tmux and this branch's viewer, ask a question, answer it from the page. About 3 minutes."

cd "$CHECKS_ROOT" || exit 2
checks_default_box TP2
bin=
sandbox_dir bin
sandbox_project sandbox
lead=lead-$SANDBOX_PROJECT
# lj: a field of lead.json, through python (no jq on every Mac).
lj() { curl -fsS "$base/p/$SANDBOX_PROJECT/lead.json" | python3 -I -c "import json,sys; m=json.load(sys.stdin); print($1)"; }

# ── S1 ────────────────────────────────────────────────────────────────
step S1 "This branch's viewer serves the sandbox's CEO chat" \
	--what "builds this branch's viewer, starts it on the sandbox project on a free port, with a private tmux." \
	--why "everything after talks to this viewer; nothing reaches your real viewer or tmux." \
	--look "the CEO page answers and says the CEO is not running yet." \
	--expect "status 200 and the Start the CEO button."
private_tmux
free_port port
base=http://127.0.0.1:$port
run go build -o "$bin/issue-viewer" .
expect '[ $code = 0 ]' "the branch builds"
background viewer_pid "$bin/issue-viewer" -config "$ISSUE_VIEWER_CONFIG" -port "$port"
eventually 'curl -fsS "$base/p/$SANDBOX_PROJECT/lead" | grep -q "lead_chat.spawn"' "the CEO page answers, CEO not running"

# ── S2 ────────────────────────────────────────────────────────────────
if step S2 "A real lead's reply is read from its transcript, as whole markdown" \
	--what "starts claude -n $lead (Haiku) in the private tmux, in the sandbox project; it replies with a list and asks two questions." \
	--why "only a real Claude Code session writes the real transcript and draws the real question widget." \
	--look "the page: the reply is one bubble with a rendered list; the question shows as a card, not as ☐/Submit text." \
	--expect "lead.json lists one open question of kind ask, read from the widget, and the conversation from the transcript." \
	--touches-real "a Claude Code session (Haiku, a few cents) and its transcript under ~/.claude/projects/<the sandbox folder>"; then
	ptmux new-session -d -s "$lead" -x 140 -y 50 -c "$SANDBOX_PROJECT_DIR" \
		"claude -n $lead --model claude-haiku-5-5 'Reply with a short markdown list of three fruits, each in bold. Then call the AskUserQuestion tool exactly once with two questions. Q1 header Color, question: Which color do you prefer?, single-select options Red (warm) and Blue (cool). Q2 header Pets, question: Which pets do you like?, multiSelect true, options Cats, Dogs, Fish. Do nothing else.'"
	# A new folder asks for trust first; its default is No.
	if eventually 'ptmux capture-pane -p -t "$lead" | grep -q "trust this folder"' "the trust prompt" 20; then
		ptmux send-keys -t "$lead" Down
		sleep 0.3
		ptmux send-keys -t "$lead" Enter
	fi
	eventually '[ "$(lj "[q[\"kind\"] for q in m[\"open_questions\"]]")" = "['"'"'ask'"'"']" ]' "one open question of kind ask" 90
	eventually 'curl -fsS "$base/p/$SANDBOX_PROJECT/lead" | grep -q "data-testid=\"lead_chat.ask_card\""' "the question card is on the page"
	eventually '[ "$(lj "m[\"source\"]")" = transcript ]' "the conversation comes from the transcript" 30
	run curl -fsS "$base/p/$SANDBOX_PROJECT/lead"
	expect '! printf "%s" "$out" | grep -q "☐"' "no widget text in the conversation"
	echo "Open $base/p/$SANDBOX_PROJECT/lead"
	ask "Is the fruits reply one bubble with a bold list, and is there a question card above the message box?"
fi

# ── S3 ────────────────────────────────────────────────────────────────
step S3 "Answering from the page answers the real widget" \
	--what "posts Blue to …/lead/answer, then Cats + Fish + Other 'Birds', then Submit answers, as the card's buttons do." \
	--why "the page presses the widget's keys; the transcript must record exactly those answers." \
	--look "the question leaves open_questions, and its turn shows it answered with Blue (cool) / Cats, Fish, Birds." \
	--expect "each post says sent; then no open question, and the transcript has the answers."
ptmux has-session -t "$lead" 2> /dev/null || skip "no stand-in lead: S2 was skipped"
answer() { curl -fsS -X POST -H 'Content-Type: application/json' -d "$1" "$base/p/$SANDBOX_PROJECT/lead/answer"; }
run answer '{"question":"Which color do you prefer?","picks":[2]}'
expect 'printf "%s" "$out" | grep -q "\"status\":\"sent\""' "Blue was pressed"
eventually '[ "$(lj "(m.get(\"ask\") or {}).get(\"question\")")" = "Which pets do you like?" ]' "the widget moved to the second question"
run answer '{"question":"Which pets do you like?","picks":[1,3],"other":"Birds"}'
expect 'printf "%s" "$out" | grep -q "\"status\":\"sent\""' "Cats, Fish and Birds were pressed"
eventually '[ "$(lj "(m.get(\"ask\") or {}).get(\"review\")")" = True ]' "the widget shows the review tab"
run curl -fsS -X POST -H 'Content-Type: application/json' -d '{"key":"1"}' "$base/p/$SANDBOX_PROJECT/lead/key"
eventually '[ "$(lj "len([q for q in m[\"open_questions\"] if q[\"kind\"]==\"ask\"])")" = 0 ]' "the question left open_questions" 30
eventually '[ "$(lj "[e[\"answer\"] for t in m[\"turns\"] for a in (t.get(\"asks\") or []) for e in a[\"questions\"]]")" = "['"'"'Blue (cool)'"'"', '"'"'Cats, Fish, Birds'"'"']" ]' "the transcript has Blue (cool) and Cats, Fish, Birds" 30
ask "On the page, is the question now a grey card marked answered with these answers, and is it gone from CEO's questions on the right?"

# ── S4 ────────────────────────────────────────────────────────────────
step S4 "You answer a question by clicking the card" \
	--what "asks the lead to ask once more; you pick on the card and press Submit." \
	--why "the card's own buttons, Other field and Submit are what you will use." \
	--look "after Submit the card leaves the box above the composer within a few seconds." \
	--expect "the lead's next reply names what you picked."
ptmux has-session -t "$lead" 2> /dev/null || skip "no stand-in lead: S2 was skipped"
[ -n "${CHECKS_YES:-}" ] && skip "needs you to click the card"
if step_active; then
	ptmux send-keys -t "$lead" -l "Call AskUserQuestion once more: one question, header Food, question: What should we eat?, options Pizza and Soup. Then say what I picked."
	sleep 0.3
	ptmux send-keys -t "$lead" Enter
	eventually '[ "$(lj "len([q for q in m[\"open_questions\"] if q[\"kind\"]==\"ask\"])")" = 1 ]' "a new open question" 90
	echo "On $base/p/$SANDBOX_PROJECT/lead: type your own answer in Other (e.g. Ramen) and press Submit."
	eventually '[ "$(lj "len([q for q in m[\"open_questions\"] if q[\"kind\"]==\"ask\"])")" = 0 ]' "you answered it from the page" 180
fi
ask "Did the card go away, and does the lead's reply name your answer?"

summary --all TP3
