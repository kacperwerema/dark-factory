package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lead's view of the owner's queue (/p/<project>/postmortems):
// postmortem list --grouped prints its groups, made by the same
// postmortem.GroupItems and Fixes as the page, and postmortem fixed links
// an action to the issue or commit that fixes it, so it needs no answer.

// postmortemFixes is what tells, on ctx's board, that an action is fixed:
// the viewer's postmortemFixes over the CLI's project.
func postmortemFixes(proj *tracker.Project, issues []*tracker.Issue, now time.Time) *postmortem.Fixes {
	root := projectRoot(proj)
	by := map[string]*tracker.Issue{}
	for _, is := range issues {
		by[is.Slug] = is
	}
	return &postmortem.Fixes{
		Repo:     root,
		MainOf:   func() string { name, _ := tracker.MainBranch(root); return name },
		Memories: postmortem.MemoriesFor(proj.IssueDir, now),
		IssueState: func(slug string) (string, bool, bool) {
			is := by[slug]
			if is == nil {
				return "", false, false
			}
			return is.Status, proj.IssueDone(is), true
		},
	}
}

// pmQueueItem is one action as list --grouped prints it.
type pmQueueItem struct {
	Issue string `json:"issue"`
	File  string `json:"file"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Place string `json:"place"`
	Fix   string `json:"fix,omitempty"`
}

// pmQueueGroup is one row of the page: copies of one thing, or one alone
// action.
type pmQueueGroup struct {
	Key   string        `json:"key"`
	Place string        `json:"place"`
	Title string        `json:"title"`
	Count int           `json:"count"`
	Alone bool          `json:"alone"`
	Items []pmQueueItem `json:"items"`
}

// pmQueue is the owner's queue as the lead reads it.
type pmQueue struct {
	Open   int            `json:"open"`
	Posts  int            `json:"posts"`
	Rows   int            `json:"rows"`
	Groups []pmQueueGroup `json:"groups"`
	Fixed  []pmQueueItem  `json:"fixed"`
	URL    string         `json:"url"`
}

func pmItem(it postmortem.Item) pmQueueItem {
	return pmQueueItem{Issue: it.Issue, File: it.File, ID: it.ID, Title: it.Title, Place: it.PlaceString(), Fix: it.Fix.Words()}
}

func buildPmQueue(ctx *Context) (pmQueue, error) {
	issues, err := ctx.Project.LoadIssues()
	if err != nil {
		return pmQueue{}, err
	}
	open, fixed := postmortemFixes(ctx.Project, issues, ctx.now()).Split(postmortem.Queue(retrosDir(ctx)))
	q := pmQueue{Open: len(open), Groups: []pmQueueGroup{}, Fixed: []pmQueueItem{}, URL: fmt.Sprintf("%s/p/%s/postmortems", viewerBaseURL(ctx.Project), ctx.Project.Slug)}
	posts := map[string]bool{}
	for _, it := range open {
		posts[it.File] = true
	}
	q.Posts = len(posts)
	for _, it := range fixed {
		q.Fixed = append(q.Fixed, pmItem(it))
	}
	for _, pg := range postmortem.GroupItems(open) {
		for i, g := range pg.Groups {
			key := fmt.Sprintf("%s-%d", pg.Place, i)
			if g.Alone {
				// Each alone action is its own answer: its own row.
				for _, it := range g.Items {
					q.Groups = append(q.Groups, pmQueueGroup{Key: key, Place: pg.Place, Title: it.Title, Count: 1, Alone: true, Items: []pmQueueItem{pmItem(it)}})
				}
				q.Rows += len(g.Items)
				continue
			}
			grp := pmQueueGroup{Key: key, Place: pg.Place, Title: g.Items[0].Title, Count: len(g.Items)}
			for _, it := range g.Items {
				grp.Items = append(grp.Items, pmItem(it))
			}
			q.Groups = append(q.Groups, grp)
			q.Rows++
		}
	}
	return q, nil
}

func runPostmortemListGrouped(ctx *Context) error {
	q, err := buildPmQueue(ctx)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, q)
	}
	w := ctx.Stdout
	copies, groups, alone := 0, 0, 0
	place := ""
	for _, g := range q.Groups {
		if g.Place != place {
			place = g.Place
			fmt.Fprintf(w, "== %s ==\n", place)
		}
		if g.Alone {
			alone++
			it := g.Items[0]
			fmt.Fprintf(w, "  alone  %s  %s#%s  %s%s\n", it.Issue, it.File, it.ID, it.Title, fixNote(it.Fix))
			continue
		}
		groups++
		copies += g.Count
		fmt.Fprintf(w, "  [%s] %d say the same: %s\n", g.Key, g.Count, g.Title)
		for _, it := range g.Items {
			fmt.Fprintf(w, "      %s  %s#%s%s\n", it.Issue, it.File, it.ID, fixNote(it.Fix))
		}
	}
	if len(q.Fixed) > 0 {
		fmt.Fprintf(w, "== fixed already: no answer needed ==\n")
		for _, it := range q.Fixed {
			fmt.Fprintf(w, "  %s  %s#%s  %s  %s\n", it.Issue, it.File, it.ID, it.Fix, it.Title)
		}
	}
	fmt.Fprintf(w, "%d open action(s) of %d post-mortem(s) need %d answer(s): %d group(s) of %d copies, %d alone; %d fixed already need none.\n",
		q.Open, q.Posts, q.Rows, groups, copies, alone, len(q.Fixed))
	if q.Open > 0 {
		fmt.Fprintf(w, "The owner answers them, one answer per row, on %s\n", q.URL)
		fmt.Fprintf(w, "An action an issue or a commit fixes: %s\n", tracker.InjectProject(ctx.Project, "issue-cli postmortem fixed <file>#A<n> --by <slug|sha> [--copies]"))
	}
	return nil
}

func fixNote(fix string) string {
	if fix == "" {
		return ""
	}
	return "  (" + fix + ")"
}

func runPostmortemFixed(ctx *Context, args []string) error {
	ref, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		ref, rest = args[0], args[1:]
	}
	fs := newFlagSet("postmortem fixed", ctx)
	by := fs.String("by", "", "the issue slug or the commit sha on main that fixes it")
	copies := fs.Bool("copies", false, "link every copy the queue groups with it too")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	if ref == "" {
		ref = fs.Arg(0)
	}
	if ref == "" {
		return &usageError{msg: "postmortem fixed needs the action: <retro file>#A<n> (postmortem list --grouped prints them)\n\n" + postmortemUsage}
	}
	file, id, ok := strings.Cut(ref, "#")
	file = filepath.Base(strings.TrimSpace(file))
	id = strings.ToUpper(strings.TrimSpace(id))
	if !ok || file == "" || id == "" {
		return &usageError{msg: fmt.Sprintf("%q is not <retro file>#A<n>\n\n%s", ref, postmortemUsage)}
	}
	fix := strings.TrimSpace(*by)
	if fix == "" {
		return &usageError{msg: "postmortem fixed needs --by <slug|sha>: the issue or the commit on main that fixes it\n\n" + postmortemUsage}
	}
	issues, err := ctx.Project.LoadIssues()
	if err != nil {
		return err
	}
	if !knownFix(ctx, issues, fix) {
		return fmt.Errorf("--by %q is neither an issue of %s nor a commit on main: nothing was linked", fix, ctx.Project.Slug)
	}
	queue := postmortem.Queue(retrosDir(ctx))
	var target *postmortem.Item
	for i := range queue {
		if queue[i].File == file && queue[i].ID == id {
			target = &queue[i]
		}
	}
	if target == nil {
		return fmt.Errorf("%s#%s is not an open action of a post-mortem the owner's queue reads (postmortem list --grouped lists them)", file, id)
	}
	todo := []postmortem.Item{*target}
	if *copies {
		keys := postmortem.GroupKeys(queue)
		for _, it := range queue {
			if it.Key() != target.Key() && keys[it.Key()] == keys[target.Key()] {
				todo = append(todo, it)
			}
		}
	}
	byIssue := map[string]*tracker.Issue{}
	for _, is := range issues {
		byIssue[is.Slug] = is
	}
	for _, it := range todo {
		if err := postmortem.LinkFix(it.Path, it.ID, fix); err != nil {
			return fmt.Errorf("%s: %w", it.Key(), err)
		}
		fmt.Fprintf(ctx.Stdout, "✓ %s (%s): Fixed: %s\n", it.Key(), it.Issue, fix)
		if is := byIssue[it.Issue]; is != nil {
			_ = tracker.AddCommentBy(is.FilePath, 0, fmt.Sprintf("postmortem: %s linked to its fix %s (retros/%s)", it.ID, fix, it.File), "cli", commentAuthor(ctx.Project, is))
		}
	}
	f := postmortemFixes(ctx.Project, issues, ctx.now())
	words := f.Of(postmortem.Action{Fixed: fix}).Words()
	if strings.HasPrefix(words, "fixed by") {
		fmt.Fprintf(ctx.Stdout, "%s: %d action(s) need no answer now.\n", capitalize(words), len(todo))
	} else {
		fmt.Fprintf(ctx.Stdout, "%s: the action(s) wait until it is done.\n", capitalize(words))
	}
	return nil
}

// knownFix is whether fix names an issue of the board or a commit on main.
func knownFix(ctx *Context, issues []*tracker.Issue, fix string) bool {
	for _, is := range issues {
		if is.Slug == fix {
			return true
		}
	}
	root := projectRoot(ctx.Project)
	main, err := tracker.MainBranch(root)
	if err != nil {
		return false
	}
	_, err = postmortem.OnMain(root, main, fix)
	return err == nil
}
