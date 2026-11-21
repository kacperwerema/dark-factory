---
title: Reel kit
order: 47
---

# Reel kit

The reel kit makes short vertical videos (reels, 9:16, for Instagram Reels and TikTok) from a **storyboard JSON**: an agent fills the storyboard, the kit fills the project's **Remotion template** with it, encodes the MP4 with **FFmpeg**, runs **QA** on it, and puts the reel with its QA report on the **shelf** for the owner's one review. The command is `issue-cli reel`; the code is `internal/reel`; the starter template is `cmd/issue-cli/templates/reel/`.

The kit **reads files only**. The narration audio and its word timings come from the project's TTS connector, AI clips from its video-generation connector, screen recordings from the playtest runner; the storyboard names the files they wrote. So a render spends no money and needs no key. Connectors, their spend caps and the voices per placement are their own feature; the department workflow that runs the steps (brief, script with a legal check, render, the owner's review, publish) is the video department's reel type.

## Where things live

| What | Where | Whose |
|---|---|---|
| Storyboard format, plan, render, QA | `internal/reel` (`issue-cli reel`) | Dark Factory: every project gets the same kit |
| Starter template | `cmd/issue-cli/templates/reel/`, copied by `issue-cli reel init` | Dark Factory's code; the project's copy is the project's to change |
| Remotion, React, Chrome | npm dependencies of the project's copy (`npm install` there) | Remotion's licence, see below |
| Storyboards, media, rendered reels | the project's repo (e.g. `reels/<name>/`) | the project |

**Remotion's licence.** Remotion is never vendored here: it is an npm dependency of the copied template, installed by the project. Remotion is free for individuals and for companies of up to 3 people; a larger company needs a Remotion company licence (https://www.remotion.dev/license). The template's README says so too. The template's own code (`src/`, `reel.json`) is Dark Factory's, under this repo's licence.

