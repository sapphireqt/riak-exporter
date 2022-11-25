package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/prometheus/common/version"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenAddress = flag.String(
		"web.listen-address", ":9104",
		"Address to listen on for web interface and telemetry.",
	)
	metricsPath = flag.String(
		"web.telemetry-path", "/metrics",
		"Path under which to expose metrics.",
	)
	riakURI = flag.String(
		"riak.uri", "http://localhost:8098",
		"The URI which the Riak HTTP API listens on.",
	)
)

const (
	namespace = "riak"
	exporter  = "exporter"
)

var landingPage = []byte(`<html>
<head><title>Riak exporter</title></head>
<body>
<h1>Riak exporter</h1>
<p><a href='` + *metricsPath + `'>Metrics</a></p>
</body>
</html>
`)

type Exporter struct {
	url             string
	duration, error prometheus.Gauge
	totalScrapes    prometheus.Counter
	scrapeErrors    *prometheus.CounterVec
	riakUp          prometheus.Gauge
}


func setLogger() (*logrus.Logger) {
	Logger := logrus.New()
	Logger.SetFormatter(
		&logrus.TextFormatter{
			ForceColors:     true,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339Nano,
		},
	)
	return Logger
}

func NewExporter(url string) *Exporter {
	return &Exporter{
		url: url,
		duration: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: exporter,
			Name:      "last_scrape_duration_seconds",
			Help:      "Duration of the last scrape of metrics from Riak.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: exporter,
			Name:      "scrapes_total",
			Help:      "Total number of times Riak was scraped for metrics.",
		}),

		error: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: exporter,
			Name:      "last_scrape_error",
			Help:      "Whether the last scrape of metrics from Riak resulted in an error (1 for error, 0 for success).",
		}),
		riakUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Whether the Riak node is up.",
		}),
	}
}
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	metricCh := make(chan prometheus.Metric)
	doneCh := make(chan struct{})

	go func() {
		for m := range metricCh {
			ch <- m.Desc()
		}

		close(doneCh)
	}()

	e.Collect(metricCh)
	close(metricCh)
	<-doneCh
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.scrape(ch)
	ch <- e.duration
	ch <- e.totalScrapes
	ch <- e.error
	ch <- e.riakUp
}

func (e *Exporter) scrape(ch chan<- prometheus.Metric) error {
	e.totalScrapes.Inc()
	var err error

	defer func(begun time.Time) {
		e.duration.Set(time.Since(begun).Seconds())
		if err == nil {
			e.error.Set(0)
		} else {
			e.error.Set(1)
		}
	}(time.Now())

	pingResponse, err := http.Get(*riakURI + "/ping")
	if err != nil {
		return err
	}

	if pingResponse.StatusCode == 200 {
		e.riakUp.Set(1)
	} else {
		e.riakUp.Set(0)
		return errors.New("Riak node is down")
	}

	statsResponse, err := http.Get(*riakURI + "/stats")
	if err != nil {
		return nil
	}

	defer func() {
		_ = pingResponse.Body.Close()
		_ = statsResponse.Body.Close()
	}()

	if statsResponse.StatusCode != 200 {
		return errors.New("Error fetching the stats for the Riak node")
	}

	data, err := ioutil.ReadAll(statsResponse.Body)
	if err != nil {
		return err
	}

	var f interface{}
	err = json.Unmarshal(data, &f)

	if err != nil {
		return err
	}

	m := f.(map[string]interface{})

	for metricName, metricValue := range m {
		description := prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", metricName),
			metricName,
			nil,
			nil,
		)

		if value, ok := metricValue.(float64); ok {
			ch <- prometheus.MustNewConstMetric(description, prometheus.GaugeValue, value)
		}
	}
	return nil
}

func init() {
	prometheus.MustRegister(version.NewCollector("riak_exporter"))
}

func main() {
	Logger := setLogger()
	flag.Parse()

	exporter := NewExporter(*riakURI + "/stats")
	prometheus.MustRegister(exporter)

	http.Handle(*metricsPath, promhttp.Handler())

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write(landingPage)
		if err != nil {
			Logger.Errorln(err)
			return
		}

	})

	Logger.Infoln("Listening on", *listenAddress)
	Logger.Fatal(http.ListenAndServe(*listenAddress, nil))
}
