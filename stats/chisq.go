package stats

import "math"

func ChiSquareTail(x float64, dof int) float64 {
	if dof <= 0 || x <= 0 {
		return 1
	}
	return gammaQ(float64(dof)/2, x/2)
}

func gammaQ(a, x float64) float64 {
	if x <= 0 || a <= 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaCF(a, x)
}

func gammaSeries(a, x float64) float64 {
	gln, _ := math.Lgamma(a)
	ap := a
	sum := 1 / a
	del := sum
	for i := 0; i < 200; i++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-15 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-gln)
}

func gammaCF(a, x float64) float64 {
	gln, _ := math.Lgamma(a)
	const tiny = 1e-300
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 200; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-gln) * h
}