A project that wants the kit itself changed (a new check, a new shape role, another field) files the request through its CEO to Product, as every engine request does ([departments.md](departments.md#settled-rules)).

## Commands

| Command | What it does |
|---|---|
| `issue-cli reel init [<dir>]` | copies the starter template into `<dir>` (default `reels/template`); refuses when any of its files is there already, so it never overwrites the project's copy; then `npm install` there. The copy has a `.gitignore` for `node_modules/`, `out/`, `.env` and `.env.*` (`.env.example` stays trackable): a key for the app goes in Keychain ([Secrets](departments.md#secrets)) |
| `issue-cli reel schema` | prints the storyboard's JSON Schema (draft 2020-12), for editors |
| `issue-cli reel validate <storyboard.json> [--render]` | every problem at its JSON path, exit 1 on any; `--render` also wants each narration's audio and word timings |
| `issue-cli reel render <storyboard.json> [--template <dir>] [--out <file.mp4>]` | the plan, the template's render, the FFmpeg encode; `mem-guard check` first (Remotion starts Chrome), refused when blocked |
| `issue-cli reel qa <file.mp4> --storyboard <storyboard.json> [--template <dir>] [--plan <file>] [--narration <file>]` | the six checks below, one line each, `<file.mp4>.qa.json`; exit 1 on a fail |
| `issue-cli reel shelve <file.mp4> --storyboard <storyboard.json> --title "..." [--issue <slug>] [--department <name>] [--template <dir>]` | QA again; a red reel is refused; a green one becomes one `video` output in review, the QA summary its note, the report beside it |

Each command prints the next one. A typical run, from the project's root:

```sh
issue-cli reel init                                   # once: reels/template, then (cd reels/template && npm install)
issue-cli reel validate reels/week-plan/storyboard.json
# the TTS connector writes each scene's audio and timings; the storyboard names them
issue-cli reel validate --render reels/week-plan/storyboard.json
issue-cli reel render reels/week-plan/storyboard.json
issue-cli reel qa reels/week-plan/storyboard.mp4 --storyboard reels/week-plan/storyboard.json
issue-cli reel shelve reels/week-plan/storyboard.mp4 --storyboard reels/week-plan/storyboard.json --title "Reel: a week's plan in three taps"
```

### Voice and music from ElevenLabs

The Video department's `elevenlabs` connector ([Spend caps](departments.md#spend-caps)) makes the narration and the music bed; the kit only reads the files it writes.

```sh
issue-cli connector check video/elevenlabs            # free: the plan and the credits used and left this period
issue-cli connector exec video/elevenlabs -- voices search --language pl --gender female --use-case narrative_story --commercial
#   PLV1 · Ania · standard · narrative_story · female young
#     preview https://…/ania.mp3                       free: listen, then the owner adds the voice_id under voices:
issue-cli connector exec video/elevenlabs -- tts --placement narrator --text-file reels/week-plan/s1.txt --out reels/week-plan/s1.mp3 --alignment reels/week-plan/s1.json
issue-cli connector exec video/elevenlabs -- music --prompt "warm acoustic guitar, 100 bpm, calm" --length-ms 30000 --instrumental --out reels/week-plan/music.mp3
```

`music` writes the MP3 and `music.licence.json` beside it, and prints the storyboard's `music` block (`"path": "music.mp3", "licence": {"source": "ElevenLabs Music", "ref": "song <id>, <tier> plan (paid), commercial use, <date>"}`) and the two legal lines the reel's legal check reads (`music: music.mp3`, `music_licence: ElevenLabs Music, …`): paste them. It is refused on a free plan (the Music API is for paid plans, and free-plan music isn't cleared for commercial use). Music is billed in credits, about 900 a minute, from the same monthly cap as the narration.

## The storyboard

One JSON file per reel, `version: 1`. Paths in it are relative to its folder. `issue-cli reel schema` prints the whole format; `reel validate` is the full check (each shape's roles, every file there, the licence and AI fields).

| Field | What |
|---|---|
| `shape` | `hook-list`, `before-after` or `app-demo` (below) |
| `title`, `language` | the reel's working title; BCP 47 language (`pl`) |
| `brand` | `name`, `colors {primary, accent, text, background}`, `font`, `logo` (an image) |
| `scenes[]` | `id`, `role` (the shape's), `duration` (planned seconds), `text` (on screen), `narration`, `clips[]` |
| `scenes[].narration` | `text`, `voice` (a placement name the TTS connector's config resolves, e.g. `narrator`), `audio` (the file it wrote), `alignment` (its word timings) |
| `scenes[].clips[]` | `source` (`app-recording`, `owner`, `stock`, `ai`), `path`, `start`/`end` (trim), `ai_generated`, `prompt`, `tool`, `licence` |
| `captions` | `style` (`words`: 3 words a cue; `lines`: 8), `position` (`lower`, `middle`, `upper`, `custom` with `box {x,y,w,h}`), `max_words`, `font_size` |
| `music` | `path`, `licence {source, ref}` (licensed music only), `volume_db` (default -18), `duck` (lower it under the narration) |
| `cta` | `text`, `url`, `handle`: what the `cta` scene shows |
| `qa` | QA bound overrides for this reel ([QA](#qa)) |

Rules `validate` holds a storyboard to: every `ai` clip says `ai_generated: true` and names its `tool` (the AI-content label and the legal check read them); every `stock` clip names its `licence`; music names its licence's source and ref; an `app-demo` step shows an `app-recording` clip; unknown fields are refused, so a typo is not a silent default.

**Word timings** (`alignment`): ElevenLabs' with-timestamps response as it comes (its `alignment`, or `normalized_alignment`, or the alignment object alone: `characters`, `character_start_times_seconds`, `character_end_times_seconds`), or the kit's own `[{"word", "start", "end"}]` (also `{"words": [...]}`), seconds into the audio file. For a recorded human voice, any tool that writes word timings in one of these forms will do.

### The shapes

| Shape | Composition | Scenes |
|---|---|---|
| `hook-list` | `HookList` | hook (1), item (2 to 6), cta (1): "3 things you…", each item numbered over its clip |
| `before-after` | `BeforeAfter` | hook, before, after, cta: the before muted, the after wiping in |
| `app-demo` | `AppDemo` | hook (1), step (1 to 6, each with an `app-recording` clip), cta (1): the recording in a phone frame |

The template's `examples/` has one text-only storyboard per shape to start from.

## Render

`reel render` works out the **plan**: each scene runs its planned `duration`, or its narration plus 0.4 s when that is longer; every word is placed on the reel's clock; the words are grouped into caption **cues**, each with its box on the 1080x1920 frame. It writes the plan beside the reel as `<name>.plan.json`, copies the clips and logo into the template's `public/reel-<name>/`, and runs the template's `render` command from its `reel.json` with the composition for the shape. Then FFmpeg:

1. the narration stem, each scene's audio at its scene's start (`<name>.narration.wav`, kept: QA's sync check reads it);
2. the music under it, looped to length, ducked under the narration when `duck`;
3. loudness: `loudnorm` to -14 LUFS, -1 dBTP, measured first and applied with the measured values (two passes);
4. the encode: H.264 High, yuv420p, 1080x1920, 30 fps, AAC 48 kHz 192 kb/s, `+faststart`.

The template only draws frames (`--muted`); every sound is the kit's.

## QA

`reel qa` (and `reel shelve`, which runs it again) checks the MP4 with ffprobe and ffmpeg's filters. Each line names what it measured and the bound; any fail exits 1.

| Check | Fails when | Default bound |
|---|---|---|
| `duration` | outside the bounds, or off the storyboard's plan | 5-90 s, within 0.5 s of the plan |
| `aspect` | the display aspect is not 9:16 (width × sample aspect / height) | 0.5625 |
| `loudness` | integrated loudness (`ebur128`) off the target; silence fails | -14 LUFS ±2 |
| `black-frames` | a black stretch (`blackdetect`, pixel threshold 0.10) | none of 0.2 s or more |
| `safe-zone` | a caption box enters what the platforms' UI covers; a narrated reel with no cues | top 220, bottom 420, left 60, right 140 px of 1080x1920 |
| `caption-sync` | a cue more than 0.15 s from its words; speech (`silencedetect` on the narration stem, else the reel's own audio) starting more than 0.25 s from a scene's first word; audio and video streams starting more than 0.15 s apart | as listed |

The safe zone is the union of Instagram Reels' and TikTok's overlays (the top bar; caption, music and buttons at the bottom; the like/comment/share column on the right). The caption checks read the cues in the plan the template drew from (`<name>.plan.json`), against the words worked out from the storyboard now: a narration re-recorded after the render shows as a sync fail until the reel is rendered again. A check with no data fails and says so; none passes silently.

**Bounds** are laid in order: the kit's defaults, then the template's `reel.json` `qa:` (every reel the project makes), then the storyboard's `qa:` (this reel). The fields: `min_seconds`, `max_seconds`, `plan_slack_seconds`, `lufs`, `lufs_slack`, `black_seconds`, `cue_slack_seconds`, `speech_slack_seconds`, `safe_zone {top, bottom, left, right}`. The report names each bound that didn't come from the defaults, and where it came from.

The report is `<file.mp4>.qa.json` (every check, measured value, bound and detail). The owner judges how the reel looks; QA only catches what a machine can.

## The shelf

`reel shelve` refuses a reel that fails QA: the owner sees only reels that pass. A passing reel becomes one `video` output in review on the issue's department's shelf (`output add --kind video --file … --report …`): the note is the verdict with the duration and loudness, and the report (`<file.mp4>.qa.md`) is kept beside the video, shown under the player in the shelf's reader. That is the reel's one owner review; publishing it stays the owner's (`owner_only`), as for every output ([Nothing goes out without the owner's stamp](departments.md#settled-rules)). A reel made again after the owner's comments goes back with `issue-cli output revise <id> --file <new.mp4>` (which drops the old report: run `reel qa` on the new file first) or a new `reel shelve`.

## Changing the template

The copy `reel init` made is the project's. What the kit expects of it, in its `README.md`:

- **`reel.json`**: `render` is the command that renders one composition, run in the template's folder, with the placeholders `{composition}`, `{out}`, `{props}`, `{duration}`, `{frames}`, `{fps}`, `{width}`, `{height}`; `compositions` maps each shape to its composition id; `public` is the folder `staticFile()` reads; `qa` changes the bounds for every reel made with it.
- **Props** are the plan; `src/types.ts` mirrors `internal/reel`'s `Plan`.
- **Captions** are drawn from the plan's `cues`, each in its `box` and only between its `start` and `end`, since those are what QA checks. To move captions, change `captions.position` or `captions.box` in the storyboard, not the drawing code.
- **Sound** stays the kit's: render with `--muted`.

A new look for a shape is a change to its file in `src/shapes/`. A new shape of the project's own is a new composition in `src/Root.tsx` mapped in `reel.json`; the kit's shape names and their roles (above) are what `validate` checks, so a shape with other roles is a request to the engine.

## Tests

The kit's tests never call a TTS, a video API or Remotion. `internal/reel` tests the storyboard rules, the word timings, the plan, the parsers and every QA verdict on fixed input (these run everywhere), and, with ffmpeg on PATH, renders a fixture reel through a stand-in renderer (`ffmpeg -f lavfi color`, written with `standin.Write`) and runs QA on it and on reels made wrong one way each (`TestRenderWritesAQAPassingReel`, `TestQAFailsBadReels`); they skip without ffmpeg, as on the public CI. `cmd/issue-cli`'s `TestReel*` walk `reel init`, `validate`, the mem-guard refusal and `shelve` end to end. A real Remotion render of the starter template is a manual check: `mem-guard check` first.
