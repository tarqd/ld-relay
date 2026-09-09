package config

import (
	"log/slog"
	"testing"

	ct "github.com/launchdarkly/go-configtypes"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBigSegmentSyncDisabledResolution(t *testing.T) {
	for _, p := range []struct {
		name     string
		main     bool
		env      ct.OptBool
		expected bool
	}{
		{name: "default", main: false, env: ct.OptBool{}, expected: false},
		{name: "main-level disable, no override", main: true, env: ct.OptBool{}, expected: true},
		{name: "environment opts out of main-level disable", main: true, env: ct.NewOptBool(false), expected: false},
		{name: "environment disables while main allows", main: false, env: ct.NewOptBool(true), expected: true},
		{name: "environment redundantly disables", main: true, env: ct.NewOptBool(true), expected: true},
	} {
		t.Run(p.name, func(t *testing.T) {
			allConfig := Config{Main: MainConfig{DisableBigSegmentSync: p.main}}
			envConfig := EnvConfig{DisableBigSegmentSync: p.env}
			assert.Equal(t, p.expected, BigSegmentSyncDisabled(allConfig, envConfig))
		})
	}
}

// TestDisableBigSegmentSyncFromEnvironment confirms the option is readable on its own, without the
// rest of the "all base properties" configuration around it.
func TestDisableBigSegmentSyncFromEnvironment(t *testing.T) {
	withEnvironment(map[string]string{
		"DISABLE_BIG_SEGMENT_SYNC":            "true",
		"LD_ENV_earth":                        "earth-sdk",
		"LD_DISABLE_BIG_SEGMENT_SYNC_earth":   "false",
		"LD_ENV_krypton":                      "krypton-sdk",
		"LD_DISABLE_BIG_SEGMENT_SYNC_krypton": "true",
	}, func() {
		var c Config
		require.NoError(t, LoadConfigFromEnvironment(&c, slog.Default()))
		assert.True(t, c.Main.DisableBigSegmentSync)
		assert.False(t, BigSegmentSyncDisabled(c, *c.Environment["earth"]))
		assert.True(t, BigSegmentSyncDisabled(c, *c.Environment["krypton"]))
	})
}

func TestBigSegmentSyncBaseURIResolution(t *testing.T) {
	baseURI := newOptURLAbsoluteMustBeValid("http://base")
	bigSegmentURI := newOptURLAbsoluteMustBeValid("http://bigsegments")

	t.Run("falls back to the base URI", func(t *testing.T) {
		c := Config{Main: MainConfig{BaseURI: baseURI}}
		assert.Equal(t, "http://base", BigSegmentSyncBaseURI(c))
	})

	t.Run("big segment URI takes precedence", func(t *testing.T) {
		c := Config{Main: MainConfig{BaseURI: baseURI, BigSegmentURI: bigSegmentURI}}
		assert.Equal(t, "http://bigsegments", BigSegmentSyncBaseURI(c))
	})

	t.Run("big segment URI applies even with no base URI", func(t *testing.T) {
		c := Config{Main: MainConfig{BigSegmentURI: bigSegmentURI}}
		assert.Equal(t, "http://bigsegments", BigSegmentSyncBaseURI(c))
	})
}

func TestBigSegmentURIFromEnvironment(t *testing.T) {
	withEnvironment(map[string]string{
		"BASE_URI":        "http://base",
		"BIG_SEGMENT_URI": "http://bigsegments",
	}, func() {
		var c Config
		require.NoError(t, LoadConfigFromEnvironment(&c, slog.Default()))
		assert.Equal(t, "http://bigsegments", c.Main.BigSegmentURI.String())
		assert.Equal(t, "http://bigsegments", BigSegmentSyncBaseURI(c))
	})
}
