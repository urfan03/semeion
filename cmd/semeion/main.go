package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/urfan03/semeion/api"
	"github.com/urfan03/semeion/autopilot"
	"github.com/urfan03/semeion/benchmark"
	"github.com/urfan03/semeion/core"
	"github.com/urfan03/semeion/datafeed"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/ingest"
	"github.com/urfan03/semeion/jobspec"
	"github.com/urfan03/semeion/logcat"
	"github.com/urfan03/semeion/model"
	"github.com/urfan03/semeion/outlier"
	"github.com/urfan03/semeion/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	var err error
	switch os.Args[1] {
	case "demo":
		err = runDemo()
	case "run":
		err = runJob(os.Args[2:])
	case "watch":
		err = runWatch(os.Args[2:])
	case "bench":
		err = runBench(os.Args[2:])
	case "catdemo":
		err = runCatDemo()
	case "logs":
		err = runLogs(os.Args[2:])
	case "forecast":
		err = runForecast(os.Args[2:])
	case "autopilot":
		err = runAutopilot(os.Args[2:])
	case "outliers":
		err = runOutliers(os.Args[2:])
	case "cloudflare":
		err = runCloudflare(os.Args[2:])
	case "nab":
		err = runNAB(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`semeion — anomaly detection engine

usage:
  semeion demo                       run the built-in synthetic (metric) demo
  semeion catdemo                    run the built-in log-categorization demo
  semeion run  --job J [source]      run a metric job over a data source
  semeion watch --job J [source]     poll a live source forever and alert
  semeion logs --file F              detect anomalies in a log CSV (time,message)
  semeion forecast --csv F           forecast a series (seasonal-naive + trend)
  semeion autopilot --csv F          infer a job from your data (zero config)
  semeion outliers --csv F           find the rows that don't belong (population)
  semeion cloudflare --file F        detect anomalies in a Cloudflare Logpush NDJSON export
  semeion nab --csv F --windows W    score detection quality on a NAB corpus (Numenta benchmark)
  semeion serve [--addr :8080]       REST API + Anomaly Explorer UI
  semeion bench                      run the quality benchmark

run sources (pick one):
  --csv FILE                         CSV with a time column + the detector's field
  --prom-url URL --prom-query PROMQL Prometheus range query
  --es-url URL --es-index IDX        Elasticsearch date_histogram
  --ch-url URL --ch-query SQL        ClickHouse SQL ({{start}}/{{end}} placeholders)

run flags:
  --threshold N   min score 0-100 to report (default 50)
  --state FILE    load/save learned baselines (resumable runs; keeps a revertable history)
  --list-snapshots        list saved --state snapshot versions and exit
  --revert VERSION        revert the --state model to a saved snapshot before running
  --json          emit one JSON record per line
  --start, --end  RFC3339 window (datafeeds); --step duration (default: bucket_span)
  --time-col, --value-col   CSV column names
  --es-time-field           ES timestamp field (default "@timestamp")

watch flags (live sources only — no --csv):
  --interval D    poll interval (default: bucket_span); --lookback D window per tick
  --dedup D       re-alert window per series (default 30m); --min-score N
  --slack-webhook URL | --webhook URL | --alertmanager URL   (default: print)
  --once          single tick then exit (cron mode)
`)
}

