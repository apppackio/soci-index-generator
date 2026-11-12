// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEmitWritesEmbeddedMetricFormat(t *testing.T) {
	var buf bytes.Buffer
	err := Emit(&buf, "my-app", "success", 8*time.Second, map[string]string{"repository": "r"})
	if err != nil {
		t.Fatalf("Emit returned %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Fatalf("output must end in exactly one newline, got %q", out)
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	aws, ok := doc["_aws"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing _aws object: %v", doc)
	}
	if ts, ok := aws["Timestamp"].(float64); !ok || ts < 1e12 {
		t.Errorf("_aws.Timestamp must be epoch milliseconds, got %v", aws["Timestamp"])
	}
	directives := aws["CloudWatchMetrics"].([]interface{})
	if len(directives) != 1 {
		t.Fatalf("want one metric directive, got %d", len(directives))
	}
	d := directives[0].(map[string]interface{})
	if d["Namespace"] != "AppPack/SOCI" {
		t.Errorf("Namespace = %v", d["Namespace"])
	}
	wantDims := []interface{}{[]interface{}{"App", "Outcome"}}
	if !reflect.DeepEqual(d["Dimensions"], wantDims) {
		t.Errorf("Dimensions = %v, want [[App Outcome]]", d["Dimensions"])
	}
	wantMetrics := []interface{}{
		map[string]interface{}{"Name": "Conversions", "Unit": "Count"},
		map[string]interface{}{"Name": "ConversionSeconds", "Unit": "Seconds"},
	}
	if !reflect.DeepEqual(d["Metrics"], wantMetrics) {
		t.Errorf("Metrics = %v", d["Metrics"])
	}

	// Every listed metric and dimension must exist at the root, or EMF ignores it.
	// Dimension values must be strings; metric values numbers.
	if doc["App"] != "my-app" || doc["Outcome"] != "success" {
		t.Errorf("dimension values wrong: App=%v Outcome=%v", doc["App"], doc["Outcome"])
	}
	if doc["Conversions"] != float64(1) || doc["ConversionSeconds"] != float64(8) {
		t.Errorf("metric values wrong: %v %v", doc["Conversions"], doc["ConversionSeconds"])
	}
	if doc["repository"] != "r" {
		t.Errorf("property not carried: %v", doc["repository"])
	}
	for k, v := range doc {
		if k == "_aws" {
			continue
		}
		if _, nested := v.(map[string]interface{}); nested {
			t.Errorf("%s is a nested object; EMF rejects these", k)
		}
	}
}

func TestEmitRejectsUnknownOutcome(t *testing.T) {
	var buf bytes.Buffer
	if err := Emit(&buf, "app", "exploded", time.Second, nil); err == nil {
		t.Fatal("unknown outcome must be an error")
	}
	if buf.Len() != 0 {
		t.Errorf("nothing may be written on error, got %q", buf.String())
	}
	for _, o := range []string{"success", "skipped", "failure"} {
		if err := Emit(&bytes.Buffer{}, "app", o, time.Second, nil); err != nil {
			t.Errorf("outcome %q rejected: %v", o, err)
		}
	}
}

// A property must never overwrite a dimension, metric, or the _aws envelope.
func TestEmitPropertiesCannotClobberReservedKeys(t *testing.T) {
	var buf bytes.Buffer
	props := map[string]string{"App": "evil", "Outcome": "evil", "Conversions": "9", "_aws": "x", "reason": "ok"}
	if err := Emit(&buf, "my-app", "skipped", 0, props); err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["App"] != "my-app" || doc["Outcome"] != "skipped" || doc["Conversions"] != float64(1) {
		t.Errorf("reserved keys clobbered: %v", doc)
	}
	if _, ok := doc["_aws"].(map[string]interface{}); !ok {
		t.Errorf("_aws clobbered: %v", doc["_aws"])
	}
	if doc["reason"] != "ok" {
		t.Errorf("ordinary property lost")
	}
}
