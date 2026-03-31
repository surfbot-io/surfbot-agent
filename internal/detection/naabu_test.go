package detection

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
		hasError bool
	}{
		{"80,443", []int{80, 443}, false},
		{"1-5", []int{1, 2, 3, 4, 5}, false},
		{"top100", Top100Ports, false},
		{"", Top100Ports, false},
		{"80, 443, 8080", []int{80, 443, 8080}, false},
		{"invalid", nil, true},
		{"0", nil, true},
		{"65536", nil, true},
		{"100-50", nil, true},
	}

	for _, tc := range tests {
		ports, err := ParsePorts(tc.input)
		if tc.hasError {
			assert.Error(t, err, "input: %q", tc.input)
		} else {
			require.NoError(t, err, "input: %q", tc.input)
			assert.Equal(t, tc.expected, ports, "input: %q", tc.input)
		}
	}
}

func TestNaabuScanLocalhost(t *testing.T) {
	// Start a TCP test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	n := NewNaabuTool()
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": portStr},
	})
	require.NoError(t, err)

	// Should find our test port open
	require.Len(t, result.Assets, 1)
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%s/tcp", portStr), result.Assets[0].Value)
}

func TestNaabuClosedPort(t *testing.T) {
	n := NewNaabuTool()
	// Port 1 is almost certainly closed on localhost
	result, err := n.Run(context.Background(), []string{"127.0.0.1"}, RunOptions{
		ExtraArgs: map[string]string{"ports": "1", "timeout": "1"},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Assets)
}
