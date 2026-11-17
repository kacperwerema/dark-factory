#!/usr/bin/env python3
"""Print the first-turn context (input + cache read + cache write tokens of the
first assistant message) of Claude Code sessions started in folders whose path
contains MATCH, from the transcripts under ~/.claude/projects (or
$CLAUDE_CONFIG_DIR/projects). Read-only; starts nothing.

  scripts/first-turn-tokens.py [--days N] [--after ISO] [MATCH]

--after keeps sessions whose first message is at or after the ISO time (a
build's install), so the before and after figures can be told apart.
"""
import glob, json, os, statistics, sys, time

def main(argv):
    days, after, match = 7, "", ""
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--days":
            days = float(argv[i + 1]); i += 2; continue
        if a == "--after":
            after = argv[i + 1]; i += 2; continue
        match = a; i += 1
    base = os.environ.get("CLAUDE_CONFIG_DIR") or os.path.expanduser("~/.claude")
    key = match.replace("/", "-").replace(".", "-")
    rows = []
    for f in glob.glob(os.path.join(base, "projects", "*", "*.jsonl")):
        if key and key not in os.path.basename(os.path.dirname(f)):
            continue
        if os.path.getmtime(f) < time.time() - days * 86400:
            continue
        with open(f) as fh:
            for line in fh:
                try:
                    m = json.loads(line)
                except ValueError:
                    continue
                u = m.get("message", {}).get("usage") if m.get("type") == "assistant" else None
                if not u:
                    continue
                if after and m.get("timestamp", "") < after:
                    break
                n = u.get("input_tokens", 0) + u.get("cache_read_input_tokens", 0) + u.get("cache_creation_input_tokens", 0)
                rows.append((n, m.get("timestamp", ""), os.path.basename(os.path.dirname(f))))
                break
    if not rows:
        print("no sessions matched")
        return 1
    v = sorted(r[0] for r in rows)
    print(f"sessions {len(v)} · first turn median {int(statistics.median(v))} · min {v[0]} · max {v[-1]} tokens")
    for n, ts, d in sorted(rows, key=lambda r: r[1])[-5:]:
        print(f"  {ts[:19]}  {n:>7}  {d[-70:]}")
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
