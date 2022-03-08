// Package kafka handles flow exports to Kafka.
package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"golang.org/x/time/rate"
	"gopkg.in/tomb.v2"

	"akvorado/daemon"
	"akvorado/reporter"
)

// Component represents the Kafka exporter.
type Component struct {
	r      *reporter.Reporter
	d      *Dependencies
	t      tomb.Tomb
	config Configuration

	kafkaConfig         *sarama.Config
	kafkaProducer       sarama.AsyncProducer
	createKafkaProducer func() (sarama.AsyncProducer, error)
	metrics             metrics
}

// Dependencies define the dependencies of the Kafka exporter.
type Dependencies struct {
	Daemon daemon.Component
}

// New creates a new HTTP component.
func New(reporter *reporter.Reporter, configuration Configuration, dependencies Dependencies) (*Component, error) {
	// Build Kafka configuration
	kafkaConfig := sarama.NewConfig()
	kafkaConfig.Producer.MaxMessageBytes = configuration.MaxMessageBytes
	kafkaConfig.Producer.Compression = sarama.CompressionCodec(configuration.CompressionCodec)
	kafkaConfig.Producer.Return.Successes = false
	kafkaConfig.Producer.Return.Errors = true
	kafkaConfig.Producer.Flush.Bytes = configuration.FlushBytes
	kafkaConfig.Producer.Flush.Frequency = configuration.FlushInterval
	kafkaConfig.Producer.Partitioner = sarama.NewHashPartitioner
	if configuration.UseTLS {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("cannot initialize TLS: %w", err)
		}
		kafkaConfig.Net.TLS.Enable = true
		kafkaConfig.Net.TLS.Config = &tls.Config{RootCAs: rootCAs}
	}
	if err := kafkaConfig.Validate(); err != nil {
		return nil, fmt.Errorf("cannot validate Kafka configuration: %w", err)
	}

	c := Component{
		r:      reporter,
		d:      &dependencies,
		config: configuration,

		kafkaConfig: kafkaConfig,
	}
	c.initMetrics()
	c.createKafkaProducer = func() (sarama.AsyncProducer, error) {
		return sarama.NewAsyncProducer(c.config.Brokers, c.kafkaConfig)
	}
	c.d.Daemon.Track(&c.t, "kafka")
	return &c, nil
}

// Start starts the Kafka component.
func (c *Component) Start() error {
	// Create producer
	kafkaProducer, err := c.createKafkaProducer()
	if err != nil {
		c.r.Err(err).
			Str("brokers", strings.Join(c.config.Brokers, ",")).
			Msg("unable to create async producer")
		return fmt.Errorf("unable to create Kafka async producer: %w", err)
	}
	c.kafkaProducer = kafkaProducer

	// Main loop
	c.t.Go(func() error {
		defer kafkaProducer.Close()
		defer c.kafkaConfig.MetricRegistry.UnregisterAll()
		errLimiter := rate.NewLimiter(rate.Every(10*time.Second), 3)
		for {
			select {
			case <-c.t.Dying():
				c.r.Debug().Msg("stop error logger")
				return nil
			case msg := <-kafkaProducer.Errors():
				c.metrics.errors.WithLabelValues(msg.Error()).Inc()
				if errLimiter.Allow() {
					c.r.Err(msg.Err).
						Str("topic", msg.Msg.Topic).
						Int64("offset", msg.Msg.Offset).
						Int32("partition", msg.Msg.Partition).
						Msg("Kafka producer error")
				}
			}
		}
	})
	return nil
}

// Stop stops the Kafka component
func (c *Component) Stop() error {
	c.t.Kill(nil)
	return c.t.Wait()
}

// Send a message to Kafka.
func (c *Component) Send(host string, payload []byte) error {
	c.metrics.bytesSent.WithLabelValues(host).Add(float64(len(payload)))
	c.metrics.messagesSent.WithLabelValues(host).Inc()
	c.kafkaProducer.Input() <- &sarama.ProducerMessage{
		Topic: c.config.Topic,
		Key:   sarama.StringEncoder(host),
		Value: sarama.ByteEncoder(payload),
	}
	return nil
}