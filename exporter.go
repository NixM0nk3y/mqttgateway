package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

var mutex sync.RWMutex

type mqttExporter struct {
	client         mqtt.Client
	versionDesc    *prometheus.Desc
	connectDesc    *prometheus.Desc
	metrics        map[string]*prometheus.GaugeVec   // hold the metrics collected
	counterMetrics map[string]*prometheus.CounterVec // hold the metrics collected
	metricsLabels  map[string][]string               // holds the labels set for each metric to be able to invalidate them
}

func newMQTTExporter() *mqttExporter {
	// create a MQTT client
	options := mqtt.NewClientOptions()

        // keep a active connection
        options.SetKeepAlive(3 * time.Second)

        // maintain subs
        options.SetCleanSession(false)

	log.Infof("Connecting to %v", *brokerAddress)
	options.AddBroker(*brokerAddress)
	if *username != "" {
		options.SetUsername(*username)
	}
	if *password != "" {
		options.SetPassword(*password)
	}
	if *clientID != "" {
		options.SetClientID(*clientID)
	}
	m := mqtt.NewClient(options)
	if token := m.Connect(); token.Wait() && token.Error() != nil {
		log.Fatal(token.Error())
	}

	// create an exporter
	c := &mqttExporter{
		client: m,
		versionDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "build", "info"),
			"Build info of this instance",
			nil,
			prometheus.Labels{"version": version, "commithash": buildHash, "builddate": buildDate}),
		connectDesc: prometheus.NewDesc(
			prometheus.BuildFQName(progname, "mqtt", "connected"),
			"Is the exporter connected to mqtt broker",
			nil,
			nil),
	}

	c.metrics = make(map[string]*prometheus.GaugeVec)
	c.counterMetrics = make(map[string]*prometheus.CounterVec)
	c.metricsLabels = make(map[string][]string)

	m.Subscribe(*topic, 2, c.receiveMessage())

	return c
}

func (e *mqttExporter) Describe(ch chan<- *prometheus.Desc) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- e.versionDesc
	ch <- e.connectDesc
	for _, m := range e.counterMetrics {
		m.Describe(ch)
	}
	for _, m := range e.metrics {
		m.Describe(ch)
	}
}

func (e *mqttExporter) Collect(ch chan<- prometheus.Metric) {
	mutex.RLock()
	defer mutex.RUnlock()
	ch <- prometheus.MustNewConstMetric(
		e.versionDesc,
		prometheus.GaugeValue,
		1,
	)
	connected := 0.
	if e.client.IsConnected() {
		connected = 1.
	}
	ch <- prometheus.MustNewConstMetric(
		e.connectDesc,
		prometheus.GaugeValue,
		connected,
	)
	for _, m := range e.counterMetrics {
		m.Collect(ch)
	}
	for _, m := range e.metrics {
		m.Collect(ch)
	}
}
func (e *mqttExporter) receiveMessage() func(mqtt.Client, mqtt.Message) {
	return func(c mqtt.Client, m mqtt.Message) {
		mutex.Lock()
		defer mutex.Unlock()

		t := m.Topic()
		t = strings.TrimPrefix(m.Topic(), *prefix)
		t = strings.TrimPrefix(t, "/")
		parts := strings.Split(t, "/")

		if len(parts)%2 == 0 {
			log.Warnf("Invalid topic: %s: odd number of levels, ignoring", t)
			return
		}
		metricName := parts[len(parts)-1]
		pushedMetricName := fmt.Sprintf("mqtt_%s_last_pushed_timestamp", metricName)
		countMetricName := fmt.Sprintf("mqtt_%s_push_total", metricName)
		metricLabels := parts[:len(parts)-1]

		var labels []string
		labelValues := prometheus.Labels{}
		log.Debugf("Metric name: %v", metricName)
		for i, l := range metricLabels {
			if i%2 == 1 {
				continue
			}
			labels = append(labels, l)
			labelValues[l] = metricLabels[i+1]
		}

		invalidate := false
		if _, ok := e.metricsLabels[metricName]; ok {
			l := e.metricsLabels[metricName]
			if !compareLabels(l, labels) {
				log.Warnf("Label names are different for %s: %v and %v, invalidating existing metric", metricName, l, labels)
				prometheus.Unregister(e.metrics[metricName])
				invalidate = true
			}
		}
		e.metricsLabels[metricName] = labels
		if _, ok := e.metrics[metricName]; ok && !invalidate {
			log.Debugf("Metric already exists")
		} else {
			log.Debugf("Creating new metric: %s %v", metricName, labels)
			e.metrics[metricName] = prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: metricName,
					Help: "Metric pushed via MQTT",
				},
				labels,
			)
			e.counterMetrics[countMetricName] = prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: countMetricName,
					Help: fmt.Sprintf("Number of times %s was pushed via MQTT", metricName),
				},
				labels,
			)
			e.metrics[pushedMetricName] = prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: pushedMetricName,
					Help: fmt.Sprintf("Last time %s was pushed via MQTT", metricName),
				},
				labels,
			)
		}

		// data comes in graphite format: $value $timestamp  i.e.
		// 142168064.000000 1587222782
		//
		data := strings.Split(string(m.Payload()), " ")

		if s, err := strconv.ParseFloat(data[0], 64); err == nil {
			e.metrics[metricName].With(labelValues).Set(s)
			e.metrics[pushedMetricName].With(labelValues).SetToCurrentTime()
			e.counterMetrics[countMetricName].With(labelValues).Inc()
		}
	}
}

func compareLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