func runJob(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	jobPath := fs.String("job", "", "path to job JSON file (required)")
	csvPath := fs.String("csv", "", "CSV data file")
	timeCol := fs.String("time-col", "time", "CSV timestamp column")
	valueCol := fs.String("value-col", "", "CSV value column (default: the detector's field)")
	promURL := fs.String("prom-url", "", "Prometheus base URL")
	promQuery := fs.String("prom-query", "", "PromQL query")
	esURL := fs.String("es-url", "", "Elasticsearch base URL")
	esIndex := fs.String("es-index", "", "Elasticsearch index")
	esTimeField := fs.String("es-time-field", "@timestamp", "Elasticsearch timestamp field")
	startStr := fs.String("start", "", "window start (RFC3339, datafeeds)")
	endStr := fs.String("end", "", "window end (RFC3339, datafeeds)")
	step := fs.Duration("step", 0, "datafeed step (default: job bucket_span)")
	threshold := fs.Float64("threshold", 50, "min anomaly score 0-100 to report")
	statePath := fs.String("state", "", "state file for resumable runs")
	renorm := fs.Bool("renormalize", false, "rescale scores relative to the biggest anomaly")
	jsonOut := fs.Bool("json", false, "emit one JSON record per line")
	revert := fs.String("revert", "", "revert the --state model to a saved snapshot version before running")
	listSnaps := fs.Bool("list-snapshots", false, "list saved --state snapshot versions and exit")
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

	if (*listSnaps || *revert != "") && *statePath == "" {
		return fmt.Errorf("--list-snapshots/--revert require --state")
	}
	if *statePath != "" {
		fstore := store.NewFileStore(dirOf(*statePath))
		if *listSnaps {
			versions, err := fstore.ListVersions(base(*statePath))
			if err != nil {
				return err
			}
			if len(versions) == 0 {
				fmt.Println("no saved snapshots")
				return nil
			}
			for _, v := range versions {
				fmt.Println(v)
			}
			return nil
		}
		if *revert != "" {
			if err := fstore.Revert(base(*statePath), *revert); err != nil {
				return err
			}
			fmt.Printf("reverted %s to snapshot %s\n", base(*statePath), *revert)
		}
	}

	points, err := loadPoints(job, sourceOpts{
		csvPath: *csvPath, timeCol: *timeCol, valueCol: *valueCol,
		promURL: *promURL, promQuery: *promQuery,
		esURL: *esURL, esIndex: *esIndex, esTimeField: *esTimeField,
		startStr: *startStr, endStr: *endStr, step: *step,
	})
	if err != nil {
		return err
	}

	eng, err := engine.New(job)
	if err != nil {
		return err
	}

	var st *store.FileStore
	if *statePath != "" {
		st = store.NewFileStore(dirOf(*statePath))
		if snap, found, lerr := st.Load(base(*statePath)); lerr != nil {
			return lerr
		} else if found {
			eng.Restore(snap)
		}
	}

	results := eng.Run(points, *threshold)
	if *renorm {
		engine.RenormalizeResults(results)
	}

	found := 0
	enc := json.NewEncoder(os.Stdout)
	for _, br := range results {
		for _, r := range br.Records {
			found++
			if *jsonOut {
				if err := enc.Encode(r); err != nil {
					return err
				}
				continue
			}
			printRecord(r)
		}
	}
	if !*jsonOut {
		fmt.Printf("job %q — %d points, %d buckets, %d anomalies (score >= %.0f)\n",
			job.Name, len(points), len(results), found, *threshold)
	}

	if st != nil {

		if _, err := st.SaveVersion(base(*statePath), eng.Snapshot()); err != nil {
			return err
		}
	}
	return nil
}

type sourceOpts struct {
	csvPath, timeCol, valueCol  string
	promURL, promQuery          string
	esURL, esIndex, esTimeField string
	chURL, chQuery              string
	startStr, endStr            string
	step                        time.Duration
}

func loadPoints(job jobspec.Job, o sourceOpts) ([]core.DataPoint, error) {
	switch {
	case o.csvPath != "":
		vc := o.valueCol
		if vc == "" {
			vc = firstField(job)
		}
		return ingest.ReadCSV(o.csvPath, o.timeCol, vc)

	case o.promURL != "":
		start, end, step, err := o.window(job)
		if err != nil {
			return nil, err
		}
		return datafeed.NewPromSource(o.promURL, o.promQuery).
			Fetch(context.Background(), start, end, step)

	case o.esURL != "":
		start, end, step, err := o.window(job)
		if err != nil {
			return nil, err
		}
		d := job.Detectors[0]
		src := datafeed.NewESSource(o.esURL, o.esIndex, o.esTimeField,
			datafeed.ESMetric{Func: string(d.Function), Field: d.Field})
		return src.Fetch(context.Background(), start, end, step)

	case o.chURL != "":
		start, end, step, err := o.window(job)
		if err != nil {
			return nil, err
		}
		return datafeed.NewClickHouseSource(o.chURL, o.chQuery).
			Fetch(context.Background(), start, end, step)

	default:
		return nil, fmt.Errorf("no data source: pass --csv, --prom-url, or --es-url")
	}
}

