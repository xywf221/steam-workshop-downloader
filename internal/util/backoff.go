package util

// MaxAutoJobs caps auto-parallel file downloads within one workshop item.
const MaxAutoJobs = 8

// ComputeBackoff returns exponential-ish retry delay in seconds: 1, 2, 4, 8, ...
// clamped at cap (default callers use 30). attempt is 1-based.
func ComputeBackoff(attempt, cap int) int {
	if attempt < 1 {
		attempt = 1
	}
	if cap < 1 {
		cap = 1
	}
	// 2^(attempt-1)
	v := 1
	for i := 1; i < attempt; i++ {
		if v > cap/2 {
			return cap
		}
		v *= 2
	}
	if v > cap {
		return cap
	}
	return v
}

// SuggestJobs picks a worker count for downloading nFiles of one item.
// 0/1 file → 1; few files → one worker each; many → soft-cap at maxJobs.
func SuggestJobs(nFiles, maxJobs int) int {
	cap := maxJobs
	if cap < 1 {
		cap = 1
	}
	n := nFiles
	if n < 0 {
		n = 0
	}
	if n <= 1 {
		return 1
	}
	if n < cap {
		return n
	}
	return cap
}
