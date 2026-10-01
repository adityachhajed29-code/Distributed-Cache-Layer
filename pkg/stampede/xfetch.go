package stampede

import (
	"math"
	"time"
)

// Refresh implements X-Fetch using observed recomputation time and uniform U.
func Refresh(remaining, computation time.Duration, u float64) bool {
	if remaining <= 0 {
		return true
	}
	if computation <= 0 || u <= 0 || u >= 1 {
		return false
	}
	return -float64(computation)*math.Log(u) > float64(remaining)
}
