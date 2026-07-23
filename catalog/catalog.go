package catalog

import (
	"sort"
	"time"

	"github.com/urfan03/semeion/jobspec"
)

type Template struct {
	Name        string
	Description string
	Build       func(span time.Duration) jobspec.Job
}

var registry = map[string]Template{}

func register(t Template) { registry[t.Name] = t }

func Get(name string, span time.Duration) (jobspec.Job, bool) {
	t, ok := registry[name]
	if !ok {
		return jobspec.Job{}, false
	}
	if span <= 0 {
		span = time.Minute
	}
	return t.Build(span), true
}

func List() []Template {
	out := make([]Template, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func high() jobspec.Side { return jobspec.SideHigh }
func low() jobspec.Side  { return jobspec.SideLow }

func init() {
	register(Template{
		Name:        "nginx",
		Description: "nginx / web access logs: traffic, error-class spikes, latency, path diversity",
		Build: func(span time.Duration) jobspec.Job {
			return jobspec.Job{
				Name: "nginx", BucketSpan: span,
				Detectors: []jobspec.Detector{
					{Function: jobspec.FuncCount, ByField: "host"},
					{Function: jobspec.FuncCount, ByField: "status_class", Side: high()},
					{Function: jobspec.FuncRatio, Field: "errors", DenomField: "total", Side: high()},
					{Function: jobspec.FuncMean, Field: "request_time", ByField: "host", Side: high()},
					{Function: jobspec.FuncInfoContent, ByField: "path", PartitionField: "host"},
				},
				Influencers: []string{"host", "status_class", "path"},
			}
		},
	})
	register(Template{
		Name:        "kubernetes",
		Description: "Kubernetes workloads: per-pod CPU/memory, restart bursts, namespace traffic",
		Build: func(span time.Duration) jobspec.Job {
			return jobspec.Job{
				Name: "kubernetes", BucketSpan: span,
				Detectors: []jobspec.Detector{
					{Function: jobspec.FuncMean, Field: "cpu", ByField: "pod", Side: high()},
					{Function: jobspec.FuncMean, Field: "memory", ByField: "pod", Side: high()},
					{Function: jobspec.FuncSum, Field: "restarts", ByField: "pod", Side: high()},
					{Function: jobspec.FuncCount, ByField: "namespace"},
					{Fields: []string{"cpu", "memory"}, ByField: "pod"},
				},
				Influencers: []string{"pod", "namespace", "node"},
			}
		},
	})
	register(Template{
		Name:        "postgres",
		Description: "PostgreSQL: query latency, connections, deadlocks, error ratio",
		Build: func(span time.Duration) jobspec.Job {
			return jobspec.Job{
				Name: "postgres", BucketSpan: span,
				Detectors: []jobspec.Detector{
					{Function: jobspec.FuncMean, Field: "query_time_ms", ByField: "db", Side: high()},
					{Function: jobspec.FuncMean, Field: "connections", ByField: "db", Side: high()},
					{Function: jobspec.FuncSum, Field: "deadlocks", ByField: "db", Side: high()},
					{Function: jobspec.FuncRatio, Field: "errors", DenomField: "queries", Side: high()},
				},
				Influencers: []string{"db", "host"},
			}
		},
	})
	register(Template{
		Name:        "redis",
		Description: "Redis: command latency, memory, evictions, cache hit-rate drop",
		Build: func(span time.Duration) jobspec.Job {
			return jobspec.Job{
				Name: "redis", BucketSpan: span,
				Detectors: []jobspec.Detector{
					{Function: jobspec.FuncMean, Field: "latency_ms", ByField: "instance", Side: high()},
					{Function: jobspec.FuncMean, Field: "used_memory", ByField: "instance", Side: high()},
					{Function: jobspec.FuncSum, Field: "evicted_keys", ByField: "instance", Side: high()},
					{Function: jobspec.FuncRatio, Field: "keyspace_hits", DenomField: "keyspace_ops", ByField: "instance", Side: low()},
				},
				Influencers: []string{"instance", "host"},
			}
		},
	})
	register(Template{
		Name:        "jvm",
		Description: "JVM services: heap, GC pause, thread count, exception bursts",
		Build: func(span time.Duration) jobspec.Job {
			return jobspec.Job{
				Name: "jvm", BucketSpan: span,
				Detectors: []jobspec.Detector{
					{Function: jobspec.FuncMean, Field: "heap_used", ByField: "service", Side: high()},
					{Function: jobspec.FuncMean, Field: "gc_pause_ms", ByField: "service", Side: high()},
					{Function: jobspec.FuncMean, Field: "threads", ByField: "service", Side: high()},
					{Function: jobspec.FuncCount, ByField: "exception_class", Side: high()},
				},
				Influencers: []string{"service", "exception_class", "host"},
			}
		},
	})
}
