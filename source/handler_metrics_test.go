// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/lambdacontext"
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

func TestNewInvocationNeverEmitsEmptyApp(t *testing.T) {
	cases := []struct{ app, repo, want string }{
		{"my-app", "repo", "my-app"},
		{"", "repo", "repo"}, // APP_NAME missing: fall back to the repository
		{"", "", "unknown"},  // validation failure with no repository at all
	}
	for _, c := range cases {
		var buf bytes.Buffer
		newInvocation(&buf, c.app, c.repo, "sha256:abc").finish("success", "", nil)
		if got := lastDoc(t, &buf)["App"]; got != c.want {
			t.Errorf("app=%q repo=%q: App = %v, want %q", c.app, c.repo, got, c.want)
		}
	}
}

func TestInvocationEmitsOnlyOnce(t *testing.T) {
	var buf bytes.Buffer
	inv := newInvocation(&buf, "app", "repo", "sha256:abc")
	inv.finish("success", "", nil)
	inv.finish("failure", PanicReason, nil)
	if n := bytes.Count(buf.Bytes(), []byte("\n")); n != 1 {
		t.Errorf("want exactly one metric line, got %d", n)
	}
	if lastDoc(t, &buf)["Outcome"] != "success" {
		t.Errorf("the first outcome must win")
	}
}

// skip and fail are what keep the emitted outcome and the returned error from
// drifting apart: skip must emit skipped with a nil error, fail must emit
// failure with the error returned.
func TestSkipAndFailPinOutcomeToReturnedError(t *testing.T) {
	ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{AwsRequestID: "req"})
	boom := errors.New("boom")
	cases := []struct {
		name        string
		call        func(inv *invocation) (string, error)
		wantOutcome string
		wantReason  string
		wantMsg     string
		wantErr     bool
	}{
		{"skip", func(inv *invocation) (string, error) { return inv.skip("no-tag", "msg") }, "skipped", "no-tag", "msg", false},
		{"skip with a failure-sounding message", func(inv *invocation) (string, error) {
			return inv.skip("oversized", OversizedMessage)
		}, "skipped", "oversized", OversizedMessage, false},
		{"fail", func(inv *invocation) (string, error) { return inv.fail(ctx, PushFailedMessage, boom) }, "failure", PushFailedMessage, PushFailedMessage, true},
		{"fail on validation", func(inv *invocation) (string, error) { return inv.fail(ctx, ValidationFailedMessage, boom) }, "failure", ValidationFailedMessage, ValidationFailedMessage, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			msg, err := c.call(newInvocation(&buf, "app", "repo", "sha256:abc"))
			if (err != nil) != c.wantErr {
				t.Errorf("returned error = %v, want non-nil: %v", err, c.wantErr)
			}
			if c.wantErr && err != boom {
				t.Errorf("fail must return the original error, got %v", err)
			}
			if msg != c.wantMsg {
				t.Errorf("message = %q, want %q", msg, c.wantMsg)
			}
			doc := lastDoc(t, &buf)
			if doc["Outcome"] != c.wantOutcome || doc["reason"] != c.wantReason {
				t.Errorf("emitted %v / %v, want %s / %s", doc["Outcome"], doc["reason"], c.wantOutcome, c.wantReason)
			}
		})
	}
}
