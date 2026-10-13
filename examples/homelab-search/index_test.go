package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIndexTypesTagsAndLimits(t *testing.T) {
	s := testSite(t, map[string]string{
		"runbooks/net/dns.md":     "---\ntitle: DNS runbook\ntags: unbound, Split DNS\n---\n\n# Ignored heading\n\nRestart unbound.\n",
		"notes/yaml-list.md":      "---\ntags: [backup, restic]\nauthor: [a, b]\n---\n\n# Restore notes\n\nrestic restore steps\n",
		"notes/untitled.md":       "no heading here, just words about zfs\n",
		"configs/proxy.conf":      "server { listen 443; }\n",
		"configs/Dockerfile":      "FROM alpine:3.24\n",
		"scripts/backup.sh":       "#!/bin/sh\nrestic backup /srv\n",
		"notes/plain.txt":         "plain words\n",
		"manuals/switch.pdf":      "%PDF-1.7 not really",
		"notes/fake.txt":          "text with a \x00 NUL byte",
		"notes/latin1.txt":        "caf\xe9",
		"notes/big.txt":           strings.Repeat("big ", 40),
		".git/config":             "[core]\n",
		"notes/.secret.md":        "# hidden\n",
		"notes/.cache/ignored.md": "# hidden dir\n",
	}, func(o *options) { o.MaxFileBytes = 100 })
	ctx := context.Background()

	cases := []struct {
		path, typ, title, reason string
		tags                     []string
	}{
		{"runbooks/net/dns.md", TypeMarkdown, "DNS runbook", "", []string{"unbound", "split", "dns", "runbooks", "net"}},
		{"notes/yaml-list.md", TypeMarkdown, "Restore notes", "", []string{"backup", "restic", "notes"}},
		{"notes/untitled.md", TypeMarkdown, "untitled.md", "", []string{"notes"}},
		{"configs/proxy.conf", TypeConfig, "proxy.conf", "", []string{"configs"}},
		{"configs/Dockerfile", TypeConfig, "Dockerfile", "", []string{"configs"}},
		{"scripts/backup.sh", TypeScript, "backup.sh", "", []string{"scripts"}},
		{"notes/plain.txt", TypeText, "plain.txt", "", []string{"notes"}},
		{"manuals/switch.pdf", TypeOther, "switch.pdf", reasonUnsupported, []string{"manuals"}},
		{"notes/fake.txt", TypeText, "fake.txt", reasonBinary, []string{"notes"}},
		{"notes/latin1.txt", TypeText, "latin1.txt", reasonBinary, []string{"notes"}},
		{"notes/big.txt", TypeText, "big.txt", reasonTooLarge, []string{"notes"}},
	}
	for _, c := range cases {
		d, err := s.ix.Lookup(ctx, c.path)
		if err != nil {
			t.Errorf("%s: %v", c.path, err)
			continue
		}
		if d.Type != c.typ || d.Title != c.title || d.Reason != c.reason || d.Searchable != (c.reason == "") {
			t.Errorf("%s: got type=%s title=%q reason=%q searchable=%v", c.path, d.Type, d.Title, d.Reason, d.Searchable)
		}
		if !slices.Equal(d.Tags, c.tags) {
			t.Errorf("%s: tags %v, want %v", c.path, d.Tags, c.tags)
		}
	}
	for _, hidden := range []string{".git/config", "notes/.secret.md", "notes/.cache/ignored.md"} {
		if _, err := s.ix.Lookup(ctx, hidden); err != errNotIndexed {
			t.Errorf("hidden %s: got %v, want errNotIndexed", hidden, err)
		}
	}
	st := s.ix.Status()
	if st.Files != len(cases) || st.Indexed != 7 || st.Skipped() != 4 {
		t.Errorf("status files=%d indexed=%d skipped=%d", st.Files, st.Indexed, st.Skipped())
	}

	// Listed-only files are found by name and folder, never by contents.
	if got := hitPaths(search(t, s, SearchParams{Query: "switch "})); !slices.Equal(got, []string{"manuals/switch.pdf"}) {
		t.Errorf("binary by name: %v", got)
	}
	if got := hitPaths(search(t, s, SearchParams{Query: "NUL "})); len(got) != 0 {
		t.Errorf("binary contents were indexed: %v", got)
	}
	if got := hitPaths(search(t, s, SearchParams{Query: "zfs "})); !slices.Equal(got, []string{"notes/untitled.md"}) {
		t.Errorf("markdown body: %v", got)
	}
}

