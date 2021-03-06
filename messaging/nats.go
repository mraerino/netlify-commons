package messaging

import (
	"fmt"
	"io/ioutil"
	"time"

	"github.com/nats-io/go-nats"
	stan "github.com/nats-io/go-nats-streaming"
	"github.com/netlify/netlify-commons/nconf"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var silent *logrus.Entry

func init() {
	l := logrus.New()
	l.Out = ioutil.Discard
	silent = logrus.NewEntry(l)
}

func ConfigureNatsConnection(config *nconf.NatsConfig, log logrus.FieldLogger) (*nats.Conn, error) {
	if log == nil {
		log = silent
	}
	if config == nil {
		log.Debug("Skipping nats connection because there is no config")
		return nil, nil
	}

	if err := config.LoadServerNames(); err != nil {
		return nil, errors.Wrap(err, "Failed to discover new servers")
	}

	log.WithFields(config.Fields()).Info("Going to connect to nats servers")
	nc, err := ConnectToNats(config, ErrorHandler(log), nats.MaxReconnects(-1))
	if err != nil {
		return nil, err
	}

	return nc, nil
}

func ConnectToNats(config *nconf.NatsConfig, opts ...nats.Option) (*nats.Conn, error) {
	if config.TLS != nil {
		tlsConfig, err := config.TLS.TLSConfig()
		if err != nil {
			return nil, errors.Wrap(err, "Failed to configure TLS")
		}
		if tlsConfig != nil {
			opts = append(opts, nats.Secure(tlsConfig))
		}
	}

	return nats.Connect(config.ServerString(), opts...)
}

func ConfigureNatsStreaming(config *nconf.NatsConfig, log logrus.FieldLogger) (stan.Conn, error) {
	// connect to the underlying instance
	nc, err := ConfigureNatsConnection(config, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to underlying NATS servers")
	}

	conn, err := ConnectToNatsStreaming(nc, config, log)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to NATS streaming")
	}

	return conn, nil
}

func ConnectToNatsStreaming(nc *nats.Conn, config *nconf.NatsConfig, log logrus.FieldLogger) (stan.Conn, error) {
	if config.ClusterID == "" {
		return nil, errors.New("Must provide a cluster ID to connect to streaming nats")
	}
	if config.ClientID == "" {
		config.ClientID = fmt.Sprintf("generated-%d", time.Now().Nanosecond())
		log.WithField("client_id", config.ClientID).Debug("No client ID specified, generating a random one")
	}

	// connect to the actual instance
	log.WithFields(config.Fields()).Debugf("Connecting to nats streaming cluster %s", config.ClusterID)
	return stan.Connect(config.ClusterID, config.ClientID, stan.NatsConn(nc))
}

func ErrorHandler(log logrus.FieldLogger) nats.Option {
	errLogger := log.WithField("component", "error-logger")
	handler := func(conn *nats.Conn, sub *nats.Subscription, natsErr error) {
		err := natsErr

		l := errLogger.WithFields(logrus.Fields{
			"subject":     sub.Subject,
			"group":       sub.Queue,
			"conn_status": conn.Status(),
		})

		if err == nats.ErrSlowConsumer {
			pendingMsgs, _, perr := sub.Pending()
			if perr != nil {
				err = perr
			} else {
				l = l.WithField("pending_messages", pendingMsgs)
			}
		}

		l.WithError(err).Error("Error while consuming from " + sub.Subject)
	}
	return nats.ErrorHandler(handler)
}
