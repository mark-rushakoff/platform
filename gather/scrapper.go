package gather

import (
	"context"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"time"

	"github.com/influxdata/platform"
	"github.com/matttproud/golang_protobuf_extensions/pbutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

/*
	1. Discoverer
	2. Scheduler
	3. Queue
	4. Scrap
	5. Storage
*/

// Scrapper gathers metrics from an url
type Scrapper interface {
	Gather(ctx context.Context, orgID, BucketID platform.ID, url string) error
}

// NewPrometheusScraper returns a new prometheusScraper
// to fetch metrics from prometheus /metrics
func NewPrometheusScraper(s Storage) Scrapper {
	return &prometheusScraper{
		Storage: s,
	}
}

type prometheusScraper struct {
	Storage Storage
}

func (p *prometheusScraper) Gather(
	ctx context.Context,
	orgID,
	BucketID platform.ID,
	url string,
) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return p.parse(resp.Body, resp.Header)
}

func (p *prometheusScraper) parse(r io.Reader, header http.Header) error {
	var parser expfmt.TextParser

	mediatype, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return err
	}
	// Prepare output
	metricFamilies := make(map[string]*dto.MetricFamily)
	if err == nil && mediatype == "application/vnd.google.protobuf" &&
		params["encoding"] == "delimited" &&
		params["proto"] == "io.prometheus.client.MetricFamily" {
		for {
			mf := &dto.MetricFamily{}
			if _, err := pbutil.ReadDelimited(r, mf); err != nil {
				if err == io.EOF {
					break
				}
				return fmt.Errorf("reading metric family protocol buffer failed: %s", err)
			}
			metricFamilies[mf.GetName()] = mf
		}
	} else {
		metricFamilies, err = parser.TextToMetricFamilies(r)
		if err != nil {
			return fmt.Errorf("reading text format failed: %s", err)
		}
	}
	ms := make([]Metrics, 0)
	// read metrics
	for metricName, mf := range metricFamilies {
		for _, m := range mf.Metric {
			println(m.Label, metricName)
			// reading tags
			tags := makeLabels(m)
			// reading fields
			fields := make(map[string]interface{})
			if mf.GetType() == dto.MetricType_SUMMARY {
				// summary metric
				fields = makeQuantiles(m)
				fields["count"] = float64(m.GetSummary().GetSampleCount())
				fields["sum"] = float64(m.GetSummary().GetSampleSum())
			} else if mf.GetType() == dto.MetricType_HISTOGRAM {
				// histogram metric
				fields = makeBuckets(m)
				fields["count"] = float64(m.GetHistogram().GetSampleCount())
				fields["sum"] = float64(m.GetHistogram().GetSampleSum())
			} else {
				// standard metric
				fields = getNameAndValue(m)
			}
			if len(fields) > 0 {
				var t time.Time
				if m.TimestampMs != nil && *m.TimestampMs > 0 {
					t = time.Unix(0, *m.TimestampMs*1000000)
				} else {
					t = time.Now()
				}
				me := Metrics{
					Timestamp: t.UnixNano(),
					Tags:      tags,
					Fields:    fields,
					Name:      metricName,
					Type:      mf.GetType(),
				}
				ms = append(ms, me)
			}
		}

	}

	return p.Storage.Record(ms)
}

// Get labels from metric
func makeLabels(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, lp := range m.Label {
		result[lp.GetName()] = lp.GetValue()
	}
	return result
}

// Get Buckets  from histogram metric
func makeBuckets(m *dto.Metric) map[string]interface{} {
	fields := make(map[string]interface{})
	for _, b := range m.GetHistogram().Bucket {
		fields[fmt.Sprint(b.GetUpperBound())] = float64(b.GetCumulativeCount())
	}
	return fields
}

// Get name and value from metric
func getNameAndValue(m *dto.Metric) map[string]interface{} {
	fields := make(map[string]interface{})
	if m.Gauge != nil {
		if !math.IsNaN(m.GetGauge().GetValue()) {
			fields["gauge"] = float64(m.GetGauge().GetValue())
		}
	} else if m.Counter != nil {
		if !math.IsNaN(m.GetCounter().GetValue()) {
			fields["counter"] = float64(m.GetCounter().GetValue())
		}
	} else if m.Untyped != nil {
		if !math.IsNaN(m.GetUntyped().GetValue()) {
			fields["value"] = float64(m.GetUntyped().GetValue())
		}
	}
	return fields
}

// Get Quantiles from summary metric
func makeQuantiles(m *dto.Metric) map[string]interface{} {
	fields := make(map[string]interface{})
	for _, q := range m.GetSummary().Quantile {
		if !math.IsNaN(q.GetValue()) {
			fields[fmt.Sprint(q.GetQuantile())] = float64(q.GetValue())
		}
	}
	return fields
}
