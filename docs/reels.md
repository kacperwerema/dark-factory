---
title: Reel kit
order: 47
---

# Reel kit

The reel kit makes short videos (reels, 9:16, for Instagram Reels and TikTok; or a 4:5 or 1:1 feed post, see [Formats](#formats)) from a **storyboard JSON**: an agent fills the storyboard, the kit fills the project's **Remotion template** with it, encodes the MP4 with **FFmpeg**, runs **QA** on it, and puts the reel with its QA report on the **shelf** for the owner's one review. The command is `issue-cli reel`; the code is `internal/reel`; the starter template is `cmd/issue-cli/templates/reel/`.

The kit **reads files only**. The narration audio and its word timings come from the project's TTS connector, AI clips from its video-generation connector, screen recordings from the playtest runner; the storyboard names the files they wrote. So a render spends no money and needs no key. Connectors, their spend caps and the voices per placement are their own feature; the department workflow that runs the steps (brief, script with a legal check, render, the owner's review, publish) is the video department's reel type.

## Where things live

| What | Where | Whose |
|---|---|---|
| Storyboard format, plan, render, QA | `internal/reel` (`issue-cli reel`) | Dark Factory: every project gets the same kit |
| Starter template | `cmd/issue-cli/templates/reel/`, copied by `issue-cli reel init` | Dark Factory's code; the project's copy is the project's to change |
| Remotion, React, Chrome | npm dependencies of the project's copy (`npm install` there) | Remotion's licence, see below |
| Storyboards, media, rendered reels | the project's repo (e.g. `reels/<name>/`) | the project |
| Each voice's spelling of the brand name | `reels/pronunciation.md` in the project | the project |

**Source media.** The reel's brief names where its media come from (app recordings, the project's own clips, photos), and the worker copies each onto the work branch, beside the storyboard, before the storyboard names it. A gitignored `media/` folder, another worktree or an unmerged branch is not a source: the render, the QA and the next agent read only what the branch has, so a reel built from them can't be made again.

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

