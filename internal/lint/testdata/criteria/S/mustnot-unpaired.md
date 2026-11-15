---
title: "mustnot unpaired"
status: backlog
created: "2026-11-02"
---

## Acceptance Criteria

- [ ] `make validate` passes
- [ ] TestOwnerGate refuses a stranger

## Must not

- Serve an owner route to a stranger
- Open a route without the identity gate → AC9
- Log the owner token → none: covered by the identity gate
- Weaken the owner gate → AC1, AC2
- Drop the Host check → none:
- nothing beyond the scope above
