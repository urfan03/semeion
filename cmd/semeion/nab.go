package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/urfan03/semeion/benchmark"
	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/jobspec"
)

func runNAB(args []string) error {
	fs := flag.NewFlagSet("nab", flag.ContinueOnError)
	csv := fs.String("csv", "", "NAB data CSV (timestamp,value) (required)")
	windows := fs.String("windows", "", "NAB anomaly-window JSON ([[start,end],...]) (required)")
	span := fs.Duration("bucket-span", time.Minute, "analysis bucket span")
	threshold := fs.Float64("threshold", 50, "min anomaly score to count as a detection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *csv == "" || *windows == "" {
		return fmt.Errorf("--csv and --windows are required")
	}
	cf, err := os.Open(*csv)
	if err != nil {
		return err
	}
	defer cf.Close()
	pts, err := benchmark.LoadNABCSV(cf)
	if err != nil {
		return err
	}
	wf, err := os.Open(*windows)
	if err != nil {
		return err
	}
	defer wf.Close()
	wins, err := benchmark.ParseNABWindows(wf)
	if err != nil {
		return err
	}

	job := jobspec.Job{Name: "nab", BucketSpan: *span,
		Detectors: []jobspec.Detector{{Function: jobspec.FuncMean, Field: "value", Side: jobspec.SideBoth}}}
	eng, err := engine.New(job)
	if err != nil {
		return err
	}
	results := eng.Run(pts, *threshold)
	dets := benchmark.DetectionTimes(results, *threshold)

	fmt.Printf("NAB: %d points, %d windows, %d detections (score >= %.0f)\n",
		len(pts), len(wins), len(dets), *threshold)
	for _, p := range []benchmark.NABProfile{benchmark.StandardProfile(), benchmark.LowFPProfile(), benchmark.LowFNProfile()} {
		r := benchmark.NABNormalized(dets, wins, p)
		fmt.Printf("  %-14s normalized=%6.2f  raw=%7.3f  TP=%d FP=%d FN=%d\n",
			r.Profile, r.Normalized, r.Raw, r.TP, r.FP, r.FN)
	}
	return nil
}
