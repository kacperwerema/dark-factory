OpenMontage is a separate program under the AGPL-3.0, never part of this
repository. Install it yourself (nothing below has been run):

  1. Clone it at the pinned commit, to the path you chose:
       git clone {{repo}} {{path}}
       git -C {{path}} checkout {{commit}}
  2. Set it up (Python venv, the Remotion composer, about 800 MB):
       make -C {{path}} setup
  3. Keep model weights inside the checkout, and the venv's tools first on
     the PATH the department's agents run with:
       export HF_HOME={{path}}/.hf-cache
       export PATH={{path}}/.venv/bin:$PATH
  4. Turn HyperFrames telemetry off: setup's HyperFrames CLI reports usage
     by default. In ~/.hyperframes/config.json set "telemetryEnabled": false.
  5. Leave {{path}}/.env with empty keys. A paid key goes in your Keychain
     only (security add-generic-password -s dark-factory-video -a FAL_KEY -w),
     and the connector passes it to the agent's command.
  6. Check: issue-cli health lint --check departments (the partner's path and
     pinned commit).

Licences:
  - OpenMontage: AGPL-3.0. You accept it when you clone it. The department
    runs it as a separate program and reads its files; never copy its files,
    skills or prompts into this repository, and publish your changes if you
    modify it and let others use it over a network.
  - Remotion (its composer): its own company licence, free for individuals
    and small companies, paid above that: https://www.remotion.dev/license
  - piper-tts (narration): GPL-3.0, run as a separate program.
  - Models: each has its own licence (CogVideoX-2b Apache-2.0, CogVideoX-5b
    its own, SD 2.1 openrail++, Wan and LTX their own). Check each one you
    download before a video is used commercially.

Reel connectors (paid, metered: docs/departments.md#spend-caps). Nothing is
billed until you write a monthly cap for each, in this department's entry
in projects.yaml:

  1. Kling: npm i -g @klingai/cli-global, then kling login in your own
     shell (agents never log in; the connector refuses login and logout).
     Its terms: https://kling.ai (the CLI's own LICENSE).
  2. ElevenLabs: keep the API key in your Keychain only:
       security add-generic-password -s elevenlabs-api -a "$USER" -w
  3. Caps, your numbers, per calendar month for this project:
       - name: kling
         spend: {meter: json, field: creditsConsumed, unit: credits, cap: <credits>, ...}
       - name: elevenlabs
         spend: {meter: elevenlabs, unit: characters, cap: <characters>}
  4. Voices, by placement (a storyboard names the placement), under the
     elevenlabs connector. To pick a library voice, listen to its preview
     first (free, nothing billed):
       issue-cli connector exec video/elevenlabs --as-owner -- voices search --language pl --gender female --commercial
     A real person's voice (your clone, an actor) needs consent: noted,
     where their consent is kept; until then it is refused:
       voices:
         - placement: narrator
           voice_id: <from ElevenLabs>
           name: "Owner (clone)"
           real_person: "the owner"
           consent: "<date>, own voice"
         - placement: actor-1
           voice_id: <id>
           name: "Library voice"
  5. Check: issue-cli connector check video/elevenlabs (free: the plan and
     the credits left), issue-cli connectors video (spend against each cap,
     voices with consent) and issue-cli health lint --check departments.
