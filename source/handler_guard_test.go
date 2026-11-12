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
