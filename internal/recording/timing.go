package recording

import (
	"math"
	"sort"
)

func Times(r *Recording, idle int64) ([]int64, []int64) {
	original, presented := make([]int64, r.Count()+1), make([]int64, r.Count()+1)
	for i := 0; i < r.Count(); i++ {
		delay := int64(0)
		if r.Source != nil {
			delay = r.Source.Times[i+1] - r.Source.Times[i]
		} else {
			delay = r.Lines[i].Time
		}
		original[i+1] = original[i] + delay
		if idle > 0 {
			delay = min(delay, idle)
		}
		presented[i+1] = presented[i] + delay
	}
	return original, presented
}
func MapTime(value float64, from, to []int64) float64 {
	value = math.Max(0, math.Min(float64(from[len(from)-1]), value))
	i := sort.Search(len(from), func(i int) bool { return float64(from[i]) > value }) - 1
	if i >= len(from)-1 {
		return float64(to[len(to)-1])
	}
	return float64(to[i]) + (value-float64(from[i]))/float64(from[i+1]-from[i])*float64(to[i+1]-to[i])
}
