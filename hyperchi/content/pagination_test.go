package content

import "testing"

// Collection.Query must return cleanly, not panic, for an empty filtered set
// with no limit (pageSize == 0, a division by zero) and for a negative offset
// (an out-of-bounds slice index).

func TestQueryEmptyCollectionNoLimit(t *testing.T) {
	c := &Collection{} // nil Contents → empty

	res, err := c.Query(ContentQuery{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Total != 0 || len(res.Contents) != 0 {
		t.Fatalf("expected empty result, got total=%d contents=%d", res.Total, len(res.Contents))
	}
	if res.TotalPages != 0 {
		t.Fatalf("TotalPages = %d, want 0", res.TotalPages)
	}
}

func TestQueryNegativeOffsetDoesNotPanic(t *testing.T) {
	c := &Collection{}

	res, err := c.Query(ContentQuery{Limit: 10, Offset: -5})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Contents) != 0 {
		t.Fatalf("expected no contents, got %d", len(res.Contents))
	}
}
