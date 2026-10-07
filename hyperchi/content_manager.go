package hyperchi

import (
	"cmp"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/validator"
)

// ContentManager (app.Content) holds content collections in memory, caches
// items by slug, and emits content:* events on the app's EventBus. It is safe
// for concurrent use. Items come from files through LoadDir (a directory,
// reloaded on change in development) or LoadFS (an embedded tree), or are
// added one at a time with Create.
type ContentManager struct {
	// mu guards collections, owners, cache, and each collection's Items
	// slice. Get runs in concurrent handlers and writes the cache on a miss,
	// so an unguarded read is a "concurrent map writes" crash, and changing
	// Items while Get ranges over it is a data race.
	mu           sync.RWMutex
	collections  map[string]*ContentCollection
	owners       map[string]*contentSource // collection name to the load that owns it
	cache        map[contentKey]*ContentItem
	events       *EventBus
	cacheManager *CacheManager
	app          *HyperChi // for development mode, the logger, and the lifecycle; nil in a bare manager

	// watchMu guards the development watchers LoadDir starts.
	watchMu         sync.Mutex
	watchers        map[string]*contentSource // by absolute directory
	watchRegistered bool                      // closeWatchers is registered with the app
	watchClosed     bool                      // the app closed; no new watchers
}

// ContentItem is one item in a ContentManager collection.
type ContentItem struct {
	Path        string         `json:"path"`
	Slug        string         `json:"slug"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Content     string         `json:"content"`
	HTML        template.HTML  `json:"html"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

// ContentCollection is a named set of ContentItems. A non-nil Schema validates
// each item's Metadata in Create.
type ContentCollection struct {
	Name    string            `json:"name"`
	Path    string            `json:"path"`
	Schema  *validator.Schema `json:"-"`
	Items   []*ContentItem    `json:"items"`
	SortBy  string            `json:"sort_by"`
	SortDir string            `json:"sort_dir"`
}

// newContentManager returns an empty manager that records cache hits and
// misses on cacheManager and emits events on eventBus.
func newContentManager(cacheManager *CacheManager, eventBus *EventBus) *ContentManager {
	return &ContentManager{
		collections:  make(map[string]*ContentCollection),
		owners:       make(map[string]*contentSource),
		cache:        make(map[contentKey]*ContentItem),
		events:       eventBus,
		cacheManager: cacheManager,
	}
}

// contentKey identifies a cached item. A struct key cannot collide the way a
// joined "collection:slug" string can when either part contains the separator.
type contentKey struct{ collection, slug string }

// String is the key's form in stats and events.
func (k contentKey) String() string { return k.collection + ":" + k.slug }

// Collection defines (or replaces, emptying it) the collection name. A non-nil
// schema validates each item's Metadata in Create. Replacing a collection drops
// its cached items, so Get never returns one from the old collection.
func (cm *ContentManager) Collection(name string, path string, schema *validator.Schema) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	maps.DeleteFunc(cm.cache, func(key contentKey, _ *ContentItem) bool {
		return key.collection == name
	})
	cm.collections[name] = &ContentCollection{
		Name:   name,
		Path:   path,
		Schema: schema,
		Items:  []*ContentItem{},
	}
}

// collectionCount returns the number of registered collections.
func (cm *ContentManager) collectionCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.collections)
}

// Get returns the item with slug in collection, caching it. It fails when the
// collection or the item does not exist.
func (cm *ContentManager) Get(collection, slug string) (*ContentItem, error) {
	cacheKey := contentKey{collection, slug}

	// Fast path: cache hit under a read lock.
	cm.mu.RLock()
	if item, exists := cm.cache[cacheKey]; exists {
		cm.mu.RUnlock()
		cm.cacheManager.RecordHit(cacheKey.String())
		return item, nil
	}
	cm.mu.RUnlock()

	cm.cacheManager.RecordMiss(cacheKey.String())

	// RWMutex has no atomic upgrade: re-check the cache under the write lock in
	// case another goroutine filled it in between.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if item, exists := cm.cache[cacheKey]; exists {
		return item, nil
	}

	col, exists := cm.collections[collection]
	if !exists {
		return nil, fmt.Errorf("collection %s not found", collection)
	}

	for _, item := range col.Items {
		if item.Slug == slug {
			cm.cache[cacheKey] = item
			return item, nil
		}
	}

	return nil, fmt.Errorf("content %s not found in collection %s", slug, collection)
}

