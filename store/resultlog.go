package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/urfan03/semeion/core"
)

type ResultLog struct {
	dir string
	mu  sync.Mutex
}

func NewResultLog(dir string) *ResultLog { return &ResultLog{dir: dir} }

func (l *ResultLog) path(job string) string {
	return filepath.Join(l.dir, sanitizeJob(job)+".ndjson")
}

func sanitizeJob(job string) string {
	var b strings.Builder
	for _, r := range job {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "job"
	}
	return b.String()
}

func (l *ResultLog) Append(job string, results []core.BucketResult) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path(job), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, br := range results {
		for _, r := range br.Records {
			if r.Time.IsZero() {
				r.Time = br.Time
			}
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *ResultLog) Query(job string, from, to time.Time) ([]core.Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path(job))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []core.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r core.Record
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		if !from.IsZero() && r.Time.Before(from) {
			continue
		}
		if !to.IsZero() && r.Time.After(to) {
			continue
		}
		out = append(out, r)
	}
	return out, sc.Err()
}
