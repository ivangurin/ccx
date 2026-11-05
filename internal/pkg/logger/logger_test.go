package logger_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestNew_JSONFormat(t *testing.T) {
	var buf bytes.Buffer

	log, err := logger.New(&buf, "info", "json")
	require.NoError(t, err)
	log.Info("hello", "key", "value")

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	require.Equal(t, "hello", record["msg"])
	require.Equal(t, "value", record["key"])
}

func TestNew_DevFormatIsText(t *testing.T) {
	var buf bytes.Buffer

	log, err := logger.New(&buf, "info", "dev")
	require.NoError(t, err)
	log.Info("hello", "key", "value")

	out := buf.String()
	require.Contains(t, out, "hello")
	require.Contains(t, out, "key=value")
	require.NotContains(t, strings.TrimSpace(out), "{")
}

func TestNew_ErrorLevelSuppressesInfo(t *testing.T) {
	var buf bytes.Buffer

	log, err := logger.New(&buf, "error", "json")
	require.NoError(t, err)
	log.Info("should-not-appear")
	log.Error("should-appear")

	require.NotContains(t, buf.String(), "should-not-appear")
	require.Contains(t, buf.String(), "should-appear")
}

func TestNew_InvalidLevelFails(t *testing.T) {
	_, err := logger.New(&bytes.Buffer{}, "no-such-level", "json")
	require.Error(t, err)
}

func TestNew_InvalidFormatFails(t *testing.T) {
	_, err := logger.New(&bytes.Buffer{}, "info", "no-such-format")
	require.Error(t, err)
}
