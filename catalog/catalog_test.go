package catalog

import (
	"testing"
	"time"
)

func TestCatalogTemplatesValidate(t *testing.T) {
	list := List()
	if len(list) < 5 {
		t.Fatalf("expected the built-in templates, got %d", len(list))
	}
	for _, tpl := range list {
		job, ok := Get(tpl.Name, time.Minute)
		if !ok {
			t.Fatalf("Get(%q) missing", tpl.Name)
		}
		if err := job.Validate(); err != nil {
			t.Fatalf("template %q produced an invalid job: %v", tpl.Name, err)
		}
		if job.BucketSpan != time.Minute {
			t.Fatalf("template %q ignored the span", tpl.Name)
		}
	}
	if _, ok := Get("nope", time.Minute); ok {
		t.Fatal("unknown template should not resolve")
	}
	if _, ok := Get("nginx", 0); !ok {
		t.Fatal("a zero span should default, not fail")
	}
}