func (o sourceOpts) window(job jobspec.Job) (start, end time.Time, step time.Duration, err error) {
	if o.startStr == "" || o.endStr == "" {
		return start, end, step, fmt.Errorf("datafeeds need --start and --end (RFC3339)")
	}
	if start, err = time.Parse(time.RFC3339, o.startStr); err != nil {
		return start, end, step, fmt.Errorf("--start: %w", err)
	}
	if end, err = time.Parse(time.RFC3339, o.endStr); err != nil {
		return start, end, step, fmt.Errorf("--end: %w", err)
	}
	step = o.step
	if step <= 0 {
		step = job.BucketSpan
	}
	return start, end, step, nil
}

func runBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	n := fs.Int("n", 240, "series length in buckets")
	threshold := fs.Float64("threshold", 50, "min anomaly score 0-100")
	if err := fs.Parse(args); err != nil {
		return err
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Minute
	lbl := benchmark.Generate(start, span, *n, []int{*n / 4, *n / 2, 3 * *n / 4}, 3.0)

	job := jobspec.Job{
		Name:       "bench",
		BucketSpan: span,
		Detectors:  []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}},
	}
	eng, err := engine.New(job)
	if err != nil {
		return err
	}
	results := eng.Run(lbl.Points, *threshold)
	res := benchmark.Score(results, lbl.Anomalies, span, 1)
	fmt.Printf("benchmark (n=%d, 3 injected spikes, threshold=%.0f):\n", *n, *threshold)
	fmt.Printf("  precision=%.2f  recall=%.2f  f1=%.2f   (TP=%d FP=%d FN=%d)\n",
		res.Precision, res.Recall, res.F1, res.TP, res.FP, res.FN)
	return nil
}

func runCatDemo() error {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Minute
	lines := ingest.SyntheticLogs(start, span, 40)

	cat := logcat.NewCategorizer(span)
	results := cat.Run(lines, 50)

	found := 0
	for _, br := range results {
		for _, r := range br.Records {
			found++
			printLogRecord(r)
		}
	}
	fmt.Printf("categorization demo — %d templates discovered, %d anomalies\n",
		len(cat.Drain().Clusters()), found)
	return nil
}

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	file := fs.String("file", "", "log CSV file (required)")
	timeCol := fs.String("time-col", "time", "timestamp column")
	msgCol := fs.String("message-col", "message", "log message column")
	span := fs.Duration("span", time.Minute, "bucket span")
	threshold := fs.Float64("threshold", 50, "min anomaly score 0-100 to report")
	jsonOut := fs.Bool("json", false, "emit one JSON record per line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("--file is required")
	}
	lines, err := ingest.ReadLogCSV(*file, *timeCol, *msgCol)
	if err != nil {
		return err
	}
	cat := logcat.NewCategorizer(*span)
	results := cat.Run(lines, *threshold)

	found := 0
	enc := json.NewEncoder(os.Stdout)
	for _, br := range results {
		for _, r := range br.Records {
			found++
			if *jsonOut {
				if err := enc.Encode(r); err != nil {
					return err
				}
				continue
			}
			printLogRecord(r)
		}
	}
	if !*jsonOut {
		fmt.Printf("%d log lines, %d templates, %d anomalies (score >= %.0f)\n",
			len(lines), len(cat.Drain().Clusters()), found, *threshold)
	}
	return nil
}

