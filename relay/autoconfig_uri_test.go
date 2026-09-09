package relay

import (
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	c "github.com/launchdarkly/ld-relay/v9/config"

	"github.com/launchdarkly/go-configtypes"
	helpers "github.com/launchdarkly/go-test-helpers/v3"
	"github.com/launchdarkly/go-test-helpers/v3/httphelpers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests verify which service the auto-configuration stream connects to: by default it uses
// the same stream URI as the SDK streams, but AutoConfig.URI overrides it if set.

const autoConfigStreamRequestPath = "/relay_auto_config"

func withTwoStreamServers(t *testing.T, action func(mainServer, autoConfigServer *httptest.Server,
	mainRequestsCh, autoConfigRequestsCh <-chan httphelpers.HTTPRequestInfo)) {
	t.Helper()

	mainStreamHandler, mainStream := httphelpers.SSEHandler(nil)
	defer mainStream.Close()
	mainRecordingHandler, mainRequestsCh := httphelpers.RecordingHandler(mainStreamHandler)

	autoConfigStreamHandler, autoConfigStream := httphelpers.SSEHandler(nil)
	defer autoConfigStream.Close()
	autoConfigRecordingHandler, autoConfigRequestsCh := httphelpers.RecordingHandler(autoConfigStreamHandler)

	httphelpers.WithServer(mainRecordingHandler, func(mainServer *httptest.Server) {
		httphelpers.WithServer(autoConfigRecordingHandler, func(autoConfigServer *httptest.Server) {
			action(mainServer, autoConfigServer, mainRequestsCh, autoConfigRequestsCh)
		})
	})
}

func TestAutoConfigStreamUsesStreamURIIfAutoConfigURIIsNotSet(t *testing.T) {
	withTwoStreamServers(t, func(mainServer, autoConfigServer *httptest.Server,
		mainRequestsCh, autoConfigRequestsCh <-chan httphelpers.HTTPRequestInfo) {
		streamURI, err := configtypes.NewOptURLAbsoluteFromString(mainServer.URL)
		require.NoError(t, err)

		config := c.Config{
			Main:       c.MainConfig{StreamURI: streamURI},
			AutoConfig: c.AutoConfigConfig{Key: "x"},
		}
		relay, err := NewRelay(config, slog.Default(), nil)
		require.NoError(t, err)
		defer relay.Close()

		request := helpers.RequireValue(t, mainRequestsCh, time.Second*5,
			"timed out waiting for auto-config request to the stream URI")
		assert.Equal(t, autoConfigStreamRequestPath, request.Request.URL.Path)

		if !helpers.AssertNoMoreValues(t, autoConfigRequestsCh, time.Millisecond*100,
			"unexpectedly made a request to the unused server") {
			t.FailNow()
		}
	})
}

func TestAutoConfigURIOverridesStreamURIForAutoConfigStream(t *testing.T) {
	withTwoStreamServers(t, func(mainServer, autoConfigServer *httptest.Server,
		mainRequestsCh, autoConfigRequestsCh <-chan httphelpers.HTTPRequestInfo) {
		streamURI, err := configtypes.NewOptURLAbsoluteFromString(mainServer.URL)
		require.NoError(t, err)
		autoConfigURI, err := configtypes.NewOptURLAbsoluteFromString(autoConfigServer.URL)
		require.NoError(t, err)

		config := c.Config{
			Main:       c.MainConfig{StreamURI: streamURI},
			AutoConfig: c.AutoConfigConfig{Key: "x", URI: autoConfigURI},
		}
		relay, err := NewRelay(config, slog.Default(), nil)
		require.NoError(t, err)
		defer relay.Close()

		request := helpers.RequireValue(t, autoConfigRequestsCh, time.Second*5,
			"timed out waiting for auto-config request to the auto-config URI")
		assert.Equal(t, autoConfigStreamRequestPath, request.Request.URL.Path)

		if !helpers.AssertNoMoreValues(t, mainRequestsCh, time.Millisecond*100,
			"unexpectedly made a request to the stream URI") {
			t.FailNow()
		}
	})
}
