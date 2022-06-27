package utils

import (
	"bytes"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func metricsConvert(rawData []byte) (map[string]*dto.MetricFamily, error) {
	parser := &expfmt.TextParser{}
	origFamilies, err := parser.TextToMetricFamilies(bytes.NewReader(rawData))
	if err != nil {
		return nil, err
	}

	return origFamilies, nil
}

func RetrieveTestedMetricValue(rawData []byte, testedMetricName string, testedMetricType dto.MetricType) ([]*dto.Metric, error) {
	if metricFamilies, err := metricsConvert(rawData); err != nil {
		return nil, err
	} else {
		for name, metricFamily := range metricFamilies {
			if name == testedMetricName && metricFamily.GetType() == testedMetricType {
				return metricFamily.GetMetric(), nil
			}
		}
		return nil, nil
	}
}
