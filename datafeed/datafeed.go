package datafeed

import (
	"context"
	"time"

	"github.com/urfan03/semeion/core"
)

type Source interface {
	Fetch(ctx context.Context, start, end time.Time, step time.Duration) ([]core.DataPoint, error)
}
