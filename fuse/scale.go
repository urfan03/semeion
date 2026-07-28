package fuse

type ScaleFunc func(window int) []float64

func Scales(base, count int) []int {
	if base < 2 {
		base = 2
	}
	if count < 1 {
		count = 1
	}
	out := make([]int, 0, count)
	w := base
	for i := 0; i < count; i++ {
		out = append(out, w)
		w *= 2
	}
	return out
}

func MultiScale(windows []int, warmup int, fn ScaleFunc) [][]float64 {
	streams := make([][]float64, 0, len(windows))
	n := -1
	for _, w := range windows {
		sc := fn(w)
		if sc == nil {
			continue
		}
		if n < 0 {
			n = len(sc)
		}
		if len(sc) != n {
			continue
		}
		streams = append(streams, PValues(sc, warmup))
	}
	return streams
}

func MultiScaleAgree(windows []int, warmup, k int, fn ScaleFunc) []float64 {
	streams := MultiScale(windows, warmup, fn)
	if len(streams) == 0 {
		return nil
	}
	if k > len(streams) {
		k = len(streams)
	}
	return AgreeStreams(streams, k)
}
