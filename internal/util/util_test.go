package util

import "testing"

func TestFormatSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{int64(2.5 * 1024 * 1024), "2.5 MB"},
	}
	for _, c := range cases {
		if got := FormatSize(c.n); got != c.want {
			t.Errorf("FormatSize(%d)=%q want %q", c.n, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		s    float64
		want string
	}{
		{0, "0:00:00"},
		{-1, "0:00:00"},
		{65, "0:01:05"},
		{3600 + 2*60 + 3, "1:02:03"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.s); got != c.want {
			t.Errorf("FormatDuration(%v)=%q want %q", c.s, got, c.want)
		}
	}
}

func TestComputeBackoff(t *testing.T) {
	// attempt 1..6 → 1,2,4,8,16,30 (cap 30)
	want := []int{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		got := ComputeBackoff(i+1, 30)
		if got != w {
			t.Errorf("ComputeBackoff(%d,30)=%d want %d", i+1, got, w)
		}
	}
}

func TestSuggestJobs(t *testing.T) {
	cases := []struct {
		n, max, want int
	}{
		{0, 8, 1},
		{1, 8, 1},
		{3, 8, 3},
		{20, 8, 8},
		{5, 0, 1}, // maxJobs clamped to 1 → min(5,1)=1? Wait: cap=1, n=5 → return 1
		{5, 1, 1},
	}
	for _, c := range cases {
		if got := SuggestJobs(c.n, c.max); got != c.want {
			t.Errorf("SuggestJobs(%d,%d)=%d want %d", c.n, c.max, got, c.want)
		}
	}
}
