package detect

import (
	"math"

	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/jobspec"
)

// GeoModel is the lat_long detector's per-series baseline: it learns a series'
// typical LOCATION (a centroid on the sphere) and the typical great-circle
// distance of its points from that centroid, then scores a new location by how
// far it falls outside that normal spread. This catches "a login from an
// unusual place" — an impossible-travel / anomalous-geo signal a value detector
// can't see. Distance (km) is scored through the same robust Model used for
// metrics, so all the baseline machinery (MAD, warm-up, drift) applies.
type GeoModel struct {
	sx, sy, sz float64 // running sum of unit vectors (for a pole/antimeridian-safe centroid)
	n          int
	dist       *Model // robust model over great-circle distances from the centroid (high side)
}

// NewGeoModel builds a lat_long model. Only unusually LARGE distances are
// anomalous, so the inner distance model is one-sided high.
func NewGeoModel() *GeoModel {
	return &GeoModel{dist: NewModel(jobspec.SideHigh)}
}

// Observe scores a location against the learned centroid, then folds it in. The
// first point defines the centroid and is never anomalous (no baseline yet).
// Returns the tail probability, 0..100 score, the typical distance (km), and the
// direction (always up — a location anomaly is "unusually far").
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

// centroid returns the mean location (degrees) of the observed unit vectors.
func (m *GeoModel) centroid() (lat, lon float64) {
	x, y, z := m.sx/float64(m.n), m.sy/float64(m.n), m.sz/float64(m.n)
	lon = math.Atan2(y, x)
	lat = math.Atan2(z, math.Hypot(x, y))
	return deg(lat), deg(lon)
}

// Count is how many locations have been observed.
func (m *GeoModel) Count() int { return m.n }

// DistanceKm is the great-circle distance (km) of a location from the centroid
// learned so far. ok is false before any point has been observed.
func (m *GeoModel) DistanceKm(lat, lon float64) (float64, bool) {
	if m.n == 0 {
		return 0, false
	}
	clat, clon := m.centroid()
	return haversineKm(lat, lon, clat, clon), true
}

// haversineKm is the great-circle distance between two lat/lon points in km.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0088 // mean Earth radius, km
	p1, p2 := rad(lat1), rad(lat2)
	dp := rad(lat2 - lat1)
	dl := rad(lon2 - lon1)
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// GeoState persists a GeoModel (centroid accumulator + distance baseline).
type GeoState struct {
	SX   float64    `json:"sx"`
	SY   float64    `json:"sy"`
	SZ   float64    `json:"sz"`
	N    int        `json:"n"`
	Dist ModelState `json:"dist"`
}

// State returns a copy of the geo model's learned state.
func (m *GeoModel) State() GeoState {
	return GeoState{SX: m.sx, SY: m.sy, SZ: m.sz, N: m.n, Dist: m.dist.State()}
}

// GeoFromState rebuilds a GeoModel from a persisted state.
func GeoFromState(s GeoState) *GeoModel {
	return &GeoModel{sx: s.SX, sy: s.SY, sz: s.SZ, n: s.N, dist: ModelFromState(s.Dist)}
}
