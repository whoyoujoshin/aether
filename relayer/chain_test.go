package relayer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Public endpoints serve gRPC over TLS on 443; a node's own port is
// plaintext.
func TestGRPCDial(t *testing.T) {
	for _, c := range []struct{ addr, target, security string }{
		{"https://grpc.osmotest5.osmosis.zone", "grpc.osmotest5.osmosis.zone:443", "tls"},
		{"https://grpc.osmotest5.osmosis.zone/", "grpc.osmotest5.osmosis.zone:443", "tls"},
		{"https://grpc.example.com:9443", "grpc.example.com:9443", "tls"},
		{"grpc.157-245-252-221.sslip.io:443", "grpc.157-245-252-221.sslip.io:443", "tls"},
		{"127.0.0.1:9090", "127.0.0.1:9090", "insecure"},
		{"http://157.245.252.221:9090", "157.245.252.221:9090", "insecure"},
	} {
		target, creds := grpcDial(c.addr)
		require.Equal(t, c.target, target, c.addr)
		require.Equal(t, c.security, creds.Info().SecurityProtocol, c.addr)
	}
}
