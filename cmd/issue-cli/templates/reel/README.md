# Reel template

The reel kit's starter template: a [Remotion](https://www.remotion.dev) project with one composition per reel shape. `issue-cli reel init` copied it here; it is this project's own copy now, so change it freely (docs/reels.md in Dark Factory says what the kit expects of it).

## Licence

This template's code is Dark Factory's and yours to change. **Remotion is not part of it**: it is an npm dependency this project installs, under Remotion's own licence. Remotion is free for individuals and for companies of up to 3 people; a larger company needs a Remotion company licence (https://www.remotion.dev/license). Check it before this project renders reels for a company above that size.

## Set up

```sh
npm install          # Remotion, React, TypeScript
npx remotion studio  # optional: see the compositions with their sample props
```

Renders start Chrome: run `mem-guard check` first on a shared machine (`issue-cli reel render` does it for you).

## The shapes

| Shape | Composition | Scenes |
|---|---|---|
| `hook-list` | `HookList` | hook, 2-6 item, cta |
| `before-after` | `BeforeAfter` | hook, before, after, cta |
| `app-demo` | `AppDemo` | hook, 1-6 step (each with an app-recording clip), cta |

## The parts

Every shape draws its scenes with the same parts in `src/parts/`; the kit works out their timing and boxes in the plan, so a part only draws what the plan says.

| Part | Draws | From the storyboard |
|---|---|---|
| `Clips` | all of a scene's clips, one after another, each in its share of the scene (`from`, `frames`); a clip shorter than its share plays slower (`rate`, down to 0.75x) instead of freezing | `scenes[].clips[]`: trimmed clips (`start`/`end` on every one) share the scene by length, others evenly |
| `Phone` | an `app-recording` clip on a plain phone held up over the scene's other footage, blurred, with a slight hand sway; its box is `layout.phone`; the shapes leave a phone scene's title out (app-demo steps keep theirs above a smaller phone) | a scene with an `app-recording` clip; app-demo steps always |
| `SceneLabel` | the storyboard's `label` over the whole reel, in `label.box`; with a label, AI clips don't get their own `AI` badge | `label`, e.g. `"Acted scene, AI voice and video"` |
| `Stamps` | a big tilted word on each of a scene's cuts, in the brand's accent colour (else primary), landing on each time the narration says it when it does once per cut | `scenes[].stamp`: `{"text": "NO"}` (`on` names another spoken word) |
| `Captions`, `Title`, `Media`, `Cta` | the cues, a scene's text, one clip, the closing card (handle and URL) | as before |

Positions come from the plan's frame, so one template renders 9:16, 4:5 and 1:1: a top written for 1920 px goes through `atY(plan, px)` (`src/frame.ts`). The label and stamp boxes are QA's (`safe-zone` checks them), so move them through the storyboard, not the drawing code.

`examples/` has a storyboard for each shape, text only, and `parts.json` (a 4:5 hook-list with a label, a stamped hook of three cuts and a phone scene): add the narration audio and clips the project's connectors make, then `issue-cli reel render examples/hook-list.json --template .`.

## What the kit expects

- **`reel.json`**: `render` is the command that renders one composition, run in this folder, with `{composition}`, `{out}`, `{props}` (also `{duration}`, `{frames}`, `{fps}`, `{width}`, `{height}`); `compositions` maps each shape to its composition id; `public` is the folder `staticFile()` reads; `qa` may change the QA bounds for every reel made here.
- **Props** are the plan the kit writes (`<reel>.plan.json`); `src/types.ts` mirrors it.
- **Captions** are drawn from `cues`, in each cue's `box` and only between its `start` and `end`: QA checks those boxes against the platforms' safe zones and those times against the narration. Moving captions means changing `captions.position` (or `captions.box`) in the storyboard, not the drawing code. Captions in another language than the voice (English over a Polish voice) are the storyboard's too: `captions.language` and each scene's `captions` lines, timed on the voice's words (docs/reels.md, Captions in another language); the box carries `lang` from `captions.language`.
- **Sound** is the kit's: render with `--muted` (as `reel.json` does). The kit lays the narration and music, brings the mix to -14 LUFS and encodes the final MP4.

To add a shape of your own, add a composition in `src/Root.tsx` and map a shape to it in `reel.json`; the kit's shape names and their scene roles stay as listed above, so a new look for an existing shape is the usual change.

## Media and voices

- **Source media** (app recordings, clips, photos) go on the work branch, beside the storyboard (e.g. `reels/<name>/`), before the storyboard names them: a gitignored `media/` folder or an unmerged branch is not a source, since the render and the next agent read only the branch.
- **Brand pronunciation**: listen to the brand name in each voice's first take; keep the spelling each voice says right in `reels/pronunciation.md`, one line per voice placement, and put it only into the TTS text, never the captions.
- **Uploading**: Facebook's code 352 means the upload's file part had no `.mp4` file name; Instagram's `ProcessingFailedError` with `retriable: false` means this MP4 is bad: render and QA it again rather than retry the same file (docs/reels.md, Publishing errors).

## Keys

A key the app or a render needs (a TTS or a video API) never goes in a file here: keep it in Keychain and let `issue-cli connector exec` hand it to the command. `.gitignore` ignores `.env` and `.env.*`; keep only `.env.example` with the variable names and empty values. The project's pre-push guard (`issue-cli privacy hook`) refuses a push that carries a key or an env file.
