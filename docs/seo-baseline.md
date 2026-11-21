---
title: "SEO Baseline: a Walk-through"
order: 10
---

# SEO baseline: a walk-through

Every site needs the same SEO groundwork before posts and page refreshes pay off. Search engines and AI assistants have to be able to read it, visitors need somewhere to go, and you need numbers to see what changed. This page takes you from the first questions to the first measurement. Each step is marked **You** (only you can do it: accounts, DNS, your CDN's dashboard, the deploy) or **Agent** (Marketing's agents do it in the site's repo).

In the factory this is one issue per site, of type `seo-baseline` in the Marketing department (system SEO):

```bash
issue-cli create --department marketing --type seo-baseline --system SEO --title "example.com: SEO baseline"
```

Its path is brief → draft → owner review → publish → measure → done. Your steps are boxes marked `(owner)` under `## Owner steps`. No agent can tick them, and the issue can't move to measure until you have. Deploying waits for your `publish` approval, which YOLO never gives for you.

The lessons here come from a real site's first SEO setup.

## 1. Answer five questions first (You)

The agent asks these in the brief and writes your answers under `## Owner answers`. Everything after depends on them.

1. **Where does the traffic go?** A waitlist (sign-up still closed), an open sign-up, a purchase.
2. **Which market and language?**
3. **How much content, and who approves it?** For example two posts a week: the agent drafts, you read and approve.
4. **Where do we start?** The technical base and measurement first is the usual answer.
5. **Which bots may read the site?** Search bots only (Googlebot, Bingbot, OAI-SearchBot, ChatGPT-User, PerplexityBot, ClaudeBot), or AI training bots as well (GPTBot, Google-Extended)?

The brief also names the site, the repo it is built from, the app or sign-in subdomain if there is one, the host and CDN, and who deploys.

The baseline is technical work, so it doesn't wait for the [brand foundation](departments.md#brand-foundation-and-research-mode). Articles do: settle brand.md and icp.md before the first post.

## 2. Search Console with access for agents (You)

1. **Add a Domain property, not a URL prefix.** Search Console → Add property → Domain → `example.com` (no https, no www), then add the `google-site-verification=…` TXT record in your DNS and verify. A Domain property covers every subdomain, http and https. A URL-prefix property on a host that doesn't resolve (a `www.` you never set up) collects nothing.
2. **A Google Cloud project** with the **Google Search Console API** turned on. No billing card needed.
3. **A service account** with no roles in the project, and a JSON key for it.
4. **Add the service account in Search Console** (Settings → Users and permissions) with **Restricted** permission. Restricted is enough for search analytics and URL Inspection. Submitting a sitemap needs Full; you can also submit it yourself.
5. **Keep the key outside the repo**, for example `~/.config/<project>/gsc.json` with `chmod 600`, or in Keychain. The department's connector names it as a reference (`env:` or `keychain:`), never its content. It never goes into git, a log or a chat.

Query data shows 2–3 days after the property is created.

## 3. Bing Webmaster Tools and IndexNow (You)

ChatGPT and Copilot search lean on Bing's index, so Bing matters for AI answers.

1. Sign in at `bing.com/webmasters` and choose **Import your sites from GSC**. Bing takes over the verification and the sitemaps, with no DNS record.
2. Submit the sitemap (`https://example.com/sitemap.xml`) if the import didn't bring it.
3. IndexNow needs nothing from you: the agent adds a key file to the site and the deploy sends new and changed URLs.
4. Optional: an API key (Settings → API access), kept outside the repo like the Search Console key.

## 4. Check that your CDN lets the bots in (You)

Since 2025 Cloudflare blocks AI bots on new domains by default, whatever your robots.txt says, and it can replace your robots.txt with its own. Check this first. Other CDNs have similar switches. Cloudflare's menu names change every few months, so search for the words in quotes:

1. **"AI Crawl Control"** → Crawlers: set each bot you allowed to Allow.
2. **"Block AI bots"** (Security → Settings or Security → Bots): off.
3. **Managed robots.txt** ("Instruct AI bot traffic with robots.txt"): off, or Cloudflare adds its own text to yours.
4. **Bot Fight Mode**: off for the marketing site. It can send bots a JavaScript challenge they can't solve.

After a few days, AI Crawl Control's Metrics shows real bot visits and their answers (200 or 403). That's the only sure proof: the agent's check only sends the bot's User-Agent header.

## 5. The technical basics (Agent)

Under `## Technical basics`, each box names what the site must have and the test that checks it. The tests go in the site's own suite, so the next change to the site can't quietly break SEO.

- **sitemap.xml** with each page's final URL (the one with no redirect: if the host turns `/page.html` into `/page`, list `/page`) and `lastmod` set to the last change of the content, not of every commit. robots.txt has a `Sitemap:` line.
- **robots.txt** that allows each bot you allowed by name.
- **JSON-LD** (organization, product, article, FAQ) with true facts only: no price when there is no price list, no ratings you don't have, FAQ entries the same as the FAQ on the page. A `<script type="application/ld+json">` block is data and a CSP of `script-src 'self'` doesn't block it.
- **An Open Graph image** 1200×630 rendered from code (an HTML source in the site's fonts and colors, shot to PNG), and **llms.txt** (cheap, effect uncertain).
- **noindex on the app subdomain, by header.** `X-Robots-Tag: noindex, nofollow` on every response. Not `Disallow: /`: a crawler that robots.txt keeps out never sees the header, and a page already in the index stays there as "indexed, though blocked". Add `Disallow` only after the pages have dropped out.
- **Page rules.** Every text response (HTML, sitemap.xml, robots.txt, llms.txt) sends `Content-Type: …; charset=utf-8` from the server or CDN (a `<meta charset>` alone lets Bing read the page as Latin-1 and garble og:title), and the article template keeps `<title>` at most 60 characters with the brand suffix, the primary phrase first. `issue-cli page-check <site>/sitemap.xml` checks every page; the weekly SEO pass runs it again (docs/departments.md, [Page rules](departments.md#page-rules)).

## 6. The waitlist (Agent)

Under `## Waitlist`, when the traffic goes to a waitlist. When it goes to an open sign-up or a purchase, the agent marks the section N/A with your answer.

- A plain HTML form that works without JavaScript, stored on your server. A repeat address gets the same answer and no duplicate, so nobody learns who is on the list.
- A required, unticked consent box linking a privacy-policy section that says what is kept, why, on what legal basis, for how long and how to leave. An unsubscribe link. No IP address or user agent stored.
- Spam protection without third-party scripts: a honeypot field, a per-IP limit and a daily cap.
- No email is sent: sending costs money and goes outside the machine, so it is your call later. You can read the list (an admin page or an export).

## 7. Pre-rendered pages and the article template (Agent)

Under `## Pre-rendered pages`. AI crawlers usually don't run JavaScript, so a page built in the browser is empty to them. Static HTML is the best case for search engines and models alike.

- A static site generator (Astro, 11ty or similar) builds every page to HTML. Existing hand-written pages are copied as they are, and a test compares their bytes.
- An article template the build enforces: frontmatter (title, a short answer, description, FAQ, author, date) and `##` headings phrased as questions. A broken article fails the build. `draft: true` keeps an article out of the output, the sitemap and llms.txt.
- Articles in a folder of the main domain (`example.com/guide/…`), not a subdomain: a folder strengthens the whole domain.
- The sitemap and llms.txt come from one list, so a new article lands in both by itself.
- IndexNow on deploy, for new or changed URLs only (compare against the live sitemap).

## 8. Review and deploy (You, then Agent)

The agent puts the change set on the shelf (what changed per section, the branch and its commits). You approve it there and give `publish` approval on the issue. Then the agent deploys, or you do if the brief says so. After the deploy the agent checks as each allowed bot (`curl -A '<bot user agent>' https://example.com/robots.txt` and one page should answer 200 with your file) and sends IndexNow.

If a bot gets 403, or a robots.txt that isn't yours, go back to step 4.

## 9. Measure (Agent)

The issue moves to measure once your owner steps are ticked. The agent reads, through the department's `openseo` connector or a small script in the site's repo:

- Search Console sitemaps (fetched, URLs seen), URL Inspection for each sitemap URL, queries and pages.
- Bing Webmaster Tools, if you gave it an API key.
- The bots check again.

It writes the numbers under `## Measure` with where each came from. The first weeks have almost no query data, so a number it can't read yet is "no value yet" with the reason, not zero. Google doesn't always fetch a new sitemap right away: submitting it after the first articles go out is your step (the service account is Restricted).

## The order that worked

1. Your five answers.
2. Search Console with the service account, and the CDN check.
3. The technical basics, with noindex on the app.
4. The brand foundation in Marketing (brand, voice, ICP). No article goes to draft without it.
5. The waitlist, the static build and the article template, then content.

After that the weekly [SEO pass](departments.md#channels-as-types) takes over.

## Gotchas

- When a promise on the page changes, change it everywhere: the description in search results, JSON-LD and llms.txt too.
- A link that answers 403 or times out from a script isn't necessarily dead: some sites refuse automated clients. Check those in a browser.
- An unofficial Search Console MCP server is someone else's code with access to your key. A small script in your repo that does the few calls you need is often the safer choice.