// Query starts a query over collection's items.
func (cm *ContentManager) Query(collection string) *ContentQuery {
	return &ContentQuery{
		manager:    cm,
		collection: collection,
		filters:    make(map[string]any),
	}
}

// Create adds item to collection. When the collection has a schema, item's
// Metadata must pass it or Create returns a 422 *HTTPError with per-field
// messages. An unknown collection or a duplicate slug is an error. It emits
// content:creating, content:created, and content:file:changed.
func (cm *ContentManager) Create(collection string, item *ContentItem) error {
	cm.events.Emit("content:creating", map[string]any{
		"collection": collection,
		"slug":       item.Slug,
		"title":      item.Title,
	})

	cm.mu.Lock()
	col, exists := cm.collections[collection]
	if !exists {
		cm.mu.Unlock()
		return fmt.Errorf("collection %s not found", collection)
	}

	if col.Schema != nil {
		if result := col.Schema.Validate(item.Metadata); !result.Valid {
			cm.mu.Unlock()
			return Unprocessable("The content does not match its collection schema.", fieldMessages(result.Errors))
		}
	}

	for _, existing := range col.Items {
		if existing.Slug == item.Slug {
			cm.mu.Unlock()
			return fmt.Errorf("content with slug %s already exists", item.Slug)
		}
	}

	col.Items = append(col.Items, item)

	cacheKey := contentKey{collection, item.Slug}
	cm.cache[cacheKey] = item
	cm.mu.Unlock()

	cm.events.Emit("content:created", map[string]any{
		"collection": collection,
		"item":       item,
		"cacheKey":   cacheKey.String(),
	})

	cm.events.Emit("content:file:changed", map[string]any{
		"type":       "created",
		"path":       item.Path,
		"collection": collection,
		"contentId":  item.Slug,
		"timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
	})

	return nil
}

// Update changes an item in place. It applies the "title", "description", and
// "content" keys (strings) and "metadata" (map[string]any) from updates and
// ignores the rest; metadata is not checked against the schema. A missing
// collection or item is an error. It emits content:updating, content:updated,
// and content:file:changed.
func (cm *ContentManager) Update(collection, slug string, updates map[string]any) error {
	cm.events.Emit("content:updating", map[string]any{
		"collection": collection,
		"slug":       slug,
		"updates":    updates,
	})

	cm.mu.Lock()
	col, exists := cm.collections[collection]
	if !exists {
		cm.mu.Unlock()
		return fmt.Errorf("collection %s not found", collection)
	}

	var updatedItem *ContentItem
	for _, item := range col.Items {
		if item.Slug == slug {
			if title, ok := updates["title"].(string); ok {
				item.Title = title
			}
			if description, ok := updates["description"].(string); ok {
				item.Description = description
			}
			if content, ok := updates["content"].(string); ok {
				item.Content = content
			}
			if metadata, ok := updates["metadata"].(map[string]any); ok {
				item.Metadata = metadata
			}

			updatedItem = item
			break
		}
	}

	if updatedItem == nil {
		cm.mu.Unlock()
		return fmt.Errorf("content %s not found in collection %s", slug, collection)
	}

	cacheKey := contentKey{collection, slug}
	cm.cache[cacheKey] = updatedItem
	cm.mu.Unlock()

	cm.events.Emit("content:updated", map[string]any{
		"collection": collection,
		"slug":       slug,
		"item":       updatedItem,
		"updates":    updates,
	})

	cm.events.Emit("content:file:changed", map[string]any{
		"type":       "updated",
		"path":       updatedItem.Path,
		"collection": collection,
		"contentId":  slug,
		"timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
	})

	return nil
}

