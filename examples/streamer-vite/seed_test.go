package main

import (
	"strings"
	"testing"
)

func TestBuiltInSeedDataDecodes(t *testing.T) {
	podcasts, err := getSeedData()
	if err != nil {
		t.Fatal(err)
	}
	if len(podcasts) == 0 {
		t.Fatal("empty catalog")
	}
	for _, p := range podcasts {
		if p.Title == "" || len(p.Topics) == 0 || len(p.ListenOn) == 0 || len(p.Transcript) == 0 {
			t.Fatalf("podcast %d decoded with empty fields: %+v", p.ID, p)
		}
	}
}

func TestSeedRejectsMalformedListField(t *testing.T) {
	entry := map[string]any{
		"date": "2025-01-01", "title": "Broken", "category": "Test", "duration": "1h",
		"image_path": "/x.png", "description": "d",
		"topics": `["unterminated"`,
	}
	_, err := decodeSeed([]map[string]any{entry})
	if err == nil || !strings.Contains(err.Error(), "topics") || !strings.Contains(err.Error(), "seed podcast 1") {
		t.Fatalf("error = %v, want one naming podcast 1 and its topics field", err)
	}
}

func TestSeedRejectsMissingTextField(t *testing.T) {
	_, err := decodeSeed([]map[string]any{{"title": "No date"}})
	if err == nil {
		t.Fatal("an entry without a date was accepted")
	}
}