func TestSymlinkOutOfLibraryIsNotIndexed(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testSite(t, map[string]string{"ok.txt": "fine"})
	if err := os.Symlink(outside, filepath.Join(s.ix.dir, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := s.ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ix.Lookup(context.Background(), "leak.txt"); err != errNotIndexed {
		t.Errorf("leak.txt: %v, want errNotIndexed", err)
	}
	if _, err := s.ix.readLimited("leak.txt", 1<<20); err == nil {
		t.Error("reading a symlink out of the library succeeded")
	}
	if errs := s.ix.Status().Errors; len(errs) == 0 || !strings.Contains(errs[0], "leak.txt") {
		t.Errorf("status errors %v should report leak.txt", errs)
	}
	if got := hitPaths(search(t, s, SearchParams{Query: "TOPSECRET"})); len(got) != 0 {
		t.Errorf("found %v", got)
	}
}

func TestRankingPrefersTitleMatches(t *testing.T) {
	s := testSite(t, map[string]string{
		"a-body.md":  "# Unrelated\n\nOne line that mentions tailscale in passing among many other words here.\n",
		"b-title.md": "# Tailscale exit node\n\nSetup steps.\n",
		"c-none.md":  "# Nothing\n\nNo match.\n",
	})
	got := hitPaths(search(t, s, SearchParams{Query: "tailscale "}))
	if !slices.Equal(got, []string{"b-title.md", "a-body.md"}) {
		t.Errorf("ranking %v", got)
	}
}

func TestSnippetsAndTitlesAreEscaped(t *testing.T) {
	s := testSite(t, map[string]string{
		"xss.txt": "before <script>alert('owned')</script> after & more",
		"t.md":    "---\ntitle: <b>bold</b> alert\n---\n\nbody\n",
	})
	res := search(t, s, SearchParams{Query: "alert "})
	if len(res.Hits) != 2 {
		t.Fatalf("hits %v", hitPaths(res))
	}
	for _, h := range res.Hits {
		for _, frag := range []string{string(h.Snippet), string(h.TitleHTML)} {
			if strings.Contains(frag, "<script") || strings.Contains(frag, "<b>") {
				t.Errorf("%s: unescaped markup in %q", h.Path, frag)
			}
		}
	}
	byPath := map[string]Hit{}
	for _, h := range res.Hits {
		byPath[h.Path] = h
	}
	if snip := string(byPath["xss.txt"].Snippet); !strings.Contains(snip, "&lt;script&gt;<mark>alert</mark>(&#39;owned&#39;)&lt;/script&gt;") || !strings.Contains(snip, "&amp; more") {
		t.Errorf("snippet %q", snip)
	}
	if title := string(byPath["t.md"].TitleHTML); title != "&lt;b&gt;bold&lt;/b&gt; <mark>alert</mark>" {
		t.Errorf("title %q", title)
	}
}

func TestMarkHTMLIsBalanced(t *testing.T) {
	for in, want := range map[string]string{
		"a\x01b\x02c":   "a<mark>b</mark>c",
		"\x02stray":     "stray",
		"open\x01<i>":   "open<mark>&lt;i&gt;</mark>",
		"\x01\x01x\x02": "<mark>x</mark>",
	} {
		if got := string(markHTML(in)); got != want {
			t.Errorf("markHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilters(t *testing.T) {
	s := testSite(t, map[string]string{
		"runbooks/proxy.md":  "---\ntags: nginx\n---\n\n# Proxy\n\nreload nginx\n",
		"configs/nginx.conf": "# nginx config\nserver {}\n",
		"notes/other.md":     "# Other\n\nnginx mentioned\n",
	})
	cases := []struct {
		p    SearchParams
		want []string
	}{
		{SearchParams{Query: "nginx ", Type: TypeConfig}, []string{"configs/nginx.conf"}},
		{SearchParams{Query: "nginx ", Tag: "nginx"}, []string{"runbooks/proxy.md"}},
		{SearchParams{Query: "nginx ", Tag: "notes"}, []string{"notes/other.md"}},
		{SearchParams{Query: "nginx ", Type: TypeMarkdown, Tag: "configs"}, nil},
		{SearchParams{Type: TypeConfig}, []string{"configs/nginx.conf"}},
		{SearchParams{Tag: "runbooks"}, []string{"runbooks/proxy.md"}},
	}
	for _, c := range cases {
		if got := hitPaths(search(t, s, c.p)); !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.p, got, c.want)
		}
	}
	types, tags, err := s.ix.Facets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0] != (Facet{TypeConfig, 1}) || types[1] != (Facet{TypeMarkdown, 2}) {
		t.Errorf("types %v", types)
	}
	if len(tags) != 4 {
		t.Errorf("tags %v", tags)
	}
}

func TestSyncRemovesDeletedFiles(t *testing.T) {
	s := testSite(t, map[string]string{"keep.md": "# Keep\n", "gone.md": "# Gone\n"})
	if err := os.Remove(filepath.Join(s.ix.dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	if err := s.ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ix.Lookup(context.Background(), "gone.md"); err != errNotIndexed {
		t.Errorf("gone.md still indexed: %v", err)
	}
	if st := s.ix.Status(); st.Files != 1 || st.Changed != 1 || st.LastPass != "full scan" {
		t.Errorf("status %+v", st)
	}
}

func TestSchemaVersionMismatchRebuilds(t *testing.T) {
	s := testSite(t, map[string]string{"a.md": "# A\n"})
	if _, err := s.app.DB.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := s.ix.migrate(); err != nil {
		t.Fatal(err)
	}
	if st, err := s.ix.Search(context.Background(), SearchParams{}); err != nil || len(st.Hits) != 0 {
		t.Fatalf("after rebuild: %v %v", st.Hits, err)
	}
	if err := s.ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hitPaths(search(t, s, SearchParams{})); !slices.Equal(got, []string{"a.md"}) {
		t.Errorf("after resync: %v", got)
	}
}
