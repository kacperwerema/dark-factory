---
title: "Retries never charge twice"
---

# Retries never charge twice

A customer taps Pay, the train goes into a tunnel, and the app times out.
They tap Pay again. Until now that could mean two charges and a support
ticket. From 1.0, every payment carries an idempotency key: the second tap
gets the first tap's receipt, and the card is charged once.
