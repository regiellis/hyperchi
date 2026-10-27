package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

// PodcastStore keeps podcasts in the "podcasts" KV store as JSON under
// podcast:<id>, with category counts cached under stats:podcasts.
type PodcastStore struct {
	app   *hyperchi.HyperChi
	store hyperchi.KVStore
}

// NewPodcastStore returns a store over app's "podcasts" KV store, which must
// already be registered.
func NewPodcastStore(app *hyperchi.HyperChi) *PodcastStore {
	return &PodcastStore{
		app:   app,
		store: app.KV("podcasts"),
	}
}

// Get returns the podcast with id.
func (ps *PodcastStore) Get(id string) (*PodcastResponse, error) {
	key := fmt.Sprintf("podcast:%s", id)

	data, err := ps.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("podcast not found: %w", err)
	}

	var podcast PodcastResponse
	if err := json.Unmarshal(data, &podcast); err != nil {
		return nil, fmt.Errorf("failed to parse podcast: %w", err)
	}

	return &podcast, nil
}

// List returns every podcast, newest first. An entry that cannot be read or
// decoded is logged and skipped.
func (ps *PodcastStore) List() ([]PodcastResponse, error) {
	keys, err := ps.store.Keys("podcast:")
	if err != nil {
		return nil, fmt.Errorf("failed to list podcasts: %w", err)
	}

	podcasts := make([]PodcastResponse, 0, len(keys))

	for _, key := range keys {
		data, err := ps.store.Get(key)
		if err != nil {
			ps.app.Helpers.Logger.Error("Failed to get podcast %s: %v", key, err)
			continue
		}

		var podcast PodcastResponse
		if err := json.Unmarshal(data, &podcast); err != nil {
			ps.app.Helpers.Logger.Error("Failed to parse podcast %s: %v", key, err)
			continue
		}
		podcasts = append(podcasts, podcast)
	}

	// Dates are YYYY-MM-DD, so string order is date order.
	sort.Slice(podcasts, func(i, j int) bool {
		return podcasts[i].Date > podcasts[j].Date
	})

	return podcasts, nil
}

// Search returns the podcasts whose title, category, or description contains
// query, ignoring case. An empty query returns them all.
func (ps *PodcastStore) Search(query string) ([]PodcastResponse, error) {
	if helpers.S(query).IsEmpty() {
		return ps.List()
	}

	query = strings.ToLower(strings.TrimSpace(query))
	podcasts, err := ps.List()
	if err != nil {
		return nil, err
	}

	matches := make([]PodcastResponse, 0)
	for _, podcast := range podcasts {
		title := strings.ToLower(podcast.Title)
		category := strings.ToLower(podcast.Category)
		description := strings.ToLower(podcast.Description)

		if strings.Contains(title, query) ||
			strings.Contains(category, query) ||
			strings.Contains(description, query) {
			matches = append(matches, podcast)
		}
	}

	return matches, nil
}

// Stats returns the total and per-category counts, from the cache when it
// holds them. Set, Delete, and Clear invalidate the cache.
func (ps *PodcastStore) Stats() (*helpers.JSON, error) {
	data, err := ps.store.Get("stats:podcasts")
	if err == nil {
		var stats helpers.JSON
		if err := json.Unmarshal(data, &stats); err == nil {
			return &stats, nil
		}
	}

	podcasts, err := ps.List()
	if err != nil {
		return nil, err
	}

	categoryCount := make(map[string]int)
	for _, podcast := range podcasts {
		categoryCount[podcast.Category]++
	}

	stats := helpers.NewJSON().
		Set("total", len(podcasts)).
		Set("by_category", categoryCount)

	if statsData, err := json.Marshal(stats); err == nil {
		if err := ps.store.Set("stats:podcasts", statsData); err != nil {
			ps.app.Helpers.Logger.Error("Failed to cache stats: %v", err)
		}
	}

	return &stats, nil
}

// InvalidateStats drops the cached counts.
func (ps *PodcastStore) InvalidateStats() {
	ps.store.Delete("stats:podcasts")
}

// Set stores podcast under id, replacing any podcast already there.
func (ps *PodcastStore) Set(id string, podcast *PodcastResponse) error {
	key := fmt.Sprintf("podcast:%s", id)

	data, err := json.Marshal(podcast)
	if err != nil {
		return fmt.Errorf("failed to marshal podcast: %w", err)
	}

	if err := ps.store.Set(key, data); err != nil {
		return fmt.Errorf("failed to store podcast: %w", err)
	}

	ps.InvalidateStats()

	return nil
}

// Delete removes the podcast with id.
func (ps *PodcastStore) Delete(id string) error {
	key := fmt.Sprintf("podcast:%s", id)

	if err := ps.store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete podcast: %w", err)
	}

	ps.InvalidateStats()

	return nil
}

// Count returns how many podcasts are stored.
func (ps *PodcastStore) Count() (int, error) {
	keys, err := ps.store.Keys("podcast:")
	if err != nil {
		return 0, err
	}
	return len(keys), nil
}

// Seed stores podcasts under their IDs, stopping at the first error.
func (ps *PodcastStore) Seed(podcasts []PodcastResponse) error {
	ps.app.Helpers.Logger.Info("💾 Seeding KV store with podcast data...")

	for _, podcast := range podcasts {
		if err := ps.Set(fmt.Sprintf("%d", podcast.ID), &podcast); err != nil {
			return fmt.Errorf("failed to seed podcast %d: %w", podcast.ID, err)
		}
	}

	ps.app.Helpers.Logger.Info("✅ Seeded %d podcasts into KV store", len(podcasts))
	return nil
}

// Clear deletes every podcast. A failed delete is logged, not returned.
func (ps *PodcastStore) Clear() error {
	keys, err := ps.store.Keys("podcast:")
	if err != nil {
		return err
	}

	for _, key := range keys {
		if err := ps.store.Delete(key); err != nil {
			ps.app.Helpers.Logger.Error("Failed to delete %s: %v", key, err)
		}
	}

	ps.InvalidateStats()

	return nil
}
