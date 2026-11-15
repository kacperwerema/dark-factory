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
