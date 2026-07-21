// Package datafeed pulls points from external backends into the engine — the
// equivalent of Elastic ML's "datafeed". A Source fetches a time range at a
// given step; the CLI (or a scheduler) then feeds the points to an Engine.
//
// Shipped sources: Prometheus (range query) and Elasticsearch (date_histogram
// aggregation). Both are source-agnostic to the engine — semeion is not tied to
// any one backend.
package datafeed

import (
	"context"
	"time"

	"github.com/urfan03/semeion/core"
)

// Source fetches points for [start, end] aggregated at step resolution.
type Source interface {
	Fetch(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error)
}
