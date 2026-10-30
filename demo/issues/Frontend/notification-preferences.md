---
title: "Notification preferences page"
status: "in design"
system: "Frontend"
version: "1.1"
priority: "medium"
parent: "backend/real-time-notifications"
assignee: "agent-notification-preferences"
created: "2026-10-14"
---

Per-user choices: which events, in-app or push, and quiet hours.

## Design

One settings page with a row per event type (mention, status change, payment failed) and two switches, in-app and push. Quiet hours are a start and end time in the user's zone; push waits until they end, in-app does not.

- [x] Event types listed with their defaults
- [x] Quiet hours: stored per user, applied by the push worker
- [x] Empty state for users with push turned off on the device
