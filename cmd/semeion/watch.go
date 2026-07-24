package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/urfan03/semeion/alert"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/datafeed"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/store"
)

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	jobPath := fs.String("job", "", "path to job JSON file (required)")
	promURL := fs.String("prom-url", "", "Prometheus base URL")
	promQuery := fs.String("prom-query", "", "PromQL query")
	esURL := fs.String("es-url", "", "Elasticsearch base URL")
	esIndex := fs.String("es-index", "", "Elasticsearch index")
	esTimeField := fs.String("es-time-field", "@timestamp", "Elasticsearch timestamp field")
	chURL := fs.String("ch-url", "", "ClickHouse base URL")
	chQuery := fs.String("ch-query", "", "ClickHouse SQL with {{start}}/{{end}}")
	interval := fs.Duration("interval", 0, "poll interval (default: bucket_span)")
	lookback := fs.Duration("lookback", 0, "window fetched each tick (default: 5× interval)")
	statePath := fs.String("state", "", "state file — baselines survive restarts")
	threshold := fs.Float64("threshold", 50, "min anomaly score 0-100 to report")
	minScore := fs.Float64("min-score", 0, "min score to alert on (default: --threshold)")
	dedup := fs.Duration("dedup", 30*time.Minute, "re-alert window per series (0 disables)")
	slackURL := fs.String("slack-webhook", "", "Slack incoming-webhook URL ($SEMEION_SLACK_WEBHOOK)")
	webhookURL := fs.String("webhook", "", "generic JSON webhook URL ($SEMEION_WEBHOOK)")
	amURL := fs.String("alertmanager", "", "Alertmanager base URL ($SEMEION_ALERTMANAGER)")
	modelURL := fs.String("model-url", "", "optional Python model-plane URL")
	once := fs.Bool("once", false, "run a single tick and exit (for cron / testing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *jobPath == "" {
		return fmt.Errorf("--job is required (see: semeion help)")
	}
	job, err := jobspec.LoadFile(*jobPath)
	if err != nil {
		return err
	}

	src, err := watchSource(job, sourceOpts{
		promURL: *promURL, promQuery: *promQuery,
		esURL: *esURL, esIndex: *esIndex, esTimeField: *esTimeField,
		chURL: *chURL, chQuery: *chQuery,
	})
	if err != nil {
		return err
	}

	every := *interval
	if every <= 0 {
		every = job.BucketSpan
	}
	back := *lookback
	if back <= 0 {
		back = 5 * every
	}

	prov := model.Provider(model.NewGoProvider())
	if *modelURL != "" {
		prov = model.NewHTTPProvider(*modelURL)
	}
	eng, err := engine.NewWithProvider(job, prov)
	if err != nil {
		return err
	}

	var st *store.FileStore
	if *statePath != "" {
		st = store.NewFileStore(dirOf(*statePath))
		snap, found, lerr := st.Load(base(*statePath))
		if lerr != nil {

			fmt.Fprintf(os.Stderr, "warning: ignoring unreadable state %s: %v\n", *statePath, lerr)
		} else if found {
			eng.Restore(snap)
			fmt.Printf("resumed baselines from %s\n", *statePath)
		}
	}

	notifier := buildNotifier(
		orEnv(*slackURL, "SEMEION_SLACK_WEBHOOK"),
		orEnv(*webhookURL, "SEMEION_WEBHOOK"),
		orEnv(*amURL, "SEMEION_ALERTMANAGER"),
		*minScore, *threshold, *dedup)
	fmt.Printf("watching job %q every %s (lookback %s) → %s\n",
		job.Name, every, back, sinkNames(notifier))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := &watcher{job: job, eng: eng, src: src, notifier: notifier,
		threshold: *threshold, lookback: back, store: st, statePath: *statePath}

	if *once {
		return w.tick(ctx, time.Now())
	}
	if err := w.tick(ctx, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "tick:", err)
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nstopping — saving state")
			return w.save()
		case now := <-tk.C:
			if err := w.tick(ctx, now); err != nil {

				fmt.Fprintln(os.Stderr, "tick:", err)
			}
		}
	}
}

