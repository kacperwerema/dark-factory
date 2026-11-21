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

`examples/` has a storyboard for each, text only: add the narration audio and clips the project's connectors make, then `issue-cli reel render examples/hook-list.json --template .`.

## What the kit expects

- **`reel.json`**: `render` is the command that renders one composition, run in this folder, with `{composition}`, `{out}`, `{props}` (also `{duration}`, `{frames}`, `{fps}`, `{width}`, `{height}`); `compositions` maps each shape to its composition id; `public` is the folder `staticFile()` reads; `qa` may change the QA bounds for every reel made here.
- **Props** are the plan the kit writes (`<reel>.plan.json`); `src/types.ts` mirrors it.
- **Captions** are drawn from `cues`, in each cue's `box` and only between its `start` and `end`: QA checks those boxes against the platforms' safe zones and those times against the narration. Moving captions means changing `captions.position` (or `captions.box`) in the storyboard, not the drawing code.
- **Sound** is the kit's: render with `--muted` (as `reel.json` does). The kit lays the narration and music, brings the mix to -14 LUFS and encodes the final MP4.

To add a shape of your own, add a composition in `src/Root.tsx` and map a shape to it in `reel.json`; the kit's shape names and their scene roles stay as listed above, so a new look for an existing shape is the usual change.

## Keys

A key the app or a render needs (a TTS or a video API) never goes in a file here: keep it in Keychain and let `issue-cli connector exec` hand it to the command. `.gitignore` ignores `.env` and `.env.*`; keep only `.env.example` with the variable names and empty values. The project's pre-push guard (`issue-cli privacy hook`) refuses a push that carries a key or an env file.
