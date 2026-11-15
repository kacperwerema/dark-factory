# Post-mortem

Issue: ui/shelf
Title: Shelf
Status: done
System: UI
Date: 2026-11-01T09:00:00Z
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

- [ ] A1: Note the image size limit
  Differently: Images over two megabytes get rejected, so nobody debugs that twice
  Place: memory pitfall
  Text: Images over two megabytes get rejected.
- [ ] A2: Refuse shipping to done unless the branch head is an ancestor of main
  Differently: a fresh agent would be refused at done when the branch head is not an ancestor of main
  Place: workflow
  Text: Add a rule on shipping to done that refuses while the branch head is not an ancestor of main.
