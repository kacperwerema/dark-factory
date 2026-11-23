# shellcheck shell=bash
# lib.sh's part (lib.sh sources it, issue-cli inlines it): summary, and
# ticking and commenting on the real issue.

# ── Summary ──────────────────────────────────────────────────────────
summary() {
	_ck_close
	local all_boxes=() then='' b
	while [ $# -gt 0 ]; do
		case "$1" in
		--all)
			for b in ${2//,/ }; do all_boxes+=("$b"); done
			shift
			;;
		--then) then=${2:-} && shift ;;
		esac
		shift
	done

	local id failed=() eyes=() skipped=() passed_boxes=() lines=()
	declare -A box_ok=() box_seen=()
	local box_order=()
	echo
	echo "${_ck_b}Summary${_ck_0}"
	for id in "${_ck_order[@]}"; do
		local r=${_ck_result[$id]:-fail} n=${_ck_note[$id]:-}
		case "$r" in
		pass) echo "  ${_ck_g}✓ $id pass${_ck_0}" ;;
		fail) echo "  ${_ck_r}✗ $id fail${_ck_0}: $n" && failed+=("$id") ;;
		skip) echo "  ${_ck_y}- $id skipped${_ck_0}: $n" && skipped+=("$id") ;;
		eyes) echo "  ${_ck_y}? $id needs your eyes${_ck_0} (machine checks passed)" && eyes+=("$id") ;;
		esac
		lines+=("step $id $r boxes=${_ck_boxes[$id]// /,}${n:+ note=$n}")
		for b in ${_ck_boxes[$id]}; do
			if [ -z "${box_seen[$b]:-}" ]; then
				box_seen[$b]=1
				box_order+=("$b")
				box_ok[$b]=1
			fi
			[ "$r" = pass ] || box_ok[$b]=0
		done
	done
	for b in "${box_order[@]}"; do
		[ "${box_ok[$b]}" = 1 ] && passed_boxes+=("$b")
	done
	if [ ${#all_boxes[@]} -gt 0 ] && [ ${#_ck_order[@]} -gt 0 ] && [ ${#failed[@]} = 0 ] && [ ${#eyes[@]} = 0 ] && [ ${#skipped[@]} = 0 ]; then
		for b in "${all_boxes[@]}"; do
			[ -n "${box_ok[$b]:-}" ] || passed_boxes+=("$b")
		done
	fi
	echo "  Boxes passed: ${passed_boxes[*]:-none}"

	local dir="$CHECKS_ROOT/.agent-logs/checks/$CHECKS_NAME" stamp
	stamp=$(date -u +%Y%m%dT%H%M%SZ)
	if mkdir -p "$dir" 2> /dev/null; then
		{
			echo "script: $CHECKS_NAME"
			echo "issue: ${CHECKS_SLUG:-}"
			echo "mode: $CHECKS_MODE"
			echo "lib: $CHECKS_LIB_VERSION"
			printf '%s\n' "${lines[@]}"
			echo "boxes passed: ${passed_boxes[*]:-}"
		} > "$dir/$stamp-$CHECKS_MODE.txt"
	fi

	if [ "$CHECKS_MODE" = machine ]; then
		# The boxes of steps that need the owner's eyes or were skipped: the
		# machine run ticks nothing, and these are the owner's to check.
		local owner_boxes=() r
		for b in "${box_order[@]}"; do
			for id in "${_ck_order[@]}"; do
				r=${_ck_result[$id]:-fail}
				if [[ " ${_ck_boxes[$id]} " == *" $b "* ]] && { [ "$r" = eyes ] || [ "$r" = skip ]; }; then
					owner_boxes+=("$b")
					break
				fi
			done
		done
		echo "Machine run: ${#failed[@]} failed, ${#eyes[@]} need the owner's eyes, ${#skipped[@]} skipped. Left to the owner: ${owner_boxes[*]:-none from this script}."
		[ ${#failed[@]} = 0 ]
		return
	fi

	if [ -n "${CHECKS_SLUG:-}" ]; then
		local answer
		if [ ${#passed_boxes[@]} -gt 0 ]; then
			read -r -p "  Tick the passed boxes (${passed_boxes[*]}) on ${CHECKS_SLUG}? [y/n] " answer
			[[ $answer == [yY]* ]] && _ck_tick "${passed_boxes[@]}"
		fi
		if [ ${#failed[@]} -gt 0 ]; then
			local text="checks: $CHECKS_NAME:" sep=
			for id in "${failed[@]}"; do
				text="$text$sep $id failed: ${_ck_note[$id]}"
				sep=";"
			done
			read -r -p "  Comment the failures on ${CHECKS_SLUG} for the agent? [y/n] " answer
			[[ $answer == [yY]* ]] && _ck_comment "$text"
		fi
	fi
	if [ ${#failed[@]} = 0 ] && [ ${#skipped[@]} = 0 ]; then
		echo "${then:-All steps passed.}"
	elif [ ${#failed[@]} -gt 0 ]; then
		echo "Tell the agent which step failed and what you saw."
	fi
	[ ${#failed[@]} = 0 ]
}

_ck_project_args() {
	[ -n "${CHECKS_PROJECT:-}" ] && printf '%s\n' --project "$CHECKS_PROJECT"
}

_ck_tick() {
	local proj=()
	mapfile -t proj < <(_ck_project_args)
	_ck_real "$CHECKS_ISSUE_CLI" "${proj[@]}" check "$CHECKS_SLUG" "$@"
}

_ck_comment() {
	local proj=()
	mapfile -t proj < <(_ck_project_args)
	_ck_real "$CHECKS_ISSUE_CLI" "${proj[@]}" comment "$CHECKS_SLUG" "$1"
}
