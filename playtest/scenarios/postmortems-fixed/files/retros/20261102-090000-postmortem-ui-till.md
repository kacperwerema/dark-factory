# Post-mortem

Issue: ui/till
Title: Till
Status: done
System: UI
Date: 2026-11-02T09:00:00Z
Kind: postmortem

## Steps
Built it.

## Worked
The tests.

## Did not work
none

## Decisions
none

## Known
none

## Proposed actions

- [ ] A1: Record the receipt printer stub
  Differently: Printer stub lives under testdata, so nobody debugs that twice
  Place: memory pitfall
  Text: Printer stub lives under testdata.
  Fixed: ui/cart
- [ ] A2: Gate shipping to done on main holding the branch
  Differently: a fresh agent would not reach done while its branch is not an ancestor of main
  Place: workflow
  Text: Add a rule on shipping to done that refuses while the branch head is not an ancestor of main.
