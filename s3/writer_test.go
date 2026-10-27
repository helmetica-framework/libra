package s3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEndpoint_HTTPS(t *testing.T) {
	host, secure, err := parseEndpoint("https://s3.example.com")

	require.NoError(t, err)
	assert.Equal(t, "s3.example.com", host)
	assert.True(t, secure)
}

func TestParseEndpoint_HTTPWithPort(t *testing.T) {
	host, secure, err := parseEndpoint("http://minio:9000")

	require.NoError(t, err)
	assert.Equal(t, "minio:9000", host)
	assert.False(t, secure)
}

func TestParseEndpoint_NoScheme(t *testing.T) {
	_, _, err := parseEndpoint("s3.example.com")

	assert.Error(t, err)
}

func TestParseEndpoint_UnknownScheme(t *testing.T) {
	_, _, err := parseEndpoint("ftp://s3.example.com")

	assert.Error(t, err)
}

func TestParseEndpoint_Empty(t *testing.T) {
	_, _, err := parseEndpoint("")

	assert.Error(t, err)
}

func TestParseEndpoint_NoHost(t *testing.T) {
	_, _, err := parseEndpoint("https://")

	assert.Error(t, err)
}

func TestNew_RejectsBadEndpoint(t *testing.T) {
	_, err := New(context.Background(), "s3.example.com", "billing")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3.example.com")
}

func TestNew_RejectsEmptyBucket(t *testing.T) {
	_, err := New(context.Background(), "https://s3.example.com", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}