// Delete removes an item and its cache entry. A missing collection or item is
// an error. It emits content:deleting, content:deleted, and
// content:file:changed.
func (cm *ContentManager) Delete(collection, slug string) error {
	cm.events.Emit("content:deleting", map[string]any{
		"collection": collection,
		"slug":       slug,
	})

	cm.mu.Lock()
	col, exists := cm.collections[collection]
	if !exists {
		cm.mu.Unlock()
		return fmt.Errorf("collection %s not found", collection)
	}

	var deletedItem *ContentItem
	newItems := make([]*ContentItem, 0, len(col.Items))
	for _, item := range col.Items {
		if item.Slug != slug {
			newItems = append(newItems, item)
		} else {
			deletedItem = item
		}
	}

	if deletedItem == nil {
		cm.mu.Unlock()
		return fmt.Errorf("content %s not found in collection %s", slug, collection)
	}

	col.Items = newItems

	cacheKey := contentKey{collection, slug}
	delete(cm.cache, cacheKey)
	cm.mu.Unlock()

	cm.events.Emit("content:deleted", map[string]any{
		"collection":  collection,
		"slug":        slug,
		"deletedItem": deletedItem,
	})

	cm.events.Emit("content:file:changed", map[string]any{
		"type":       "deleted",
		"path":       deletedItem.Path,
		"collection": collection,
		"contentId":  slug,
		"timestamp":  fmt.Sprintf("%d", time.Now().Unix()),
	})

	return nil
}

// ContentQuery filters, sorts, and pages one collection's items. Start one
// with ContentManager.Query.
type ContentQuery struct {
	manager    *ContentManager
	collection string
	filters    map[string]any
	limit      int
	offset     int
	sortBy     string
	sortDir    string
}

// Where keeps items whose Metadata[key] == value.
func (cq *ContentQuery) Where(key string, value any) *ContentQuery {
	cq.filters[key] = value
	return cq
}

// Limit caps the result at n items; n <= 0 means no cap.
func (cq *ContentQuery) Limit(n int) *ContentQuery {
	cq.limit = n
	return cq
}

// Offset skips the first n matching items.
func (cq *ContentQuery) Offset(n int) *ContentQuery {
	cq.offset = n
	return cq
}

// SortBy sorts by Metadata[field]. Two numbers (Go integers and floats,
// json.Number, or strings that parse as finite numbers, so "10" follows "9")
// compare numerically; two times (time.Time, or strings in RFC 3339,
// "2006-01-02 15:04:05", or "2006-01-02" form) compare chronologically; any
// other pair compares as %v text. When kinds differ, numbers come before
// times and times before text. Items without the field come last in either
// direction. direction "desc" sorts descending; anything else ascending. The
// sort is stable, so equal values keep their collection order.
func (cq *ContentQuery) SortBy(field, direction string) *ContentQuery {
	cq.sortBy = field
	cq.sortDir = direction
	return cq
}

// OrderBy is SortBy.
func (cq *ContentQuery) OrderBy(field, direction string) *ContentQuery {
	cq.sortBy = field
	cq.sortDir = direction
	return cq
}

// All runs the query. An unknown collection yields no items and no error.
func (cq *ContentQuery) All() ([]*ContentItem, error) {
	// Snapshot under the read lock so Items is never ranged while
	// Create, Update, or Delete changes it.
	cq.manager.mu.RLock()
	collection, exists := cq.manager.collections[cq.collection]
	if !exists {
		cq.manager.mu.RUnlock()
		return []*ContentItem{}, nil
	}

	var items []*ContentItem
	for _, item := range collection.Items {
		matches := true
		for key, value := range cq.filters {
			if item.Metadata[key] != value {
				matches = false
				break
			}
		}
		if matches {
			items = append(items, item)
		}
	}
	cq.manager.mu.RUnlock()

	if cq.sortBy != "" {
		type keyed struct {
			item *ContentItem
			key  sortKey
		}
		rows := make([]keyed, len(items))
		for i, item := range items {
			rows[i] = keyed{item, newSortKey(item.Metadata[cq.sortBy])}
		}
		desc := cq.sortDir == "desc"
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].key, rows[j].key
			if a.kind == sortMissing || b.kind == sortMissing {
				return b.kind == sortMissing && a.kind != sortMissing
			}
			if desc {
				return compareSortKeys(b, a) < 0
			}
			return compareSortKeys(a, b) < 0
		})
		for i, row := range rows {
			items[i] = row.item
		}
	}

	start := cq.offset
	end := start + cq.limit
	if cq.limit <= 0 {
		end = len(items)
	}
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return items[start:end], nil
}