func runForecast(args []string) error {
	fs := flag.NewFlagSet("forecast", flag.ContinueOnError)
	csvPath := fs.String("csv", "", "CSV data file (required)")
	timeCol := fs.String("time-col", "time", "CSV timestamp column")
	valueCol := fs.String("value-col", "value", "CSV value column")
	horizon := fs.Int("horizon", 12, "number of steps to forecast")
	modelURL := fs.String("model-url", "", "optional Python model-plane URL (else pure-Go)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *csvPath == "" {
		return fmt.Errorf("--csv is required")
	}
	points, err := ingest.ReadCSV(*csvPath, *timeCol, *valueCol)
	if err != nil {
		return err
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time.Before(points[j].Time) })
	series := make([]float64, len(points))
	for i, p := range points {
		series[i] = p.Value
	}

	var prov model.Provider = model.NewGoProvider()
	if *modelURL != "" {
		prov = model.NewHTTPProvider(*modelURL)
		fmt.Printf("using Python model plane at %s\n", *modelURL)
	}
	if periods := prov.DetectSeasonality(series); len(periods) > 0 {
		fmt.Printf("detected period: %d samples\n", periods[0])
	} else {
		fmt.Println("no strong seasonality detected")
	}
	bands := prov.ForecastBands(series, *horizon)
	fmt.Printf("forecast (%d steps, 95%% interval):\n", *horizon)
	for h, b := range bands {
		fmt.Printf("  +%-3d %.2f  [%.2f, %.2f]\n", h+1, b.Point, b.Lower, b.Upper)
	}
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	modelURL := fs.String("model-url", "", "optional Python model-plane URL")
	demo := fs.Bool("demo", true, "seed demo jobs so the UI has content")
	slackURL := fs.String("slack-webhook", "", "Slack incoming-webhook URL ($SEMEION_SLACK_WEBHOOK)")
	webhookURL := fs.String("webhook", "", "generic JSON webhook URL ($SEMEION_WEBHOOK)")
	amURL := fs.String("alertmanager", "", "Alertmanager base URL ($SEMEION_ALERTMANAGER)")
	minScore := fs.Float64("min-score", 50, "min score to alert on (live jobs)")
	dedup := fs.Duration("dedup", 30*time.Minute, "re-alert window per series (0 disables)")
	statePath := fs.String("state", "", "persist server state to this file (survives restarts)")
	saveEvery := fs.Duration("save-interval", 60*time.Second, "how often to persist --state")
	authToken := fs.String("auth-token", "", "require this bearer token on the API ($SEMEION_AUTH_TOKEN)")
	rateLimit := fs.Float64("rate-limit", 0, "max requests/second across the API (0 = unlimited)")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (serves HTTPS when set with --tls-key)")
	tlsKey := fs.String("tls-key", "", "TLS key file")
	historyDir := fs.String("history", "", "durable anomaly history directory (enables /v1/history)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	srv := api.NewServer()
	if *historyDir != "" {
		srv.WithHistory(*historyDir)
		fmt.Printf("durable history → %s\n", *historyDir)
	}
	if *modelURL != "" {
		srv.WithProvider(model.NewHTTPProvider(*modelURL)).
			WithOutlierDetector(outlier.NewHTTPDetector(*modelURL))
		fmt.Printf("using Python model plane at %s\n", *modelURL)
	}
	if tok := orEnv(*authToken, "SEMEION_AUTH_TOKEN"); tok != "" {
		srv.WithAuthToken(tok)
		fmt.Println("API authentication: bearer token required")
	} else {
		fmt.Fprintln(os.Stderr, "semeion: WARNING no auth token set — anyone who can reach the API has full access (set --auth-token or $SEMEION_AUTH_TOKEN)")
	}
	if *rateLimit > 0 {
		srv.WithRateLimit(*rateLimit)
		fmt.Printf("rate limit: %.0f req/s\n", *rateLimit)
	}

	slack, hook, am := orEnv(*slackURL, "SEMEION_SLACK_WEBHOOK"),
		orEnv(*webhookURL, "SEMEION_WEBHOOK"), orEnv(*amURL, "SEMEION_ALERTMANAGER")
	if slack != "" || hook != "" || am != "" {
		n := buildNotifier(slack, hook, am, *minScore, *minScore, *dedup)
		srv.WithNotifier(n).OnAlertError(func(err error) { fmt.Fprintln(os.Stderr, "alert:", err) })
		fmt.Printf("live-job alerting → %s\n", sinkNames(n))
	}

	if *statePath != "" {
		loaded, err := srv.LoadState(*statePath)
		if err != nil {

			fmt.Fprintf(os.Stderr, "warning: ignoring unreadable state %s: %v\n", *statePath, err)
		} else if loaded {
			fmt.Printf("state restored from %s\n", *statePath)
		}
	}
	if *demo {
		seedDemo(srv)
	}
	scheme := "http"
	if *tlsCert != "" && *tlsKey != "" {
		scheme = "https"
	}
	fmt.Printf("semeion listening on %s — Explorer at %s://localhost%s/\n", *addr, scheme, *addr)
	return serveWithState(srv, *addr, *statePath, *saveEvery, *tlsCert, *tlsKey)
}

