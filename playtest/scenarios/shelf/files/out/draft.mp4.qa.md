## Reel QA: pass

draft.mp4, 2026-11-21 12:00

| Check | Result | Measured | Bound |
|---|---|---|---|
| duration | pass | 12.40 s (plan 12.40 s) | 5-90 s, within 0.5 s of the plan |
| aspect | pass | 1080x1920, SAR 1:1, 0.5625 | 9:16 (0.5625) |
| loudness | pass | -14.1 LUFS integrated | -14 LUFS ±2 |
| black-frames | pass | none | no black stretch of 0.2 s or more |
| safe-zone | pass | 0 of 6 caption boxes outside the safe zone | inside top 220, bottom 420, left 60, right 140 px of 1080x1920 |
| caption-sync | pass | cues off by 0.00 s at most, speech by 0.04 s, audio/video +0.00 s | cues within 0.15 s of their words; speech within 0.25 s of the first word; audio and video start together |

Bounds not from the kit's defaults: none.
