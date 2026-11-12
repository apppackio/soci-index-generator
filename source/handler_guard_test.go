// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"
)

func TestShouldSkipConversionWhenAlreadyConverted(t *testing.T) {
	if !shouldSkipConversion(true, nil) {
		t.Fatal("an already-converted index must be skipped")
	}
}

// The fail-closed property: a failed loop-guard check must skip, never convert.
func TestShouldSkipConversionWhenCheckErrored(t *testing.T) {
	if !shouldSkipConversion(false, errors.New("AccessDenied")) {
		t.Fatal("a failed conversion check must skip the image (fail closed)")
	}
}

func TestShouldNotSkipConversionForPlainUnconvertedImage(t *testing.T) {
	if shouldSkipConversion(false, nil) {
		t.Fatal("an unconverted image with a successful check must be converted")
	}
}

func TestResolveIndexVersion(t *testing.T) {
	for _, raw := range []string{"", "V2"} {
		got, err := resolveIndexVersion(raw)
		if err != nil || got != "V2" {
			t.Errorf("resolveIndexVersion(%q) = (%q, %v), want (V2, nil)", raw, got, err)
		}
	}
	// Anything else must fail rather than silently take the V1 path.
	for _, raw := range []string{"V1", "v2", "V2 ", " V2", "2", "V3"} {
		if got, err := resolveIndexVersion(raw); err == nil {
			t.Errorf("resolveIndexVersion(%q) = (%q, nil), want an error", raw, got)
		}
	}
}