func serveWithState(srv *api.Server, addr, statePath string, every time.Duration, tlsCert, tlsKey string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		var err error
		if tlsCert != "" && tlsKey != "" {
			err = hs.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			err = hs.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	if statePath == "" {
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return hs.Shutdown(sctx)
		}
	}

	if every <= 0 {
		every = 60 * time.Second
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	save := func() {
		if err := srv.SaveState(statePath); err != nil {
			fmt.Fprintln(os.Stderr, "save state:", err)
		}
	}
	for {
		select {
		case err := <-errc:
			return err
		case <-tk.C:
			save()
		case <-ctx.Done():
			fmt.Println("\nstopping — saving state")
			save()
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return hs.Shutdown(sctx)
		}
	}
}

func seedDemo(srv *api.Server) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	lbl := benchmark.Generate(start, time.Minute, 240, []int{60, 120, 180}, 3.0)
	job := jobspec.Job{Name: "demo-metrics", BucketSpan: time.Minute,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "v"}}}
	if eng, err := engine.New(job); err == nil {
		srv.Store(job.Name, eng.Run(lbl.Points, 50))
	}

	lines := ingest.SyntheticLogs(start, time.Minute, 40)
	cat := logcat.NewCategorizer(time.Minute)
	srv.Store("demo-logs", cat.Run(lines, 50))

	srv.SeedIntelligenceDemo(time.Now().UTC())
}

func runAutopilot(args []string) error {
	fs := flag.NewFlagSet("autopilot", flag.ContinueOnError)
	csvPath := fs.String("csv", "", "CSV data file to profile (required)")
	timeCol := fs.String("time-col", "time", "CSV timestamp column")
	valueCol := fs.String("value-col", "value", "CSV value column (for single-metric data)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *csvPath == "" {
		return fmt.Errorf("--csv is required")
	}
	points, err := ingest.ReadCSV(*csvPath, *timeCol, *valueCol)
	if err != nil {
		return err
	}
	job := autopilot.Suggest(points)
	out := map[string]any{
		"name":        job.Name,
		"bucket_span": job.BucketSpan.String(),
		"detectors":   job.Detectors,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("// suggested job from %d points — save and run with: semeion run --job <file> --csv %s\n", len(points), *csvPath)
	fmt.Println(string(b))
	return nil
}

func printLogRecord(r core.Record) {
	fmt.Printf("  %s  %-15s %-4s score=%3.0f  %q\n",
		r.Time.Format("2006-01-02 15:04"), r.Kind, r.Series, r.Score, r.Template)
}

func runDemo() error {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := 5 * time.Minute

	anomalies := map[int]float64{700: 3.0, 701: 3.2}
	points := ingest.Synthetic(start, span, 864, anomalies)

	job := jobspec.Job{
		Name:       "demo-mean-latency",
		BucketSpan: span,
		Detectors:  []jobspec.Detector{{Function: jobspec.FuncMean, Field: "latency"}},
	}
	eng, err := engine.New(job)
	if err != nil {
		return err
	}
	const threshold = 50
	results := eng.Run(points, threshold)
	found := 0
	for _, br := range results {
		for _, r := range br.Records {
			found++
			printRecord(r)
		}
	}
	fmt.Printf("job %q — %d buckets analysed, %d anomalies (score >= %d)\n",
		job.Name, len(results), found, threshold)
	if found == 0 {
		fmt.Println("  (none)")
	}
	return nil
}

func firstField(job jobspec.Job) string {
	for _, d := range job.Detectors {
		if d.Field != "" {
			return d.Field
		}
	}
	return "value"
}

func printRecord(r core.Record) {
	series := r.Series
	if series != "" {
		series = "  [" + series + "]"
	}
	fmt.Printf("  %s  %-18s actual=%9.2f  typical=%9.2f  %-4s  score=%3.0f%s\n",
		r.Time.Format("2006-01-02 15:04"), r.Detector,
		r.Actual, r.Typical, r.Direction, r.Score, series)
}

func dirOf(p string) string {
	if i := lastSep(p); i >= 0 {
		return p[:i]
	}
	return "."
}

func base(p string) string {
	if i := lastSep(p); i >= 0 {
		p = p[i+1:]
	}
	if len(p) > 5 && p[len(p)-5:] == ".json" {
		p = p[:len(p)-5]
	}
	return p
}

func lastSep(p string) int {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return i
		}
	}
	return -1
}
