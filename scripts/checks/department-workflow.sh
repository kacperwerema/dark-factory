#!/usr/bin/env bash
# issue: factory/department-workflow-files
# Four steps run this branch's issue-cli and issue-viewer, built into a sandbox_dir, on a throwaway project:
# Product on the development template, a Marketing department (system SEO) on the bundled content template.
# Its own projects.yaml, issues and viewer on a free port; never the real projects.yaml, issues tree or tmux.
#
# Boxes: TP2 (sandbox) passes when every step does; TP3 (manual checks as a script) likewise, via summary --all.
# TP4 is the CLI walk, TP5 the broken file, TP6 the viewer (the owner's eye).
# shellcheck source=/dev/null disable=SC2034,SC2154
source "${CHECKS_LIB:?run me with: issue-cli check-script <slug>}"

checks_intro "A department in projects.yaml can name its own workflow file. These steps build the branch, make a" \
	"throwaway project with a Marketing department on the content template, walk a Marketing issue to the" \
	"publish gate, break the file, and open the viewer on the sandbox. About 4 minutes; it is all removed after."

cd "$CHECKS_ROOT" || exit 2
sandbox_dir tmp
proj=$tmp/proj
issues=$proj/issues
bin=$tmp/bin
cfg=$tmp/projects.yaml
export GIT_CONFIG_GLOBAL=/dev/null TMPDIR=$tmp XDG_STATE_HOME=$tmp/state ISSUE_VIEWER_CONFIG=$cfg
# ic: the branch's issue-cli on the sandbox project, from its folder, in a clean environment (no agent
# session of the caller's, so the sandbox's issues are nobody's worker's)
ic() { (cd "$proj" && clean_shell "$bin/issue-cli" --config "$cfg" --project sandbox "$@"); }

sandbox() {
	mkdir -p "$bin" "$issues/CLI" "$issues/SEO" || return 1
	go build -o "$bin/issue-cli" ./cmd/issue-cli && go build -o "$bin/issue-viewer" . || return 1
	cat > "$cfg" << YAML
projects:
  - name: "Sandbox"
    slug: "sandbox"
    issues: "$issues"
    workflow: "$proj/workflow.yaml"
    workdir: "$proj"
    terminal: "none"
    departments:
      - name: "Marketing"
        role: "SEO pages and posts for the client roster"
        systems: ["SEO"]
        workflow: "workflows/marketing.yaml"
YAML
	(cd "$proj" && "$bin/issue-cli" workflow init --template development < /dev/null > /dev/null) || return 1
	ic workflow init --template content --out "$proj/workflows/marketing.yaml" || return 1
	ic create --title "Faq page for the plumber" --system SEO --status brief > /dev/null || return 1
	ic create --title "Export button" --system CLI --status idea > /dev/null || return 1
	ic update seo/faq-page-for-the-plumber --body "An FAQ page for the plumbing client's site." > /dev/null || return 1
	ic update cli/export-button --body "A button that exports the board." > /dev/null || return 1
	echo "sandbox at $tmp"
}

# ── TP4 ───────────────────────────────────────────────────────────────
step S1 "A Marketing issue walks its own workflow; Product's doesn't change" --box TP4 --box TP2 \
	--what "builds the branch, makes the sandbox, then walks seo/faq-page-for-the-plumber brief → draft → owner review and tries publish; cli/export-button goes idea → in design on workflow.yaml." \
	--why "each department runs on its own workflow file (epic D-1); nothing goes out before your stamp." \
	--look "show's Department: and Workflow file: lines, and the refusal at publish." \
	--expect "Department: Marketing, workflows/marketing.yaml; publish refused for a missing approval; the CLI issue still on idea → in design."
