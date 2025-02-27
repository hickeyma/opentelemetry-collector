// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otlptext // import "go.opentelemetry.io/collector/internal/otlptext"

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/model/pdata"
)

type dataBuffer struct {
	buf bytes.Buffer
}

func (b *dataBuffer) logEntry(format string, a ...interface{}) {
	b.buf.WriteString(fmt.Sprintf(format, a...))
	b.buf.WriteString("\n")
}

func (b *dataBuffer) logAttr(label string, value string) {
	b.logEntry("    %-15s: %s", label, value)
}

func (b *dataBuffer) logAttributes(label string, m pdata.Map) {
	if m.Len() == 0 {
		return
	}

	b.logEntry("%s:", label)
	m.Range(func(k string, v pdata.Value) bool {
		b.logEntry("     -> %s: %s(%s)", k, v.Type().String(), attributeValueToString(v))
		return true
	})
}

func (b *dataBuffer) logInstrumentationScope(il pdata.InstrumentationScope) {
	b.logEntry(
		"InstrumentationScope %s %s",
		il.Name(),
		il.Version())
}

func (b *dataBuffer) logMetricDescriptor(md pdata.Metric) {
	b.logEntry("Descriptor:")
	b.logEntry("     -> Name: %s", md.Name())
	b.logEntry("     -> Description: %s", md.Description())
	b.logEntry("     -> Unit: %s", md.Unit())
	b.logEntry("     -> DataType: %s", md.DataType().String())
}

func (b *dataBuffer) logMetricDataPoints(m pdata.Metric) {
	switch m.DataType() {
	case pdata.MetricDataTypeNone:
		return
	case pdata.MetricDataTypeGauge:
		b.logNumberDataPoints(m.Gauge().DataPoints())
	case pdata.MetricDataTypeSum:
		data := m.Sum()
		b.logEntry("     -> IsMonotonic: %t", data.IsMonotonic())
		b.logEntry("     -> AggregationTemporality: %s", data.AggregationTemporality().String())
		b.logNumberDataPoints(data.DataPoints())
	case pdata.MetricDataTypeHistogram:
		data := m.Histogram()
		b.logEntry("     -> AggregationTemporality: %s", data.AggregationTemporality().String())
		b.logDoubleHistogramDataPoints(data.DataPoints())
	case pdata.MetricDataTypeExponentialHistogram:
		data := m.ExponentialHistogram()
		b.logEntry("     -> AggregationTemporality: %s", data.AggregationTemporality().String())
		b.logExponentialHistogramDataPoints(data.DataPoints())
	case pdata.MetricDataTypeSummary:
		data := m.Summary()
		b.logDoubleSummaryDataPoints(data.DataPoints())
	}
}

func (b *dataBuffer) logNumberDataPoints(ps pdata.NumberDataPointSlice) {
	for i := 0; i < ps.Len(); i++ {
		p := ps.At(i)
		b.logEntry("NumberDataPoints #%d", i)
		b.logDataPointAttributes(p.Attributes())

		b.logEntry("StartTimestamp: %s", p.StartTimestamp())
		b.logEntry("Timestamp: %s", p.Timestamp())
		switch p.ValueType() {
		case pdata.MetricValueTypeInt:
			b.logEntry("Value: %d", p.IntVal())
		case pdata.MetricValueTypeDouble:
			b.logEntry("Value: %f", p.DoubleVal())
		}
	}
}

func (b *dataBuffer) logDoubleHistogramDataPoints(ps pdata.HistogramDataPointSlice) {
	for i := 0; i < ps.Len(); i++ {
		p := ps.At(i)
		b.logEntry("HistogramDataPoints #%d", i)
		b.logDataPointAttributes(p.Attributes())

		b.logEntry("StartTimestamp: %s", p.StartTimestamp())
		b.logEntry("Timestamp: %s", p.Timestamp())
		b.logEntry("Count: %d", p.Count())
		b.logEntry("Sum: %f", p.Sum())

		bounds := p.ExplicitBounds()
		if len(bounds) != 0 {
			for i, bound := range bounds {
				b.logEntry("ExplicitBounds #%d: %f", i, bound)
			}
		}

		buckets := p.BucketCounts()
		if len(buckets) != 0 {
			for j, bucket := range buckets {
				b.logEntry("Buckets #%d, Count: %d", j, bucket)
			}
		}
	}
}

