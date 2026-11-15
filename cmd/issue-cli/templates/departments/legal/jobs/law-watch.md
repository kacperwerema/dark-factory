Law watch for {{period}} (started {{date}}; previous run: {{last_run}}).

Re-read every source the newest baseline report cites (`issue-cli outputs legal`), at its official text: EUR-Lex, ISAP or the Dz.U. ELI link, the regulator's page, each third party's terms.

For each source, note:

- whether its text or version changed since the date the report cites, and what changed;
- a directive whose national transposition was pending: has it passed, and from when does it apply;
- new guidance or decisions from the regulator that touch a check.

Heavy tools: where mem-guard is installed, run `mem-guard check` before anything that loads a model, starts a browser or a big server, or fetches many pages, and don't start while it says BLOCKED. One local model and one browser at a time; plain fetches first.

Put the list on the shelf as a report (`issue-cli output add --issue <this run> --kind report --title "Law watch {{period}}" --file <report.md>`), each line with the source, the date you read it and what it means for which baseline check. A change that needs work on the product is the owner's call: name it in the report, don't file or start it.

You flag and cite; you never sign off. Done when every cited source has been re-read and the report is on the shelf for the owner's review.
