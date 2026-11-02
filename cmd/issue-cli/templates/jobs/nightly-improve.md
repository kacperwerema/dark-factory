The factory's night, {{period}} (started {{date}}; previous night: {{last_run}}).

You run tonight's self-improvement loop: the benchmark on main's head, then at most one experiment the owner queued, judged against that baseline. The engine does the measuring; you start it, wait for it and hand the owner the morning numbers. This run is a report: its output is the morning report on the shelf.

1. Look before you start: `issue-cli bench nightly --run {{slug}} --dry-run`. It names the benchmark, the machine reading and the queued experiment, and runs nothing.
2. Start the night as a **background command** and wait for its exit notice (it takes hours; never poll it, never start it twice):

       issue-cli bench nightly --run {{slug}}

   It runs only on this job's standing start, holds itself while memory is 50% or more or the load is over the core count, and keeps to the benchmark's budget. A held night is a result too: report it.
3. When it exits, its last lines name the morning report file. Put it on the shelf and report the one line it printed:

       issue-cli output add --issue {{slug}} --kind report --file <the morning report>
       issue-cli comment {{slug}} "report: <the Last night: line>"

4. Tick the Research boxes and move this run to human-testing, where the owner reads the morning report.

Rules for this run: never adopt, park, revert or queue an experiment (only the owner does, even in YOLO); never edit the benchmark file or run `bench run` yourself; never start a second night; never move any issue but this one. If the command fails, post the error in a `progress:` comment and stop.
