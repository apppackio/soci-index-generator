// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

// Package metrics emits CloudWatch Embedded Metric Format (EMF) documents.
//
// EMF needs no IAM permission and no API call: a correctly shaped JSON line on
// stdout becomes a CloudWatch metric. That matters here because conversion is a
// pure optimization. When it silently stops working nothing breaks, tasks just
// start slowly, so these metrics are the only signal anyone gets.
package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const Namespace = "AppPack/SOCI"

// Outcomes a single invocation can report.
const (
	OutcomeSuccess = "success"
	OutcomeSkipped = "skipped"
	OutcomeFailure = "failure"
)

// Emit writes one EMF line to w. properties are searchable log fields only, not
// dimensions (that would multiply metric cardinality per repository/digest).
// Properties never override the dimensions, metrics or _aws envelope.
func Emit(w io.Writer, app string, outcome string, duration time.Duration, properties map[string]string) error {
	switch outcome {
	case OutcomeSuccess, OutcomeSkipped, OutcomeFailure:
	default:
		return fmt.Errorf("unknown outcome %q", outcome)
	}

	// The document is flat apart from _aws: EMF rejects nested dimension values.
	doc := make(map[string]interface{}, len(properties)+5)
	for k, v := range properties {
		doc[k] = v
	}
	doc["_aws"] = map[string]interface{}{
		"Timestamp": time.Now().UnixMilli(),
		"CloudWatchMetrics": []interface{}{
			map[string]interface{}{
				"Namespace":  Namespace,
				"Dimensions": [][]string{{"App", "Outcome"}},
				"Metrics": []map[string]string{
					{"Name": "Conversions", "Unit": "Count"},
					{"Name": "ConversionSeconds", "Unit": "Seconds"},
				},
			},
		},
	}
	doc["App"] = app
	doc["Outcome"] = outcome
	doc["Conversions"] = 1
	doc["ConversionSeconds"] = duration.Seconds()

	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