**Brand pronunciation.** A TTS voice can misread a made-up brand or product name (ElevenLabs v3 did, on a project's first reel), and each voice misreads it its own way. Listen to the names in the first take of each voice before making the rest. Keep the spelling each voice says right in the project's reel config, `reels/pronunciation.md`, one line per voice placement:

```
narrator: <Brand> → <the spelling it says right>, checked on reels/week-plan/s1.mp3
```

Read it before every take. The spelling goes only into the TTS text (`--text-file`); the script, the captions and the legal check keep the real name. A new voice or a new model gets its own line, checked again.

## The storyboard

One JSON file per reel, `version: 1`. Paths in it are relative to its folder. `issue-cli reel schema` prints the whole format; `reel validate` is the full check (each shape's roles, every file there, the licence and AI fields).

| Field | What |
|---|---|
| `shape` | `hook-list`, `before-after` or `app-demo` (below) |
| `format` | the frame: `9:16` (default), `4:5` or `1:1` ([Formats](#formats)) |
| `title`, `language` | the reel's working title; the voice's BCP 47 language (`pl`); the captions' may differ (`captions.language`) |
| `label` | a disclosure over the whole reel, in the reel's own words (`"Acted scene, AI voice and video"`); it takes the place of each AI clip's badge ([Parts](#parts)) |
| `brand` | `name`, `colors {primary, accent, text, background}`, `font`, `logo` (an image) |
| `scenes[]` | `id`, `role` (the shape's), `duration` (planned seconds), `text` (on screen), `narration`, `clips[]` |
| `scenes[].narration` | `text`, `voice` (a placement name the TTS connector's config resolves, e.g. `narrator`), `audio` (the file it wrote), `alignment` (its word timings) |
| `scenes[].clips[]` | `source` (`app-recording`, `owner`, `stock`, `ai`), `path`, `start`/`end` (trim), `ai_generated`, `prompt`, `tool`, `licence`; all of them play, one after another ([Parts](#parts)) |
| `scenes[].captions` | caption lines in place of the scene's voice words, one cue each ([Captions in another language](#captions-in-another-language)) |
| `scenes[].stamp` | `text` (the word drawn, e.g. `NO`), `on` (the spoken word it lands on; default `text`): a stamp on each of the scene's cuts; not on the `cta` scene |
| `captions` | `style` (`words`: 3 words a cue; `lines`: 8), `position` (`lower`, `middle`, `upper`, `custom` with `box {x,y,w,h}` in pixels on the format's frame, which `validate` keeps inside it), `max_words` (words a cue, over `style`'s default), `font_size`, `language` (the captions' language when not the voice's) |
| `music` | `path`, `licence {source, ref}` (licensed music only), `volume_db` (default -18), `duck` (lower it under the narration) |
| `cta` | `text`, `url`, `handle`: what the `cta` scene shows |
| `qa` | QA bound overrides for this reel ([QA](#qa)) |

Rules `validate` holds a storyboard to: every `ai` clip says `ai_generated: true` and names its `tool` (the AI-content label and the legal check read them); every `stock` clip names its `licence`; music names its licence's source and ref; an `app-demo` step shows an `app-recording` clip; a `label` and a stamp's word fit the format's safe area, and no stamp is on the `cta` scene; unknown fields are refused, so a typo is not a silent default.

**Word timings** (`alignment`): ElevenLabs' with-timestamps response as it comes (its `alignment`, or `normalized_alignment`, or the alignment object alone: `characters`, `character_start_times_seconds`, `character_end_times_seconds`), or the kit's own `[{"word", "start", "end"}]` (also `{"words": [...]}`), seconds into the audio file. For a recorded human voice, any tool that writes word timings in one of these forms will do. An eleven_v3 audio tag in the alignment (`[laughs]`) is left out: it is not a spoken word.

### Captions in another language

By default the captions are the voice's own words, grouped into cues of up to `captions.max_words` (`1` is one word a cue), a cue also ending after a word that ends a phrase (`.,!?;:`). For captions in another language than the voice, English lines over a Polish voice, set `captions.language` and give every narrated scene its `captions` lines; `validate` names a narrated scene without them. Each line is one cue, timed from the voice's own word timings:

```json
"language": "pl",
"captions": {"language": "en", "position": "lower"},
"scenes": [{"id": "hook", "role": "hook", "duration": 3,
  "narration": {"text": "Ryba? Nie. Dziś jemy zupę z dyni.", "voice": "narrator", "audio": "hook.mp3", "alignment": "hook.json"},
  "captions": [
    {"text": "Fish?", "words": [0, 0]},
    {"text": "No.", "words": [1, 1]},
    "Pumpkin soup today."
  ]}]
```

- `{"text", "words": [first, last]}` shows from the first voice word's start to the last one's end (indexes into the scene's words, 0 first), in order and not overlapping;
- a plain string takes a share of the words the spans around it leave, by its word count (at least one word each);
- `{"text", "start", "end"}` is seconds into the scene, for a scene with no narration; there plain strings share the scene's planned `duration`.

Lines in the voice's own language work too (a brand's spelling on screen, the TTS's in the voice). A line naming a word the take doesn't have fails the render with the scene and line; QA's `caption-scene` fails a line timed past its scene. The plan's `captions.language` goes to the template, which sets it on the caption box.

### Formats

`format` picks the frame. The plan, the size render asks the template for (`{width}`, `{height}`), the caption boxes, the default safe zone and the `aspect` check all follow it, so a feed post renders, passes QA and shelves the same way a reel does.

| `format` | Frame | For | Default safe zone (top, bottom, left, right px) |
|---|---|---|---|
| `9:16` (default) | 1080x1920 | Instagram Reels, TikTok, YouTube Shorts, Facebook Reels | 220, 420, 60, 140 |
| `4:5` | 1080x1350 | a Facebook or Instagram feed post | 100, 150, 60, 60 |
| `1:1` | 1080x1080 | a square feed post | 80, 120, 60, 60 |

The same material in two formats is two storyboards (one with `"format": "4:5"`), each rendered and shelved on its own, by the same template: its parts take their positions from the plan's frame, so there is no separate 4:5 template. A template's `reel.json` `qa: safe_zone` applies to every format it renders: set a 4:5 or 1:1 reel's own in its storyboard's `qa:`. The starter template's compositions take their size from the plan; a project's copy that hard-codes positions for 1920 px wants a look at a 4:5 frame in Remotion Studio.

### The shapes

| Shape | Composition | Scenes |
|---|---|---|
| `hook-list` | `HookList` | hook (1), item (2 to 6), cta (1): "3 things you…", each item numbered over its clip |
| `before-after` | `BeforeAfter` | hook, before, after, cta: the before muted, the after wiping in |
| `app-demo` | `AppDemo` | hook (1), step (1 to 6, each with an `app-recording` clip), cta (1): the recording in a phone frame |

The template's `examples/` has one text-only storyboard per shape to start from, and `parts.json`, a 4:5 hook-list using every part.

### Parts

What the template draws besides the shape's own look. The kit works out each part's timing and box in the plan, so `go test` and QA see them without Remotion and the template only draws.

| Part | What the reel shows | Storyboard | Plan |
|---|---|---|---|
| Clips | all of a scene's clips, one after another; trimmed clips (`start`/`end` on every one) share the scene by length, others evenly; a clip shorter than its share plays slower, down to 0.75x, instead of freezing | `scenes[].clips[]` | each clip's `from`, `frames` (inside its scene), `rate` |
| Phone | an `app-recording` clip on a plain dark phone held up over the scene's other footage (blurred; the brand background when there is none), with a slight hand sway; the scene's title is left out (an `app-demo` step keeps its text above a smaller phone), and a number or tag in the top corner moves below the label | an `app-recording` clip; every `app-demo` step | `layout.phone`, the frame's outer box (the screen is 16 px inside) |
| Label | one disclosure over the whole reel, top left inside the safe zone; with it, AI clips drop their own `AI` badge | `label` | `label {text, box}` |
| Stamps | a big tilted word in the brand's `accent` colour (else `primary`) on each of the scene's cuts (one when it has no clips): on each time the narration says `on` when it says it once per cut, else 4 frames into the cut | `scenes[].stamp {text, on}` | the scene's `stamps[] {text, from, frames, start, end, box}` |

The label and the stamps sit at the format's default safe zone, or the storyboard's `qa: safe_zone` when it sets one, and QA's `safe-zone` check reads their boxes with the captions'. The phone is laid out per format (9:16: a 540 px screen from y 312; 4:5: 430 px from y 200; 1:1: 340 px from y 180); captions may sit over its lower edge, on their own dark box.

## Render

`reel render` works out the **plan**: each scene runs its planned `duration`, or its narration plus 0.4 s when that is longer; every word is placed on the reel's clock; the words are grouped into caption **cues** (or a scene's `captions` lines become them), each with its box on the format's frame (1080x1920 for 9:16). It writes the plan beside the reel as `<name>.plan.json`, copies the clips and logo into the template's `public/reel-<name>/`, and runs the template's `render` command from its `reel.json` with the composition for the shape. Then FFmpeg:

1. the narration stem, each scene's audio at its scene's start (`<name>.narration.wav`, kept: QA's sync check reads it);
2. the music under it, looped to length, ducked under the narration when `duck`;
3. loudness: `loudnorm` to -14 LUFS, -1 dBTP, measured first and applied with the measured values (two passes);
4. the encode: H.264 High, yuv420p, the format's frame (1080x1920 for 9:16), 30 fps, AAC 48 kHz 192 kb/s, `+faststart`.

The template only draws frames (`--muted`); every sound is the kit's.

## QA

`reel qa` (and `reel shelve`, which runs it again) checks the MP4 with ffprobe and ffmpeg's filters. Each line names what it measured and the bound; any fail exits 1.

| Check | Fails when | Default bound |
|---|---|---|
| `duration` | outside the bounds, or off the storyboard's plan | 5-90 s, within 0.5 s of the plan |
| `aspect` | the display aspect is not the storyboard's format (width × sample aspect / height) | 9:16 0.5625, 4:5 0.8, 1:1 1 |
| `loudness` | integrated loudness (`ebur128`) off the target; silence fails | -14 LUFS ±2 |
| `black-frames` | a black stretch (`blackdetect`, pixel threshold 0.10) | none of 0.2 s or more |
| `safe-zone` | a caption box, the label's or a stamp's enters what the platforms' UI covers; a narrated reel with no cues | 9:16: top 220, bottom 420, left 60, right 140 px of 1080x1920; 4:5 and 1:1: [Formats](#formats) |
| `caption-scene` | a caption starting before its scene or ending after it (a line's `end` past the scene) | none |
| `caption-sync` | a cue more than 0.15 s from its words (a line timed by seconds is `caption-scene`'s); speech (`silencedetect` on the narration stem, else the reel's own audio) starting more than 0.25 s from a scene's first word; audio and video streams starting more than 0.15 s apart | as listed |

The 9:16 safe zone is the union of Instagram Reels' and TikTok's overlays (the top bar; caption, music and buttons at the bottom; the like/comment/share column on the right). The caption checks read the cues in the plan the template drew from (`<name>.plan.json`), against the words worked out from the storyboard now: a narration re-recorded after the render shows as a sync fail until the reel is rendered again. A check with no data fails and says so; none passes silently.

**Bounds** are laid in order: the kit's defaults, then the template's `reel.json` `qa:` (every reel the project makes), then the storyboard's `qa:` (this reel). The fields: `min_seconds`, `max_seconds`, `plan_slack_seconds`, `lufs`, `lufs_slack`, `black_seconds`, `cue_slack_seconds`, `speech_slack_seconds`, `safe_zone {top, bottom, left, right}`. The report names each bound that didn't come from the defaults, and where it came from.

The report is `<file.mp4>.qa.json` (every check, measured value, bound and detail). The owner judges how the reel looks; QA only catches what a machine can.

## The shelf

`reel shelve` refuses a reel that fails QA: the owner sees only reels that pass. A passing reel becomes one `video` output in review on the issue's department's shelf (`output add --kind video --file … --report …`): the note is the verdict with the duration and loudness, and the report (`<file.mp4>.qa.md`) is kept beside the video, shown under the player in the shelf's reader. That is the reel's one owner review; publishing it stays the owner's (`owner_only`), as for every output ([Nothing goes out without the owner's stamp](departments.md#settled-rules)). A reel made again after the owner's comments goes back with `issue-cli output revise <id> --file <new.mp4>` (which drops the old report: run `reel qa` on the new file first) or a new `reel shelve`.

A reel the owner shares privately (sends to someone, never posts) is **done without publishing**: review → done, with the same shelf approval and the owner's `done` stamp, their words recorded ([Reels](departments.md#reels)).

## Publishing errors

Two upload errors the platforms give with little to go on, met on a project's first reels:

| Platform | Error | Cause and fix |
|---|---|---|
| Facebook (Graph API video upload) | code 352, the file format is not supported | the multipart file part has no file name, or one without `.mp4`: send the MP4 as a file part named `<name>.mp4`, not as bare bytes |
| Instagram (Reels container) | `ProcessingFailedError` with `retriable: false` | the MP4 itself is bad for Instagram; retrying the same file fails the same way. Re-encode it (`issue-cli reel render` again, then `reel qa`) and upload the new file |

A [direct publisher](#direct-publishing) handles both: its Facebook upload always names the file part `*.mp4` (and names the cause if a 352 still comes back), and it reports Instagram's `ProcessingFailedError` as "Instagram refused this MP4", never as a token or size error.

## Direct publishing

A reel the owner stamped for publishing goes out one of two ways, chosen per channel in the department's `publish:` map in `projects.yaml`:

- **Direct**: a connector with `publish:` (`instagram-reels`, `facebook-reels`, `tiktok`, `pinterest`). issue-cli makes the platform's own API calls itself, the keys read from Keychain into its process only, never into a child, a file or a line it prints (every line and error is redacted). Pick it for reels: the video upload, the processing wait and the platforms' errors are handled and named, and nothing has to run in Docker.
- **Postiz**: a connector with `per_run: tool: postiz` ([Connecting socials](departments.md#connecting-socials-from-the-factory)). Pick it for text and image posts across many networks, and for a network with no direct publisher.

```yaml
departments:
  - name: Video
    publish:
      instagram: instagram          # a publish: connector here: direct
      tiktok: tiktok
      linkedin: marketing/postiz    # <dept>/<name>: Marketing's Postiz
    connectors:
      - name: instagram
        kind: tool
        publish: instagram-reels
        secrets:
          META_PAGE_TOKEN: keychain:meta-page-token
          META_APP_ID: keychain:meta-app/id          # token check and refresh only
          META_APP_SECRET: keychain:meta-app/secret
        optional: [META_APP_ID, META_APP_SECRET]
        account: {page_id: "<page id>", ig_user_id: "<instagram business account id>"}
        sends: true
        needs_approved_output: true
      - name: tiktok
        kind: tool
        publish: tiktok
        secrets:
          TIKTOK_CLIENT_KEY: keychain:tiktok-app/key
          TIKTOK_CLIENT_SECRET: keychain:tiktok-app/secret
          TIKTOK_REFRESH_TOKEN: keychain:tiktok-refresh   # traded in memory for a 24-hour access token per call
        sends: true
        needs_approved_output: true
```

`issue-cli reel routes [<slug>]` prints each channel's route and the command that uses it; the reel type's publish step and its `## Published` box start there. `health lint --check departments` names a route that names no usable connector, a publisher without the secrets or `account:` ids its platform needs (Facebook: `META_PAGE_TOKEN`, `page_id`; Instagram: `META_PAGE_TOKEN`, `ig_user_id`; TikTok: `TIKTOK_ACCESS_TOKEN`, or the refresh token with the app's key and secret; Pinterest: `PINTEREST_ACCESS_TOKEN`, a board by `--board` or `account: board:`), and one without `sends: true` and `needs_approved_output: true`.

| Call | What it does |
|---|---|
| `connector exec <dept>/<name> -- publish --file <mp4> --caption-file <txt> [--title …] [--link …] [--board …] [--cover-at <ms>] [--privacy …] [--ai-label] [--send]` | Without `--send` a dry run: the route, the read-only checks (Instagram's 24-hour quota, what TikTok lets the account post now) and everything that still holds it. `--send` publishes; it needs the owner's shell (`--as-owner`), or a worker whose issue is at `publish` (after the owner's stamp) with its newest output approved on the shelf. A lead never sends. The same file is never sent twice through one connector (`<issues>/.connectors/published.jsonl`: connector, issue, file name and hash, post id and link; no key, no caption) |
| `-- token check` | Free and read-only: Meta's `debug_token` (valid, expiry, the app's data-access end, the permissions, the page and its Instagram account), Pinterest's account and board, TikTok's creator info. Exit 1 when something is invalid or ends within 7 days |
| `-- token refresh` | The owner's shell only (`--as-owner`): Meta reads a fresh user token from Graph API Explorer on stdin and stores the page's token (it doesn't expire until a password change or the data access ends); Pinterest and TikTok trade their refresh token. The new values go to the `keychain:` references under `secrets:` (`tracker.SecretWrite`), never printed |

What each platform's publisher does:

| Platform | Calls | Notes |
|---|---|---|
| `facebook-reels` | `POST graph-video …/{page}/videos` (multipart, the file part named `<name>.mp4`), then the video's status until ready | A 9:16 video shows as a Reel. A slow processing is a note, not a failure: the video is up |
| `instagram-reels` | quota, `POST /{ig}/media` (`REELS`, `upload_type=resumable`, `thumb_offset` from `--cover-at`), the bytes to rupload, the container's status until `FINISHED`, `media_publish`, the permalink | `share_to_feed` is on |
| `tiktok` | `creator_info`, `video/init` (`FILE_UPLOAD`; one chunk up to 64 MB, else 10 MB chunks), the chunks with `Content-Range`, the post's status until `PUBLISH_COMPLETE` | Privacy is `SELF_ONLY` unless `--privacy` names a level the account allows: an app TikTok hasn't audited posts only privately. `--ai-label` sets `is_aigc` |
| `pinterest` | the board by name, `POST /media` (video), the upload with Pinterest's parameters, the media's status until `succeeded`, `POST /pins` | A board is never created |

The Instagram and Facebook calls are the ones a project ran for its first reels; TikTok and Pinterest video follow the platforms' docs and are untried until a first real post. Neither Instagram's nor Facebook's API sets an AI label: with `--ai-label` the result says to turn it on by hand. Rate limits (Meta's codes 4, 17, 32, 613, 80001, 80002, HTTP 429, TikTok's `spam_risk_*`) say "try again later"; a refused token names `token check`. None of the four bills money, so there is no `spend:`.

## Changing the template

The copy `reel init` made is the project's. What the kit expects of it, in its `README.md`:

- **`reel.json`**: `render` is the command that renders one composition, run in the template's folder, with the placeholders `{composition}`, `{out}`, `{props}`, `{duration}`, `{frames}`, `{fps}`, `{width}`, `{height}`; `compositions` maps each shape to its composition id; `public` is the folder `staticFile()` reads; `qa` changes the bounds for every reel made with it.
- **Props** are the plan; `src/types.ts` mirrors `internal/reel`'s `Plan`.
- **Captions** are drawn from the plan's `cues`, each in its `box` and only between its `start` and `end`, since those are what QA checks. To move captions, change `captions.position` or `captions.box` in the storyboard, not the drawing code.
- **Sound** stays the kit's: render with `--muted`.

A new look for a shape is a change to its file in `src/shapes/`; the parts ([Parts](#parts)) are in `src/parts/`, and a top written for the 1920 px frame goes through `atY(plan, px)` (`src/frame.ts`) so the copy keeps rendering every format. A new shape of the project's own is a new composition in `src/Root.tsx` mapped in `reel.json`; the kit's shape names and their roles (above) are what `validate` checks, so a shape with other roles is a request to the engine.

## Tests

The kit's tests never call a TTS, a video API or Remotion. `internal/reel` tests the storyboard rules, the word timings, the plan, the parsers and every QA verdict on fixed input (these run everywhere), and, with ffmpeg on PATH, renders a fixture reel through a stand-in renderer (`ffmpeg -f lavfi color`, written with `standin.Write`) and runs QA on it and on reels made wrong one way each (`TestRenderWritesAQAPassingReel`, `TestQAFailsBadReels`), and a reel using every part in 9:16 and 4:5 (`TestRenderPartsPassQAInEachFormat`); `TestPlanNineSixteenUnchanged` holds a 9:16 plan without the parts to the one the kit wrote before them; they skip without ffmpeg, as on the public CI. `cmd/issue-cli`'s `TestReel*` walk `reel init`, `validate`, the mem-guard refusal and `shelve` end to end. A real Remotion render of the starter template is a manual check: `mem-guard check` first.
