#!/bin/sh
# Stand-in for make playtest: writes one judge run of play-due for $1,
# TP1 passed and TP2 failed, as the overlay would.
sleep 1
d=".playtest/runs/play-due/$(date -u +%Y%m%d-%H%M%S)-judge"
mkdir -p "$d"
cat > "$d/results.yaml" <<YAML
session: play-due
issue: $1
mode: judge
started: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
steps:
  - id: TP1
    title: The band reads well
    status: pass
  - id: TP2
    title: The phone layout holds
    judge:
      status: fail
YAML
echo "Judged play-due: TP1 pass, TP2 fail."
