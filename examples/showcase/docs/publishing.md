---
title: Sitemaps and feeds
section: Built-ins
order: 105
next: devtools
description: Generate sitemap XML, RSS 2.0, and Atom 1.0 from public content, then serve immutable snapshots or write them into a static build.
---

## Publish selected content {#overview}

The `github.com/regiellis/hyperchi/hyperchi/publishing` package generates sitemap XML, RSS 2.0, and Atom 1.0. You choose the public entries and supply absolute canonical URLs. Nothing is inferred from route registration or a request's Host header. Exclude drafts, private pages, and parameterized routes before generating a document.

Each generator returns a `Document`. Call `Bytes()` to write XML into a static build, or `Handler()` to serve a snapshot generated at startup. The handler supports GET, HEAD, ETag conditional requests, and `Cache-Control: no-cache`. It has no background work: regenerate and replace the handler when content changes, or restart the application.

## Generate a sitemap {#example}

```go
import "github.com/regiellis/hyperchi/hyperchi/publishing"

func mountSitemap(app *hyperchi.HyperChi) error {
    sitemap, err := publishing.Sitemap([]publishing.SitemapEntry{
        {URL: "https://example.com/"},
        {URL: "https://example.com/about"},
    })
    if err != nil {
        return err
    }
    app.Get("/sitemap.xml", hyperchi.WrapHTTP(sitemap.Handler()))
    return nil
}
```

Entries are sorted by URL without changing the input slice. URLs must be unique, absolute HTTP(S), shorter than 2048 bytes, and share an origin. Credentials, fragments, whitespace, and invalid XML characters are rejected. Use escaped URLs for spaces and other reserved characters. Query-string ampersands are escaped by the XML encoder.

`LastModified` is optional. Set it to the page's actual modification time; zero omits `<lastmod>`. Dates are emitted in UTC. A sitemap must contain 1–50,000 entries and fit within 50 MiB. Sitemap indexes, automatic splitting, alternate-language links, and automatic route discovery are not included in this increment. Serve the sitemap at the site root so its scope includes all listed pages.

## Generate RSS and Atom {#feeds}

```go
func mountFeeds(app *hyperchi.HyperChi) error {
    updated := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
    feed := publishing.Feed{
        Title: "Example updates",
        Description: "New articles and release notes",
        URL: "https://example.com/",
        FeedURL: "https://example.com/rss.xml",
        Author: "Example team",
        Updated: updated,
        Entries: []publishing.Entry{
            {
                Title: "Our first release",
                URL: "https://example.com/posts/first-release",
                Summary: "What shipped and how to get started.",
                Updated: updated,
            },
        },
    }
    rss, err := feed.RSS()
    if err != nil {
        return err
    }
    feed.FeedURL = "https://example.com/atom.xml"
    atom, err := feed.Atom()
    if err != nil {
        return err
    }
    app.Get("/rss.xml", hyperchi.WrapHTTP(rss.Handler()))
    app.Get("/atom.xml", hyperchi.WrapHTTP(atom.Handler()))
    return nil
}
```

Both formats require a title, description, author, website URL, feed URL, and update time. Each entry requires a title, unique canonical URL, and update time. Entry URLs become stable RSS GUIDs and Atom IDs. Preserve those URLs across edits. Entries retain the order you supply; sort your selected collection items newest first if that is the desired reading order. Empty feeds are supported.

`Published` is optional. RSS uses it for `pubDate`, falling back to `Updated`; Atom omits `<published>` when absent. Entry update times must not exceed the feed update time, and publication must not follow the entry's update time. Atom inherits the feed author. RSS omits author because its author field expects an email address rather than a display name.

Titles, descriptions, and summaries are plain text, escaped as XML. Rich HTML, enclosures, custom IDs, and per-entry authors are not part of this initial API. Include discovery links in your HTML layout:

```html
<link rel="alternate" type="application/rss+xml" title="Updates" href="/rss.xml">
<link rel="alternate" type="application/atom+xml" title="Updates" href="/atom.xml">
```

## Use collections and static builds {#builds}

Query your [content collection](/docs/content), exclude unpublished items, and map the result to `SitemapEntry` or `Entry`. Use your configured public site URL to construct links. The generators do not publish everything in a collection automatically.

For a static build, write the same document alongside your generated pages:

```go
if err := os.WriteFile("dist/sitemap.xml", sitemap.Bytes(), 0644); err != nil {
    return err
}
```

Create the destination directory first. `Bytes()` returns a copy; changing it cannot alter an already registered handler. Sitemap and feed generation can run alongside `PreRender` during startup without making an HTTP request or rendering an XML template.

Format references: [Sitemaps protocol](https://www.sitemaps.org/protocol.html), [RSS 2.0](https://www.rssboard.org/rss-specification), and [Atom RFC 4287](https://www.rfc-editor.org/rfc/rfc4287).

## Keep going {#next}

The [developer toolbar](/docs/devtools) inspects local requests, routes, and DOM elements. For build-time HTML snapshots, see [templates](/docs/templates).

For translated pages, use `SitemapEntry.Alternates` and `Feed.Language`. The [internationalization guide](/docs/i18n#publishing) shows reciprocal language links and per-language feeds.