// First returns the first matching item, or an error when nothing matches.
func (cq *ContentQuery) First() (*ContentItem, error) {
	items, err := cq.Limit(1).All()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no content found")
	}
	return items[0], nil
}

// sortKind ranks metadata values of different kinds against each other.
type sortKind int

const (
	sortNumber sortKind = iota
	sortTime
	sortText
	sortMissing
)

// sortKey is a metadata value classified once, before sorting.
type sortKey struct {
	kind    sortKind
	integer bool // num came from an integer and i holds it exactly
	i       int64
	num     float64
	t       time.Time
	text    string
}

// sortTimeLayouts are the string forms SortBy reads as times.
var sortTimeLayouts = []string{time.RFC3339Nano, time.DateTime, time.DateOnly}

func newSortKey(v any) sortKey {
	switch x := v.(type) {
	case nil:
		return sortKey{kind: sortMissing}
	case int:
		return intSortKey(int64(x))
	case int8:
		return intSortKey(int64(x))
	case int16:
		return intSortKey(int64(x))
	case int32:
		return intSortKey(int64(x))
	case int64:
		return intSortKey(x)
	case uint:
		return uintSortKey(uint64(x))
	case uint8:
		return uintSortKey(uint64(x))
	case uint16:
		return uintSortKey(uint64(x))
	case uint32:
		return uintSortKey(uint64(x))
	case uint64:
		return uintSortKey(x)
	case float32:
		return floatSortKey(float64(x), fmt.Sprint(x))
	case float64:
		return floatSortKey(x, fmt.Sprint(x))
	case json.Number:
		return stringSortKey(string(x))
	case time.Time:
		return sortKey{kind: sortTime, t: x}
	case string:
		return stringSortKey(x)
	}
	return sortKey{kind: sortText, text: fmt.Sprintf("%v", v)}
}

func intSortKey(n int64) sortKey {
	return sortKey{kind: sortNumber, integer: true, i: n, num: float64(n)}
}

func uintSortKey(n uint64) sortKey {
	if n <= math.MaxInt64 {
		return intSortKey(int64(n))
	}
	return sortKey{kind: sortNumber, num: float64(n)}
}

// floatSortKey treats NaN as text, since it has no place in a numeric order.
func floatSortKey(f float64, text string) sortKey {
	if math.IsNaN(f) {
		return sortKey{kind: sortText, text: text}
	}
	return sortKey{kind: sortNumber, num: f}
}

// stringSortKey reads s as an integer, a finite float, or a time, in that
// order, and falls back to text.
func stringSortKey(s string) sortKey {
	trimmed := strings.TrimSpace(s)
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return intSortKey(n)
	}
	if f, err := strconv.ParseFloat(trimmed, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return sortKey{kind: sortNumber, num: f}
	}
	for _, layout := range sortTimeLayouts {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return sortKey{kind: sortTime, t: t}
		}
	}
	return sortKey{kind: sortText, text: s}
}

// compareSortKeys orders two present keys: by kind first, then by value.
func compareSortKeys(a, b sortKey) int {
	if a.kind != b.kind {
		return cmp.Compare(a.kind, b.kind)
	}
	switch a.kind {
	case sortNumber:
		if a.integer && b.integer {
			return cmp.Compare(a.i, b.i)
		}
		return cmp.Compare(a.num, b.num)
	case sortTime:
		return a.t.Compare(b.t)
	}
	return strings.Compare(a.text, b.text)
}
