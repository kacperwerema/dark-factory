# Brand and ICP: Dark Factory

Draft from brand-foundation-dark-factory-software-for-running-a-company-with-departments; nothing settled until the owner picks.

## Brand
### Proposal (the owner settles it)

**Core idea: a dark factory with one lamp on, yours.** Everyone else says "dark factory" means humans are "neither needed nor welcome" ([Willison/Shapiro](https://simonwillison.net/2026/jan/28/the-five-levels), [StrongDM](https://factory.strongdm.ai/)). Ours runs dark everywhere except the moments that matter, and those are lit orange and wait for you. Source: README "Agents do the work. You make the decisions that matter."; the design system's "Orange is reserved for you".

**Line options** (pick one, or none):
- A. "Agents do the work. You make the decisions that matter." (in use, README)
- B. "A dark factory with one light on: yours."
- C. "Run the company. Let the agents run the floor."

### Traits

1. **Owner in charge, not in the way.** The agents work; the owner stamps the few costly moments and is never asked to watch. Source: README rule "You own the costly moments", `docs/why.md`; outside, approval fatigue at ~93% of prompts ([Anthropic via Beri.net](https://www.beri.net/article/human-in-the-loop-agent-approval-gates-buyers-guide-reviewer-cost-kill-switch)) shows why fewer, better gates matter.
2. **Playful on the surface, strict underneath.** Robots, crates, stamps, a lot on a map: a board game you can read at a glance; under it, gates that refuse a move until the check passes. Source: design system "board-game objects … nothing is decoration"; README "One step at a time".
3. **Honest.** Real data or an honest empty state; no fake rows, no inflated claims, budgets shown as they are. Source: `docs/UI/workshop-board.md` "Honest empty states"; departments settled rules "Only real data", "Budgets are shown, never enforced".
4. **Yours, on your machine.** Files, git and tmux you can read; no SaaS, no API keys, works with Claude Code and Codex; private projects stay private. Source: README "Everything is files"; `docs/why.md` workflow harness is portable; departments "Private projects stay private". Contrast: Factory.ai and Conductor are paid products; Paperclip is a server with its own DB.
5. **A company, not a chat.** HQ, departments, leads, workers, a shelf of outputs: the agents are organised like a firm with jobs and goals. Source: `docs/departments.md`, the design system's levels Map › Company › Hall › Station. Paperclip uses the same metaphor ([paperclip.ing](https://paperclip.ing/)), so this trait alone doesn't set us apart; it does with traits 1 and 3.

### Voice

Plain, short, concrete. Sentences an engineer would say to a colleague at the board. Names things by what they are (a gate, a worker, a crate), says what's missing and the command that fixes it, and lets the owner's own words stand as they wrote them. Dry humour from the factory metaphor, never hype.

Example (landing page):
> Ten agents, one owner. They pick up issues, walk your workflow and stop at the gates you drew. When something needs you, it turns orange. Everything else runs in the dark.

Example (empty state):
> No outputs yet. Marketing puts drafts here with `issue-cli output add`; you approve each one.

Source: README and `docs/why.md` tone; `docs/UI/workshop-board.md` (English only, owner quotes as written); Conductor's plain product-first tone is the closest market neighbour ([conductor.build](https://www.conductor.build/)).

### Do

- Show the real board: screenshots and GIFs of the map, hall and station (README already leads with the hall).
- Keep orange for the owner's move in every asset, page and slide; yellow-black stripes only for YOLO. (design system, workshop-board rules)
- Use the board-game look: thick ink outline (`#1B1F2A`), hard shadow, flat colour, no gradients; Bricolage Grotesque 800 for names and numbers, Figtree for text, JetBrains Mono for commands. (design system §4–5)
- Give each department its colour: HQ `#F2B233`, Product `#3D7BF7`, Marketing `#E2559B`, Support `#19A7B8`, Sales `#8BC34A`, Finance `#B07A4A`. (design system §4)
- Say what the gate checks and what the owner decides; name the command. (README rules, `docs/why.md`)
- Credit the tools: Claude Code and Codex do the work; we're the process around them. (`docs/why.md`)
- Quote the owner as they wrote it.

### Don't

- Don't promise "zero humans", "fully autonomous" or "lights out". It's the opposite of our point and the market is already sceptical ([Stanford CodeX](https://law.stanford.edu/2026/02/08/built-by-agents-tested-by-agents-trusted-by-whom/), [MindStudio on Paperclip](https://www.mindstudio.ai/blog/what-is-paperclip-zero-human-ai-company-framework-3)).
- Don't use orange for anything else: not for the logo, not for a button that doesn't wait on the owner.
- Don't show mock data or invented numbers in marketing shots; use `make demo` or a real board. (workshop-board rules)
- Don't claim speed or savings we haven't measured; the benchmark and Stats page are where numbers come from. (`docs/benchmark.md`)
- Don't make it dark and menacing (black, red, industrial grit). The "dark" is in the name only; the look is a bright board. (design system: Paper `#FFFFFF`, Board `#E8ECF2`)
- Don't call it "AI employees" or give agents human names and faces; agents are robots, the owner is "you". (design system §2)
- Don't knock competitors by name in copy.

### What the owner settles

1. The public name: **Dark Factory** or **AI Workflow Engine** (D-2 proposes Dark Factory, with AI Workflow Engine as the descriptive line). Note [Factory.ai](https://www.factory.ai/) owns "Factory" in this market.
2. The line: A (in use), B or C above.
3. Trait 5 ("a company, not a chat"): keep it as a trait, or treat it as the product and leave the traits to 1–4? It's where Paperclip stands too.
4. How much humour: dry and rare (proposed), or more playful like the robots on the board.
5. Language (D-3 proposes English first; the design system is in Polish).

## ICP
### Proposal (the owner settles it)

#### Segment 1 (primary): the solo builder running several projects with agents

- **Who:** an experienced developer or technical founder, alone or with one partner, running 2–10 projects at once (own products, games, client work) with Claude Code or Codex on their own machine. Already on tmux and git worktrees. Places: anywhere, English-speaking online. Source: our own owner's use (the dark-factory board runs the engine, a game, a meal planner and client sites as lots on one map); [Reddit, Feb 2026](https://reddit.sentinel-team.org/posts/1qzduim/snapshots/2026-02-08T20%3A03%3A51.59357Z); [claude-squad](https://www.sourcepulse.org/projects/2338008).
- **Problems, in their words:** "tmux handled the sessions fine, but I couldn't keep up" and "the productivity gains from parallelism were getting eaten by the overhead of managing it" ([jmux](https://www.producthunt.com/products/jmux)); agents "start stepping on each other's files" and the gap is monitoring them "when I'm away from my desk" ([Reddit](https://reddit.sentinel-team.org/posts/1qzduim/snapshots/2026-02-08T20%3A03%3A51.59357Z)); agents skip the quality checks ([HN](https://news.ycombinator.com/threads?id=unohee)); approving everything by reflex ([~93% of prompts approved](https://www.beri.net/article/human-in-the-loop-agent-approval-gates-buyers-guide-reviewer-cost-kill-switch)). Ours: "terrible at knowing when to stop" (`docs/why.md`).
- **Where they are:** r/ClaudeAI, r/ChatGPTCoding, r/LocalLLaMA; Hacker News (Show HN); X/Bluesky around Simon Willison's posts on agents and the "dark factory"; GitHub trending and awesome-claude-code lists; Product Hunt. Search terms: "run multiple claude code agents", "claude code worktrees tmux", "agent orchestration", "dark factory", "human in the loop coding agents", "workflow for coding agents".
- **How to reach them:** a Show HN and a README that lead with the hall GIF; posts that answer the search terms with our own board as the example; a reply to the "dark factory" debate ("one lamp stays on"). **Message:** "Run ten agents without watching ten terminals. They walk your workflow, stop at your gates, and everything that needs you turns orange."

#### Segment 2: the one-person company that wants departments

- **Who:** a solo or two-person founder (indie SaaS, small studio, newsletter or product business) who wants agents to do marketing, support, sales as well as code, under goals they set. Source: [Ryan Carson running his startup solo with agents](https://creatoreconomy.so/p/how-this-5x-founder-runs-his-startup-solo-with-ai-agents-ryan-carson); [Maven: AI for founders, run a company with agents](https://maven.com/theaiinternship/ai-for-founders-run-a-company-with-agents); [arXiv: Digital co-founders](https://arxiv.org/abs/2511.09533v1); Paperclip's 31k stars show the demand ([jimmysong.io](https://jimmysong.io/ai/paperclip)); our departments, goals, jobs and shelf (`docs/departments.md`).
- **Problems:** chat-first "AI CEO" tools act without a process; they want agents to draft, not act (the founder with 25 marketing agents on "agents draft, they don't act"); fear of a bad post going out or money spent; no single view of what each department is doing and what it costs.
- **Where they are:** Indie Hackers, r/SaaS, r/Entrepreneur, r/indiehackers, X build-in-public, founder newsletters (Lenny's, Behind the Craft), Product Hunt. Search terms: "run a company with AI agents", "AI marketing department", "one-person company AI", "Paperclip alternative".
- **How to reach them:** the company page and outputs shelf as the hero; a case of our own Marketing department (this foundation is the first) written up honestly. **Message:** "Your company, staffed by agents. Departments with goals and jobs; nothing goes out until you stamp it."
- **Caveat:** they must be comfortable with a terminal, git and Claude Code today; non-technical founders are not reachable until install is easy (roadmap goal 2).

#### Segment 3 (later): the small agency or studio

- **Who:** 2–15 people delivering client sites, SEO or apps, wanting one board across client projects with a lead per client. Source: Marketing's mission "Every client site refreshed every week" (`departments/marketing/role.md`); the map of lots.
- **Why later:** the product is one owner on one machine; teams need shared gates and access. Reach later via the agency's own case study.

#### Who is not a target

- Enterprises buying agent platforms with SSO and compliance (Factory.ai's market: EY, Wipro, [factory.ai](https://www.factory.ai/)).
- Teams wanting a true no-human "lights out" factory ([StrongDM](https://factory.strongdm.ai/)): we put the human at the gates on purpose.
- Non-technical users wanting a hosted chat app (Soloop, Adaptive), until install is one step.
- People who want a single agent in their IDE (Cursor, Copilot): they have one task at a time and no need for a process.

#### What the owner settles

1. Primary segment: solo builder (1, proposed: the product fits them today) or one-person company with departments (2, the direction of the roadmap and Paperclip's market).
2. Whether agencies (3) are a target this year.
3. Free and open source vs a paid tier later (question: comment): it changes the buying path for 2 and 3.
4. First channel: Show HN + Reddit (proposed, cheap, matches segment 1) or build-in-public on X.

## Research (sources)

### Our own material (the established brand)

- `issues/Factory/designs/workshop-board/design-system.dc.html` + `ds.png` ("Plansza warsztatowa" v0.1): a clean board with board-game objects (thick outline, hard shadow, no gradients); "every object stands for one thing in the data, nothing is decoration"; robots for agents, a crowned robot for the CEO, "Ty" (you) as the only one who stamps gates; orange reserved for the owner ("if something is orange, it waits on your move"); Bricolage Grotesque 800 for names and numbers, Figtree for the interface, JetBrains Mono for slugs and tmux; five rules (motion only where something happens, dashed outline = not built yet, every number leads one level down, building height = open work, a line must carry something).
- `docs/UI/workshop-board.md`: the rules as built: English only, the owner's quotes kept as written, honest empty states ("never draw a fake or sample row"), orange waits on the owner, yellow-black stripes mean YOLO only.
- `README.md`: the line already in use, "Agents do the work. You make the decisions that matter."; "We believe agents should do the work and people should own the decisions."; "You stop watching the agent and start running the factory."; five rules (one step at a time, you own the costly moments, guidance just in time, everything is files: "No database, no SaaS, no API keys", the factory runs itself and you steer).
- `docs/why.md`: agents "are great at writing code and terrible at knowing when to stop"; vendor harness = sandbox and lock-in, workflow harness = "what good looks like", portable across Claude Code and Codex.
- `docs/roadmap.md`: goal 2 "Others can adopt it" (install → init → first approval in minutes); goal 3 "The harness checks the work".
- `docs/departments.md` settled rules: nothing goes out without the owner's stamp, budgets shown never enforced, private projects stay private, only real data.

### The term "dark factory"

- [Simon Willison on Dan Shapiro's five levels](https://simonwillison.net/2026/jan/28/the-five-levels): level 5 is the "dark software factory", "a black box that turns specs into software", from Fanuc's lights-out plant, "a place where humans are neither needed nor welcome".
- [StrongDM AI, Software Factories and the Agentic Moment](https://factory.strongdm.ai/) and [Willison's write-up](https://simonwillison.net/2026/feb/7/software-factory/): rules "code must not be written by humans; code must not be reviewed by humans"; scenarios and a digital twin universe instead of review; "$1,000 on tokens per human engineer".
- [Stanford CodeX, Built by agents, tested by agents, trusted by whom?](https://law.stanford.edu/2026/02/08/built-by-agents-tested-by-agents-trusted-by-whom/): the trust gap; agent-judged satisfaction is circular.
- [Tessl pattern: Dark Factory](https://tessl.io/patterns/agentic-development-workflow/dark-factory/), [env.dev guide](https://env.dev/guides/ai-dark-factory): the term is now a named pattern; several guides call it an aim, not a default ("many claimed level 5 setups are level 3 with more automation").
- Takeaway: the name is topical and recognised by the exact audience, but the public meaning is "no humans". Our product's whole point is the one lamp left on: the owner's stamp. The brand has to own that twist, or it reads as the opposite of what the product does.

### Competitors and neighbours

- [Paperclip](https://paperclip.ing/) (MIT, self-hosted, [~31k stars per jimmysong.io](https://jimmysong.io/ai/paperclip)): the closest rival. "A team of agents for every person", "Manage business goals not pull requests"; org chart with CEO/CTO/CMO, goals traced to a mission, heartbeats, budgets that auto-pause at 100%, the user as "the board", "Autonomy is a privilege you grant, not a default". Clean minimal SaaS-ish look. [MindStudio](https://www.mindstudio.ai/blog/what-is-paperclip-zero-human-ai-company-framework-3): "zero-human" is an aspiration; output still needs review.
- [Factory.ai](https://www.factory.ai/) (Droids, $50M Series B per [BusinessWire](https://www.businesswire.com/news/home/20250925993478/en/Factory-Raises-50M-Series-B)): "agent-native software development", enterprise (EY, Wipro). Owns the word "Factory" in this market: a naming risk.
- [Conductor](https://www.conductor.build/): "Run parallel coding agents on your Mac"; workspaces, review, merge. Plain, product-first tone. Coding only, no departments or gates.
- [Vibe Kanban](https://vibecodinghub.org/tools/vibe-kanban): open-source kanban over agents in worktrees; [one listing](https://aiidelist.com/ide/vibe-kanban) says the company shut down in April 2026 (unconfirmed).
- [claude-squad](https://www.sourcepulse.org/projects/2338008): tmux + worktrees for several agents; terminal only.
- [Soloop](https://hunted.space/product/soloop), [Adaptive](https://adaptive.ai/usecases/solopreneurs): "AI CEO, CTO, CMO" workspaces for solo founders; chat-first, little process.
- [withHuman](https://www.producthunt.com/p/withhuman/withhuman): approval layer over tool calls; its makers name approval fatigue as the problem.

### Where the audience talks and what hurts

- [Reddit thread via mirror, Feb 2026](https://reddit.sentinel-team.org/posts/1qzduim/snapshots/2026-02-08T20%3A03%3A51.59357Z): several Claude Code sessions on one repo step on each other; moved to one worktree + tmux each; the gap left is watching them when away from the desk.
- [jmux on Product Hunt](https://www.producthunt.com/products/jmux): "running up to 20 Claude Code instances … tmux handled the sessions fine, but I couldn't keep up"; parallelism eaten by the overhead of managing it.
- [How I manage 10 Claude Code agents](https://lapis-forge-e7a6.here.now/2026-04-08-how-i-manage-10-claude-code-agents.md): the human's attention (who's done, who's stuck) is the bottleneck.
- [Beri.net on approval gates](https://www.beri.net/article/human-in-the-loop-agent-approval-gates-buyers-guide-reviewer-cost-kill-switch), citing [Anthropic's auto mode post](https://www.anthropic.com/engineering/claude-code-auto-mode): users approve ~93% of permission prompts ("approval fatigue"); gate fewer, more important moments. This is our "you own the costly moments" argument from outside.
- [HN comments by a multi-worker user](https://news.ycombinator.com/threads?id=unohee): agents skip the quality checks; a separate reviewer catches them. Supports "the harness checks the work".
- [Behind the Craft: Ryan Carson runs his startup solo with agents](https://creatoreconomy.so/p/how-this-5x-founder-runs-his-startup-solo-with-ai-agents-ryan-carson), [Maven course: AI for founders, run a company with agents](https://maven.com/theaiinternship/ai-for-founders-run-a-company-with-agents), [arXiv: Digital co-founders](https://arxiv.org/abs/2511.09533v1): the solo-founder-with-departments idea is live; one founder runs 25 marketing agents on the rule "agents draft, they don't act", the same as our outputs shelf.

### Open questions for the owner (noted as I went)

1. Name: "Dark Factory" (the project, the design system's title) vs "AI Workflow Engine" (README, repo). Which is the public brand?
2. Language: the design system is in Polish, the product is English only. Is the market English-speaking first?
3. Open source and free, or a paid tier later? Paperclip is MIT and free; Conductor and Factory charge.
