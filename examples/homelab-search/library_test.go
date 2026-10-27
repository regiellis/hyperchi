package main

import (
	"slices"
	"testing"
)

func TestClassify(t *testing.T) {
	for path, want := range map[string]string{
		"a/b.md": TypeMarkdown, "README.MARKDOWN": TypeMarkdown, "x.txt": TypeText, "rules.csv": TypeText,
		"nginx.conf": TypeConfig, "compose.YML": TypeConfig, "unit.service": TypeConfig, "srv/Caddyfile": TypeConfig,
		"etc/hosts": TypeConfig, "run.sh": TypeScript, "tool.py": TypeScript,
		"manual.pdf": TypeOther, "photo.jpg": TypeOther, "noext": TypeOther, "page.html": TypeOther,
	} {
		if got := classify(path); got != want {
			t.Errorf("classify(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestTags(t *testing.T) {
	if got := pathTags("Home Lab/Network_Gear/switch.md"); !slices.Equal(got, []string{"home-lab", "network_gear"}) {
		t.Errorf("pathTags: %v", got)
	}
	if got := pathTags("top.md"); got != nil {
		t.Errorf("pathTags at the root: %v", got)
	}
	if got := splitTags(` [Backup, "restic" , 'ZFS'] `); !slices.Equal(got, []string{"backup", "restic", "zfs"}) {
		t.Errorf("splitTags list: %v", got)
	}
	for in, want := range map[string]string{"  NGINX ": "nginx", "<script>": "script", "..": "", "-x-": "x", "日本": ""} {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mergeTags([]string{"a", "b"}, []string{"b", "c"}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("mergeTags: %v", got)
	}
}

func TestCleanTextStripsMarkers(t *testing.T) {
	if got := cleanText("a\x01b\x02c\td\ne\x1b"); got != "abc\td\ne" {
		t.Errorf("cleanText = %q", got)
	}
}
