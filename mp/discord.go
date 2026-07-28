package mp

const defaultWindow = 8

const MaxPoints = 100_000

type Options struct {
	Window        int
	Spread        bool
	FlatAsDiscord bool
	Workers       int
	Serial        bool
	MaxPoints     int
}

func (o Options) limit() int {
	if o.MaxPoints > 0 {
		return o.MaxPoints
	}
	return MaxPoints
}

func AutoWindow(n int) int {
	m := defaultWindow
	if 2*m > n {
		m = n / 2
	}
	if m < 2 {
		return 0
	}
	return m
}

func Scores(t []float64, opt Options) []float64 {
	n := len(t)
	out := make([]float64, n)
	m := opt.Window
	if m <= 0 {
		m = AutoWindow(n)
	}
	if m < 2 || n < 2*m || n > opt.limit() {
		return out
	}
	var prof []float64
	if opt.Serial {
		prof = stomp(t, m, !opt.FlatAsDiscord)
	} else {
		prof = scamp(t, m, !opt.FlatAsDiscord, opt.Workers)
	}
	if prof == nil {
		return out
	}
	if opt.Spread {
		return PointScores(prof, n, m)
	}
	copy(out, prof)
	return out
}
