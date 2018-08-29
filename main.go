package main

import (
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/RobustPerception/azure_metrics_exporter/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"

	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

var (
	sc = &config.SafeConfig{
		C: &config.Config{},
	}
	ac                    = NewAzureClient()
	configFile            = kingpin.Flag("config.file", "Azure exporter configuration file.").Default("azure.yml").String()
	listenAddress         = kingpin.Flag("web.listen-address", "The address to listen on for HTTP requests.").Default(":9276").String()
	listMetricDefinitions = kingpin.Flag("list.definitions", "Whether or not to list available metric definitions for the given resources.").Bool()
	invalidMetricChars    = regexp.MustCompile("[^a-zA-Z0-9_:]")
)

func init() {
	prometheus.MustRegister(version.NewCollector("azure_exporter"))
}

// Collector generic collector type
type Collector struct{}

// Describe implemented with dummy data to satisfy interface.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc("dummy", "dummy", nil, nil)
}

// Collect - collect results from Azure Montior API and create Prometheus metrics.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	// Get metric values for all defined metrics
	var metricValueData AzureMetricValueResponse
	for _, target := range sc.C.Targets {
		metrics := []string{}
		for _, metric := range target.Metrics {
			metrics = append(metrics, metric.Name)
		}
		metricsStr := strings.Join(metrics, ",")
		metricValueData = ac.getMetricValue(metricsStr, target.Resource)
		if metricValueData.Value == nil {
			log.Printf("Metric %v not found at target %v\n", metricsStr, target.Resource)
			continue
		}
		if len(metricValueData.Value[0].Timeseries[0].Data) == 0 {
			log.Printf("No metric data returned for metric %v at target %v\n", metricsStr, target.Resource)
			continue
		}

		for _, value := range metricValueData.Value {
			// Ensure Azure metric names conform to Prometheus metric name conventions
			metricName := strings.Replace(value.Name.Value, " ", "_", -1)
			metricName = strings.ToLower(metricName + "_" + value.Unit)
			metricName = strings.Replace(metricName, "/", "_per_", -1)
			metricName = invalidMetricChars.ReplaceAllString(metricName, "_")
			metricValue := value.Timeseries[0].Data[len(value.Timeseries[0].Data)-1]
			labels, values := CreateResourceLabels(value.ID)

			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(metricName+"_total", "", labels, nil),
				prometheus.GaugeValue,
				metricValue.Total,
                values...,
			)

			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(metricName+"_average", "", labels, nil),
				prometheus.GaugeValue,
				metricValue.Average,
                values...,
			)

			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(metricName+"_min", "", labels, nil),
				prometheus.GaugeValue,
				metricValue.Minimum,
                values...,
			)

			ch <- prometheus.MustNewConstMetric(
				prometheus.NewDesc(metricName+"_max", "", labels, nil),
				prometheus.GaugeValue,
				metricValue.Maximum,
                values...,
			)
		}
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	registry := prometheus.NewRegistry()
	collector := &Collector{}
	registry.MustRegister(collector)
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func main() {
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	log.Printf("azure_metrics_exporter listening on port %v", *listenAddress)
	if err := sc.ReloadConfig(*configFile); err != nil {
		log.Fatalf("Error loading config: %v", err)
		os.Exit(1)
	}

	ac.getAccessToken()

	// Print list of available metric definitions for each resource to console if specified.
	if *listMetricDefinitions {
		results := ac.getMetricDefinitions()
		for k, v := range results {
			log.Printf("Resource: %s\n\nAvailable Metrics:\n", strings.Split(k, "/")[6])
			for _, r := range v.MetricDefinitionResponses {
				log.Printf("- %s\n", r.Name.Value)
			}
		}
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head>
            <title>Azure Exporter</title>
            </head>
            <body>
            <h1>Azure Exporter</h1>
						<p><a href="/metrics">Metrics</a></p>
            </body>
            </html>`))
	})

	http.HandleFunc("/metrics", handler)
	if err := http.ListenAndServe(*listenAddress, nil); err != nil {
		log.Fatalf("Error starting HTTP server: %v", err)
		os.Exit(1)
	}

}
