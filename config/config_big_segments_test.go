package config

import (
	"log/slog"
	"testing"

	ct "github.com/launchdarkly/go-configtypes"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBigSegmentSyncEnabledResolution(t *testing.T) {
	for _, p := range []struct {
		name     string
		main     ct.OptBool
		env      ct.OptBool
		expected bool
	}{
		{name: "unset everywhere defaults to enabled", main: ct.OptBool{}, env: ct.OptBool{}, expected: true},
		{name: "main-level disable, no override", main: ct.NewOptBool(false), env: ct.OptBool{}, expected: false},
		{name: "main-level enable, no override", main: ct.NewOptBool(true), env: ct.OptBool{}, expected: true},
		{name: "environment opts back in to main-level disable", main: ct.NewOptBool(false), env: ct.NewOptBool(true), expected: true},
		{name: "environment disables while main is unset", main: ct.OptBool{}, env: ct.NewOptBool(false), expected: false},
		{name: "environment disables while main enables", main: ct.NewOptBool(true), env: ct.NewOptBool(false), expected: false},
		{name: "environment redundantly disables", main: ct.NewOptBool(false), env: ct.NewOptBool(false), expected: false},
	} {
		t.Run(p.name, func(t *testing.T) {
			allConfig := Config{Main: MainConfig{EnableBigSegmentSync: p.main}}
			envConfig := EnvConfig{EnableBigSegmentSync: p.env}
			assert.Equal(t, p.expected, BigSegmentSyncEnabled(allConfig, envConfig))
		})
	}
}

// TestBigSegmentSyncEnabledByDefault pins the headline behavior: a configuration that says nothing
// at all about big segment synchronization synchronizes.
func TestBigSegmentSyncEnabledByDefault(t *testing.T) {
	assert.True(t, BigSegmentSyncEnabled(Config{}, EnvConfig{}))
}

// TestEnableBigSegmentSyncFromEnvironment confirms the option is readable on its own, without the
// rest of the "all base properties" configuration around it.
func TestEnableBigSegmentSyncFromEnvironment(t *testing.T) {
	withEnvironment(map[string]string{
		"ENABLE_BIG_SEGMENT_SYNC":            "false",
		"LD_ENV_earth":                       "earth-sdk",
		"LD_ENABLE_BIG_SEGMENT_SYNC_earth":   "true",
		"LD_ENV_krypton":                     "krypton-sdk",
		"LD_ENABLE_BIG_SEGMENT_SYNC_krypton": "false",
		"LD_ENV_mars":                        "mars-sdk",
	}, func() {
		var c Config
		require.NoError(t, LoadConfigFromEnvironment(&c, slog.Default()))
		assert.Equal(t, ct.NewOptBool(false), c.Main.EnableBigSegmentSync)
		assert.True(t, BigSegmentSyncEnabled(c, *c.Environment["earth"]))
		assert.False(t, BigSegmentSyncEnabled(c, *c.Environment["krypton"]))
		// mars says nothing, so it inherits the main-level disable.
		assert.False(t, BigSegmentSyncEnabled(c, *c.Environment["mars"]))
	})
}

func TestBigSegmentSyncURIResolution(t *testing.T) {
	baseURI := newOptURLAbsoluteMustBeValid("http://base")
	streamURI := newOptURLAbsoluteMustBeValid("http://stream")
	bsBaseURI := newOptURLAbsoluteMustBeValid("http://bigsegmentbase")
	bsStreamURI := newOptURLAbsoluteMustBeValid("http://bigsegmentstream")

	t.Run("both fall back to the general URIs", func(t *testing.T) {
		c := Config{Main: MainConfig{BaseURI: baseURI, StreamURI: streamURI}}
		assert.Equal(t, "http://base", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://stream", BigSegmentSyncStreamURI(c))
	})

	t.Run("both overrides take precedence", func(t *testing.T) {
		c := Config{Main: MainConfig{
			BaseURI: baseURI, StreamURI: streamURI,
			BigSegmentBaseURI: bsBaseURI, BigSegmentStreamURI: bsStreamURI,
		}}
		assert.Equal(t, "http://bigsegmentbase", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://bigsegmentstream", BigSegmentSyncStreamURI(c))
	})

	// The two are independent, so overriding one must not disturb the other.
	t.Run("base override alone", func(t *testing.T) {
		c := Config{Main: MainConfig{BaseURI: baseURI, StreamURI: streamURI, BigSegmentBaseURI: bsBaseURI}}
		assert.Equal(t, "http://bigsegmentbase", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://stream", BigSegmentSyncStreamURI(c))
	})

	t.Run("stream override alone", func(t *testing.T) {
		c := Config{Main: MainConfig{BaseURI: baseURI, StreamURI: streamURI, BigSegmentStreamURI: bsStreamURI}}
		assert.Equal(t, "http://base", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://bigsegmentstream", BigSegmentSyncStreamURI(c))
	})

	t.Run("overrides apply even with no general URIs set", func(t *testing.T) {
		c := Config{Main: MainConfig{BigSegmentBaseURI: bsBaseURI, BigSegmentStreamURI: bsStreamURI}}
		assert.Equal(t, "http://bigsegmentbase", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://bigsegmentstream", BigSegmentSyncStreamURI(c))
	})
}

func TestBigSegmentURIsFromEnvironment(t *testing.T) {
	withEnvironment(map[string]string{
		"BASE_URI":               "http://base",
		"STREAM_URI":             "http://stream",
		"BIG_SEGMENT_BASE_URI":   "http://bigsegmentbase",
		"BIG_SEGMENT_STREAM_URI": "http://bigsegmentstream",
	}, func() {
		var c Config
		require.NoError(t, LoadConfigFromEnvironment(&c, slog.Default()))
		assert.Equal(t, "http://bigsegmentbase", c.Main.BigSegmentBaseURI.String())
		assert.Equal(t, "http://bigsegmentstream", c.Main.BigSegmentStreamURI.String())
		assert.Equal(t, "http://bigsegmentbase", BigSegmentSyncBaseURI(c))
		assert.Equal(t, "http://bigsegmentstream", BigSegmentSyncStreamURI(c))
	})
}
