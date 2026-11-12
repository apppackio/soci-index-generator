package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func lastDoc(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var doc map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("no valid EMF line written: %v (%q)", err, buf.String())
	}
	return doc
}

func TestInvocationSkipCarriesReasonRepoAndDigest(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.finish("skipped", "already-converted")
	doc := lastDoc(t, &buf)
	if doc["Outcome"] != "skipped" || doc["reason"] != "already-converted" ||
		doc["repository"] != "repo" || doc["digest"] != "sha256:abc" || doc["App"] != "app" {
		t.Errorf("unexpected document: %v", doc)
	}
	if _, ok := doc["size_guard"]; ok {
		t.Errorf("size_guard must be absent when the guard ran")
	}
}

func TestInvocationFlagsUnenforcedSizeGuard(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.sizeGuardSkipped = true
	inv.finish("success", "")
	doc := lastDoc(t, &buf)
	if doc["size_guard"] != "not-enforced" {
		t.Errorf("size_guard = %v, want not-enforced", doc["size_guard"])
	}
	if _, ok := doc["reason"]; ok {
		t.Errorf("empty reason must be omitted")
	}
}

// A metrics failure must never change the handler's result.
func TestInvocationFinishSwallowsEmitErrors(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.finish("bogus-outcome", "") // must not panic
	if buf.Len() != 0 {
		t.Errorf("nothing should be written for an invalid outcome")
	}
}
