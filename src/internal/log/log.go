package log

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pachyderm/pachyderm/v2/src/internal/errors"
	"github.com/sirupsen/logrus"
)

// FormatterFunc is a type alias for a function that satisfies logrus'
// `Formatter` interface
type FormatterFunc func(entry *logrus.Entry) ([]byte, error)

// Format proxies the closure in order to satisfy `logrus.Formatter`'s
// interface.
func (f FormatterFunc) Format(entry *logrus.Entry) ([]byte, error) {
	return f(entry)
}

// Pretty formats a logrus entry like so:
// ```
// 2019-02-11T16:02:02Z INFO pfs.API.InspectRepo {"request":{"repo":{"name":"images"}}} []
// ```
func Pretty(entry *logrus.Entry) ([]byte, error) {
	serialized := []byte(
		fmt.Sprintf(
			"%v %v ",
			entry.Time.Format(time.RFC3339),
			strings.ToUpper(entry.Level.String()),
		),
	)
	if entry.Data["service"] != nil && entry.Data["method"] != nil {
		serialized = append(serialized, []byte(fmt.Sprintf("%v.%v ", entry.Data["service"], entry.Data["method"]))...)
		// TODO: seems like a bad idea to modify the log statement data
		delete(entry.Data, "service")
		delete(entry.Data, "method")
	}
	if len(entry.Data) > 0 {
		if entry.Data["duration"] != nil {
			entry.Data["duration"] = entry.Data["duration"].(time.Duration).Seconds()
		}
		data, err := json.Marshal(entry.Data)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to marshal fields to JSON")
		}
		serialized = append(serialized, data...)
		serialized = append(serialized, ' ')
	}

	serialized = append(serialized, []byte(entry.Message)...)
	serialized = append(serialized, '\n')
	return serialized, nil
}

// GRPCLogWriter proxies gRPC and etcd-produced log messages to a logrus
// logger. Because it implements `io.Writer`, it could be used anywhere where
// `io.Writer`s are used, but it has some logic specifically designed to
// handle gRPC-formatted logs.
type GRPCLogWriter struct {
	logger *logrus.Logger
	source string
}

// NewGRPCLogWriter creates a new GRPC log writer. `logger` specifies the
// underlying logger, and `source` specifies where these logs are coming from;
// it is added as a entry field for all log messages.
func NewGRPCLogWriter(logger *logrus.Logger, source string) *GRPCLogWriter {
	return &GRPCLogWriter{
		logger: logger,
		source: source,
	}
}

// Write allows `GRPCInfoWriter` to implement the `io.Writer` interface. This
// will take gRPC logs, which look something like this:
// ```
// INFO: 2019/02/18 12:21:54 ClientConn switching balancer to "pick_first"
// ```
// strip out redundant content, and print the message at the appropriate log
// level in logrus. Any parse errors of the log message will be reported in
// logrus as well.
func (l *GRPCLogWriter) Write(p []byte) (int, error) {
	parts := strings.SplitN(string(p), " ", 4)
	entry := l.logger.WithField("source", l.source)

	if len(parts) == 4 {
		// parts[1] and parts[2] contain the date and time, but logrus already
		// adds this under the `time` entry field, so it's not needed (though
		// the time will presumably be marginally ahead of the original log
		// message)
		level := parts[0]
		message := strings.TrimSpace(parts[3])

		if level == "INFO:" {
			entry.Info(message)
		} else if level == "ERROR:" {
			entry.Error(message)
		} else if level == "WARNING:" {
			entry.Warning(message)
		} else if level == "FATAL:" {
			// no need to call fatal ourselves because gRPC will exit the
			// process
			entry.Error(message)
		} else {
			entry.Error(message)
			entry.Errorf("entry had unknown log level prefix: '%s'; this is a bug, please report it along with the previous log entry", level)
		}
	} else {
		// can't format the message -- just display the contents
		entry := l.logger.WithFields(logrus.Fields{
			"source": l.source,
		})
		entry.Error(p)
		entry.Error("entry had unexpected format; this is a bug, please report it along with the previous log entry")
	}

	return len(p), nil
}