run sandbox
expect '[ $code = 0 ] && [ -x "$bin/issue-cli" ]' "the branch built and the sandbox exists"
run ic show seo/faq-page-for-the-plumber
expect 'grep -q "Department: Marketing" <<< "$out" && grep -q "workflows/marketing.yaml" <<< "$out"' "show names Marketing and its file"
run ic transition seo/faq-page-for-the-plumber --to draft
expect '[ $code = 0 ]' "brief → draft on the content workflow"
run ic check seo/faq-page-for-the-plumber --section Draft --all
run ic transition seo/faq-page-for-the-plumber --to "owner review"
expect '[ $code = 0 ]' "draft → owner review"
run ic transition seo/faq-page-for-the-plumber --to publish
expect '[ $code != 0 ] && grep -qi "approv" <<< "$out"' "publish is refused until the owner approves it"
run ic transition seo/faq-page-for-the-plumber --to backlog
expect '[ $code != 0 ]' "Product's backlog is not a Marketing status"
run ic transition cli/export-button --to "in design"
expect '[ $code = 0 ]' "the Product issue still walks workflow.yaml"
run ic set-meta cli/export-button --key department --value Marketing
expect '[ $code != 0 ] && grep -q -- "--status" <<< "$out"' "moving it to Marketing is refused and names --status"

# ── TP5 ───────────────────────────────────────────────────────────────
step S2 "A broken department file stops only its own issues" --box TP5 --box TP2 \
	--what "breaks workflows/marketing.yaml, tries a Marketing and a Product command, runs health lint --check department, then puts the file back." \
	--why "a broken file must never fall back to Product's gates, and must say which file to fix." \
	--look "the error text and the lint line." \
	--expect "the Marketing command fails naming department Marketing and marketing.yaml; the Product one works; lint reports it."
run cp "$proj/workflows/marketing.yaml" "$tmp/marketing.good"
printf 'statuses: [: broken\n' > "$proj/workflows/marketing.yaml"
run ic transition seo/faq-page-for-the-plumber --to draft
expect '[ $code != 0 ] && grep -q "department Marketing" <<< "$out" && grep -q "marketing.yaml" <<< "$out"' "the Marketing command names the department and the file"
run ic show cli/export-button
expect '[ $code = 0 ] && grep -q "Department: Product" <<< "$out"' "Product's issue is not affected"
run ic health lint --check department
expect 'grep -q "department Marketing" <<< "$out"' "lint reports the broken file"
run cp "$tmp/marketing.good" "$proj/workflows/marketing.yaml"

# ── TP6 ───────────────────────────────────────────────────────────────
step S3 "The viewer draws Marketing's own line and switches the Board" --box TP6 --box TP2 \
	--what "starts this branch's viewer on the sandbox, on a free port, and gives you two pages." \
	--why "a department room shows its own stations, and the Board shows one workflow's columns at a time." \
	--look "the company page's Marketing room, then the Board's Workflow chips." \
	--expect "Marketing's room has a line brief · draft · owner review · gate · publish · done; Board: Product shows idea…done, Marketing shows brief…done."
free_port port
background viewer_pid "$bin/issue-viewer" -config "$cfg" -port "$port"
eventually 'curl -fsS "http://127.0.0.1:$port/p/sandbox/company" > /dev/null' "the viewer answers"
page=$(curl -fsS "http://127.0.0.1:$port/p/sandbox/company")
expect 'grep -q "company.room.dept.line" <<< "$page" && grep -q "owner review" <<< "$page"' "the company page has Marketing's line"
page=$(curl -fsS "http://127.0.0.1:$port/p/sandbox/workboard?all=1&department=Marketing")
expect 'grep -q "workboard.column.owner_review" <<< "$page"' "the Board on Marketing has its columns"
page=$(curl -fsS "http://127.0.0.1:$port/p/sandbox/workboard?all=1")
expect 'grep -q "workboard.department.marketing" <<< "$page" && ! grep -q "workboard.column.owner_review" <<< "$page"' "Product's Board has the switch and none of Marketing's columns"
echo "Open: http://127.0.0.1:$port/p/sandbox/company  and  http://127.0.0.1:$port/p/sandbox/workboard?all=1"
ask "Does Marketing's room show its own line, and does the Board's Workflow switch show Product's columns, then Marketing's?"

summary --all TP3
