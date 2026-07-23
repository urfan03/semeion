package detect

import (
	"math"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

type GeoModel struct {
	sx, sy, sz float64
	n          int
	dist       *Model
}

func NewGeoModel() *GeoModel {
	return &GeoModel{dist: NewModel(jobspec.SideHigh)}
}

func (m *GeoModel) Observe(lat, lon float64) (prob, score, typicalKm float64, dir core.Direction) {
	if m.n == 0 {
		m.add(lat, lon)
		return 1, 0, 0, core.DirUp
	}
	clat, clon := m.centroid()
	d := haversineKm(lat, lon, clat, clon)
	prob, score, typicalKm, _ = m.dist.Observe(d)
	m.add(lat, lon)
	return prob, score, typicalKm, core.DirUp
}

func (m *GeoModel) add(lat, lon float64) {
	phi, lam := rad(lat), rad(lon)
	m.sx += math.Cos(phi) * math.Cos(lam)
	m.sy += math.Cos(phi) * math.Sin(lam)
	m.sz += math.Sin(phi)
	m.n++
}

func (m *GeoModel) centroid() (lat, lon float64) {
	x, y, z := m.sx/float64(m.n), m.sy/float64(m.n), m.sz/float64(m.n)
	lon = math.Atan2(y, x)
	lat = math.Atan2(z, math.Hypot(x, y))
	return deg(lat), deg(lon)
}

func (m *GeoModel) Count() int { return m.n }

func (m *GeoModel) DistanceKm(lat, lon float64) (float64, bool) {
	if m.n == 0 {
		return 0, false
	}
	clat, clon := m.centroid()
	return haversineKm(lat, lon, clat, clon), true
}

func (m *GeoModel) Bounds(z float64) (lower, upper float64) {
	lo, up := m.dist.Bounds(z)
	if lo < 0 {
		lo = 0
	}
	return lo, up
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0088
	p1, p2 := rad(lat1), rad(lat2)
	dp := rad(lat2 - lat1)
	dl := rad(lon2 - lon1)
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

type GeoState struct {
	SX   float64    `json:"sx"`
	SY   float64    `json:"sy"`
	SZ   float64    `json:"sz"`
	N    int        `json:"n"`
	Dist ModelState `json:"dist"`
}

func (m *GeoModel) State() GeoState {
	return GeoState{SX: m.sx, SY: m.sy, SZ: m.sz, N: m.n, Dist: m.dist.State()}
}

func GeoFromState(s GeoState) *GeoModel {
	return &GeoModel{sx: s.SX, sy: s.SY, sz: s.SZ, n: s.N, dist: ModelFromState(s.Dist)}
}
