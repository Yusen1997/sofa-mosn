package healthcheck

import (
	"strings"
	"strconv"
	"gitlab.alipay-inc.com/afe/mosn/pkg/types"
	"gitlab.alipay-inc.com/afe/mosn/pkg/stream"
	"gitlab.alipay-inc.com/afe/mosn/pkg/protocol"
	"gitlab.alipay-inc.com/afe/mosn/pkg/api/v2"
)

type httpHealthChecker struct {
	healthChecker
	checkPath   string
	serviceName string
}

func NewHttpHealthCheck(config v2.HealthCheck) types.HealthChecker {
	hc := NewHealthCheck(config)
	hhc := &httpHealthChecker{
		healthChecker: *hc,
		checkPath:     config.CheckPath,
	}

	if config.ServiceName != "" {
		hhc.serviceName = config.ServiceName
	}

	return hhc
}

func (c *httpHealthChecker) newSession(host types.Host) types.HealthCheckSession {
	hcs := NewHealthCheckSession(&c.healthChecker, host)

	return &httpHealthCheckSession{
		healthChecker:      c,
		healthCheckSession: *hcs,
	}
}

func (c *httpHealthChecker) createCodecClient(data types.CreateConnectionData) stream.CodecClient {
	return stream.NewCodecClient(protocol.Http2, data.Connection, data.HostInfo)
}

// types.StreamDecoder
type httpHealthCheckSession struct {
	healthCheckSession

	client          stream.CodecClient
	requestEncoder  types.StreamEncoder
	responseHeaders map[string]string
	healthChecker   *httpHealthChecker
	expectReset     bool
}

func (s *httpHealthCheckSession) OnDecodeHeaders(headers map[string]string, endStream bool) {
	s.responseHeaders = headers

	if endStream {
		s.onResponseComplete()
	}
}

func (s *httpHealthCheckSession) OnDecodeData(data types.IoBuffer, endStream bool) {
	if endStream {
		s.onResponseComplete()
	}
}

func (s *httpHealthCheckSession) OnDecodeTrailers(trailers map[string]string) {
	s.onResponseComplete()
}

func (s *httpHealthCheckSession) onInterval() {
	if s.client == nil {
		connData := s.host.CreateConnection()
		s.client = s.healthChecker.createCodecClient(connData)
		s.expectReset = false
	}

	s.requestEncoder = s.client.NewStream(0, s)
	s.requestEncoder.GetStream().AddCallbacks(s)

	reqHeaders := map[string]string{
		types.HeaderMethod: "GET",
		types.HeaderHost:   s.healthChecker.cluster.Info().Name(),
		types.HeaderPath:   s.healthChecker.checkPath,
	}

	s.requestEncoder.EncodeHeaders(reqHeaders, true)
	s.requestEncoder = nil
}

func (s *httpHealthCheckSession) onTimeout() {
	s.expectReset = true
	s.client.Close()
	s.client = nil
}

func (s *httpHealthCheckSession) onResponseComplete() {
	if s.isHealthCheckSucceeded() {
		s.handleSuccess()
	} else {
		s.handleFailure(types.FailureActive)
	}

	if conn, ok := s.responseHeaders["connection"]; ok {
		if strings.Compare(strings.ToLower(conn), "close") == 0 {
			s.client.Close()
			s.client = nil
		}
	}

	s.responseHeaders = nil
}

func (s *httpHealthCheckSession) isHealthCheckSucceeded() bool {
	if status, ok := s.responseHeaders[types.HeaderStatus]; ok {
		statusCode, _ := strconv.Atoi(status)

		return statusCode == 200
	}

	return true
}

func (s *httpHealthCheckSession) OnResetStream(reason types.StreamResetReason) {
	if s.expectReset {
		return
	}

	s.handleFailure(types.FailureNetwork)
}

func (s *httpHealthCheckSession) OnAboveWriteBufferHighWatermark() {}

func (s *httpHealthCheckSession) OnBelowWriteBufferLowWatermark() {}
