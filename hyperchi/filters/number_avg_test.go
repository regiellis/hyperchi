package filters

import "testing"

// avg must average the numeric values only, the way sum adds only them.
func TestAvgSkipsNonNumeric(t *testing.T) {
	cases := []struct {
		name   string
		values []any
		want   float64
	}{
		{"all numeric", []any{2, 4}, 3},
		{"non-numeric ignored", []any{"n/a", 2, 4}, 3},
		{"no numbers", []any{"a", "b"}, 0},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		if got := avg(tc.values...); got != tc.want {
			t.Errorf("%s: avg(%v) = %v, want %v", tc.name, tc.values, got, tc.want)
		}
	}
}
