package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/urfan03/semeion/engine"
	"github.com/urfan03/semeion/ingest"
)

func runCloudflare(args []string) error {
	fs := flag.NewFlagSet("cloudflare", flag.ContinueOnError)
	file := fs.String("file", "", "Cloudflare Logpush/Logpull NDJSON file (required)")
	span := fs.Duration("bucket-span", time.Minute, "analysis bucket span")
	threshold := fs.Float64("threshold", 50, "min anomaly score 0-100 to report")
	name := fs.String("name", "cloudflare", "job name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("--file is required (a Cloudflare Logpush NDJSON export)")
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()

	points, skipped, err := ingest.ParseLogpush(f)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return fmt.Errorf("no usable request events in %s (%d lines skipped)", *file, skipped)
	}

	job := ingest.CloudflareJob(*name, *span)
	eng, err := engine.New(job)
	if err != nil {
		return err
	}
	results := eng.Run(points, *threshold)

	found := 0
	for _, br := range results {
		for _, r := range br.Records {
			found++
			printRecord(r)
		}
	}
	fmt.Printf("cloudflare — %d requests (%d skipped), %d buckets, %d anomalies (score >= %.0f)\n",
		len(points), skipped, len(results), found, *threshold)
	return nil
}