type watcher struct {
	job       jobspec.Job
	eng       *engine.Engine
	src       datafeed.Source
	notifier  *alert.Notifier
	threshold float64
	lookback  time.Duration
	store     *store.FileStore
	statePath string

	lastSeen time.Time
}

func (w *watcher) tick(ctx context.Context, now time.Time) error {
	points, err := w.src.Fetch(ctx, now.Add(-w.lookback), now, w.job.BucketSpan)
	if err != nil {
		return err
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time.Before(points[j].Time) })

	var results []core.BucketResult
	fresh := 0
	for _, p := range points {
		if !w.lastSeen.IsZero() && !p.Time.After(w.lastSeen) {
			continue
		}
		w.lastSeen = p.Time
		fresh++
		for _, br := range w.eng.Push(p) {
			if len(br.Records) > 0 {
				results = append(results, br)
			}
		}
	}
	if fresh == 0 {
		return nil
	}

	sent, nerr := w.notifier.Notify(ctx, w.job.Name, filterScore(results, w.threshold))
	fmt.Printf("%s  %d new points, %d anomalous buckets, %d alerts\n",
		now.UTC().Format("15:04:05"), fresh, len(results), sent)
	if serr := w.save(); serr != nil {
		return serr
	}
	return nerr
}

func (w *watcher) save() error {
	if w.store == nil {
		return nil
	}
	return w.store.Save(base(w.statePath), w.eng.Snapshot())
}

func filterScore(in []core.BucketResult, threshold float64) []core.BucketResult {
	out := make([]core.BucketResult, 0, len(in))
	for _, br := range in {
		keep := make([]core.Record, 0, len(br.Records))
		for _, r := range br.Records {
			if r.Score >= threshold {
				keep = append(keep, r)
			}
		}
		if len(keep) > 0 {
			br.Records = keep
			out = append(out, br)
		}
	}
	return out
}

func buildNotifier(slackURL, webhookURL, amURL string, minScore, threshold float64, dedup time.Duration) *alert.Notifier {
	var sinks []alert.Sink
	if slackURL != "" {
		sinks = append(sinks, alert.NewSlackSink(slackURL))
	}
	if webhookURL != "" {
		sinks = append(sinks, alert.NewWebhookSink(webhookURL))
	}
	if amURL != "" {
		sinks = append(sinks, alert.NewAlertmanagerSink(amURL))
	}
	if len(sinks) == 0 {

		sinks = append(sinks, alert.StdoutSink{})
	}
	n := alert.NewNotifier(sinks...)
	if minScore > 0 {
		n.MinScore = minScore
	} else {
		n.MinScore = threshold
	}
	n.Dedup = dedup
	n.OnError = func(sink string, a alert.Alert, err error) {
		fmt.Fprintf(os.Stderr, "sink %s failed for %s: %v\n", sink, a.Title(), err)
	}
	return n
}

func orEnv(flagVal, env string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(env)
}

func sinkNames(n *alert.Notifier) string {
	names := make([]string, 0, len(n.Sinks))
	for _, s := range n.Sinks {
		names = append(names, s.Name())
	}
	return fmt.Sprintf("%v (min score %.0f, dedup %s)", names, n.MinScore, n.Dedup)
}

func watchSource(job jobspec.Job, o sourceOpts) (datafeed.Source, error) {
	switch {
	case o.promURL != "":
		return datafeed.NewPromSource(o.promURL, o.promQuery), nil
	case o.esURL != "":
		d := job.Detectors[0]
		return datafeed.NewESSource(o.esURL, o.esIndex, o.esTimeField,
			datafeed.ESMetric{Func: string(d.Function), Field: d.Field}), nil
	case o.chURL != "":
		return datafeed.NewClickHouseSource(o.chURL, o.chQuery), nil
	default:
		return nil, fmt.Errorf("watch needs a live source: --prom-url, --es-url, or --ch-url")
	}
}