func (b *dataBuffer) logExponentialHistogramDataPoints(ps pdata.ExponentialHistogramDataPointSlice) {
	for i := 0; i < ps.Len(); i++ {
		p := ps.At(i)
		b.logEntry("ExponentialHistogramDataPoints #%d", i)
		b.logDataPointAttributes(p.Attributes())

		b.logEntry("StartTimestamp: %s", p.StartTimestamp())
		b.logEntry("Timestamp: %s", p.Timestamp())
		b.logEntry("Count: %d", p.Count())
		b.logEntry("Sum: %f", p.Sum())

		scale := int(p.Scale())
		factor := math.Ldexp(math.Ln2, -scale)
		// Note: the equation used here, which is
		//   math.Exp(index * factor)
		// reports +Inf as the _lower_ boundary of the bucket nearest
		// infinity, which is incorrect and can be addressed in various
		// ways.  The OTel-Go implementation of this histogram pending
		// in https://github.com/open-telemetry/opentelemetry-go/pull/2393
		// uses a lookup table for the last finite boundary, which can be
		// easily computed using `math/big` (for scales up to 20).

		negB := p.Negative().BucketCounts()
		posB := p.Positive().BucketCounts()

		for i := 0; i < len(negB); i++ {
			pos := len(negB) - i - 1
			index := p.Negative().Offset() + int32(pos)
			count := p.Negative().BucketCounts()[pos]
			lower := math.Exp(float64(index) * factor)
			upper := math.Exp(float64(index+1) * factor)
			b.logEntry("Bucket (%f, %f], Count: %d", -upper, -lower, count)
		}

		if p.ZeroCount() != 0 {
			b.logEntry("Bucket [0, 0], Count: %d", p.ZeroCount())
		}

		for pos := 0; pos < len(posB); pos++ {
			index := p.Positive().Offset() + int32(pos)
			count := p.Positive().BucketCounts()[pos]
			lower := math.Exp(float64(index) * factor)
			upper := math.Exp(float64(index+1) * factor)
			b.logEntry("Bucket [%f, %f), Count: %d", lower, upper, count)
		}
	}
}

func (b *dataBuffer) logDoubleSummaryDataPoints(ps pdata.SummaryDataPointSlice) {
	for i := 0; i < ps.Len(); i++ {
		p := ps.At(i)
		b.logEntry("SummaryDataPoints #%d", i)
		b.logDataPointAttributes(p.Attributes())

		b.logEntry("StartTimestamp: %s", p.StartTimestamp())
		b.logEntry("Timestamp: %s", p.Timestamp())
		b.logEntry("Count: %d", p.Count())
		b.logEntry("Sum: %f", p.Sum())

		quantiles := p.QuantileValues()
		for i := 0; i < quantiles.Len(); i++ {
			quantile := quantiles.At(i)
			b.logEntry("QuantileValue #%d: Quantile %f, Value %f", i, quantile.Quantile(), quantile.Value())
		}
	}
}

func (b *dataBuffer) logDataPointAttributes(labels pdata.Map) {
	b.logAttributes("Data point attributes", labels)
}

func (b *dataBuffer) logEvents(description string, se pdata.SpanEventSlice) {
	if se.Len() == 0 {
		return
	}

	b.logEntry("%s:", description)
	for i := 0; i < se.Len(); i++ {
		e := se.At(i)
		b.logEntry("SpanEvent #%d", i)
		b.logEntry("     -> Name: %s", e.Name())
		b.logEntry("     -> Timestamp: %s", e.Timestamp())
		b.logEntry("     -> DroppedAttributesCount: %d", e.DroppedAttributesCount())

		if e.Attributes().Len() == 0 {
			continue
		}
		b.logEntry("     -> Attributes:")
		e.Attributes().Range(func(k string, v pdata.Value) bool {
			b.logEntry("         -> %s: %s(%s)", k, v.Type().String(), attributeValueToString(v))
			return true
		})
	}
}

func (b *dataBuffer) logLinks(description string, sl pdata.SpanLinkSlice) {
	if sl.Len() == 0 {
		return
	}

	b.logEntry("%s:", description)

	for i := 0; i < sl.Len(); i++ {
		l := sl.At(i)
		b.logEntry("SpanLink #%d", i)
		b.logEntry("     -> Trace ID: %s", l.TraceID().HexString())
		b.logEntry("     -> ID: %s", l.SpanID().HexString())
		b.logEntry("     -> TraceState: %s", l.TraceState())
		b.logEntry("     -> DroppedAttributesCount: %d", l.DroppedAttributesCount())
		if l.Attributes().Len() == 0 {
			continue
		}
		b.logEntry("     -> Attributes:")
		l.Attributes().Range(func(k string, v pdata.Value) bool {
			b.logEntry("         -> %s: %s(%s)", k, v.Type().String(), attributeValueToString(v))
			return true
		})
	}
}

func attributeValueToString(v pdata.Value) string {
	switch v.Type() {
	case pdata.ValueTypeString:
		return v.StringVal()
	case pdata.ValueTypeBool:
		return strconv.FormatBool(v.BoolVal())
	case pdata.ValueTypeDouble:
		return strconv.FormatFloat(v.DoubleVal(), 'f', -1, 64)
	case pdata.ValueTypeInt:
		return strconv.FormatInt(v.IntVal(), 10)
	case pdata.ValueTypeSlice:
		return sliceToString(v.SliceVal())
	case pdata.ValueTypeMap:
		return mapToString(v.MapVal())
	default:
		return fmt.Sprintf("<Unknown OpenTelemetry attribute value type %q>", v.Type())
	}
}

func sliceToString(s pdata.Slice) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < s.Len(); i++ {
		if i < s.Len()-1 {
			fmt.Fprintf(&b, "%s, ", attributeValueToString(s.At(i)))
		} else {
			b.WriteString(attributeValueToString(s.At(i)))
		}
	}

	b.WriteByte(']')
	return b.String()
}

func mapToString(m pdata.Map) string {
	var b strings.Builder
	b.WriteString("{\n")

	m.Sort().Range(func(k string, v pdata.Value) bool {
		fmt.Fprintf(&b, "     -> %s: %s(%s)\n", k, v.Type(), v.AsString())
		return true
	})
	b.WriteByte('}')
	return b.String()
}
