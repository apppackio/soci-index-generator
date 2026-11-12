package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	inv.finish("skipped", "already-converted", nil)
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
	inv.finish("success", "", nil)
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
	inv.finish("bogus-outcome", "", nil) // must not panic
	if buf.Len() != 0 {
		t.Errorf("nothing should be written for an invalid outcome")
	}
}

// reason must stay a bounded constant; the raw error text goes in its own property.
func TestInvocationFailureKeepsReasonStableAndErrorSeparate(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.finish("failure", PushFailedMessage, errors.New("throttled arn:aws:ecr:us-east-1:123:repository/x"))
	doc := lastDoc(t, &buf)
	if doc["reason"] != PushFailedMessage {
		t.Errorf("reason = %v, want the message constant", doc["reason"])
	}
	if doc["error"] != "throttled arn:aws:ecr:us-east-1:123:repository/x" {
		t.Errorf("error = %v", doc["error"])
	}
}

func TestInvocationOmitsErrorWhenNil(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.finish("skipped", "no-tag", nil)
	if _, ok := lastDoc(t, &buf)["error"]; ok {
		t.Error("error property must be absent without an error")
	}
}
