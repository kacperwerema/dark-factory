# Post-mortem

Issue: ui/cart
Title: Cart
Status: done
System: UI
Date: 2026-11-03T09:00:00Z
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

- [ ] A1: Remember the coupon rounding
  Differently: Coupons round down per line, so nobody debugs that twice
  Place: memory pitfall
  Text: Coupons round down per line.
- [ ] A2: Try a rule on shipping to done that refuses while the branch head is not on main
  Differently: a fresh agent could not close an issue while its branch head is not an ancestor of main
  Place: workflow
  Text: Add a rule on shipping to done that refuses while the branch head is not an ancestor of main.
