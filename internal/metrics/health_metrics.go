package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/launchdarkly/ld-relay/v9/internal/health"

	"github.com/launchdarkly/go-sdk-common/v4/ldtime"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EnvironmentHealthProvider supplies a health snapshot for a single environment. It is
// implemented by relayenv's environment context.
type EnvironmentHealthProvider interface {
	EnvironmentHealth() health.EnvironmentStatus
}

// RelayHealthProvider supplies a health snapshot for the Relay as a whole. It is implemented
// by the Relay instance.
type RelayHealthProvider interface {
	RelayHealth() health.RelayStatus
}

// allDataSourceStates and allDataStoreStates enumerate the possible states for the state-set
// gauges: at each observation, the current state is reported as 1 and every other state as 0,
// so that dashboards and alerts can rely on all series always being present.
var allDataSourceStates = []interfaces.DataSourceState{ //nolint:gochecknoglobals
	interfaces.DataSourceStateInitializing,
	interfaces.DataSourceStateValid,
	interfaces.DataSourceStateInterrupted,
	interfaces.DataSourceStateOff,
}

var allDataStoreStates = []string{ //nolint:gochecknoglobals
	health.DataStoreStateInitializing,
	health.DataStoreStateValid,
	health.DataStoreStateInterrupted,
}

// setWith builds an attribute set from a base attribute list plus extra attributes, without
// mutating the base list (attribute.NewSet sorts its input in place, so the callbacks must
// always pass it a fresh slice).
func setWith(base []attribute.KeyValue, extra ...attribute.KeyValue) attribute.Set {
	kvs := make([]attribute.KeyValue, 0, len(base)+len(extra))
	kvs = append(kvs, base...)
	kvs = append(kvs, extra...)
	return attribute.NewSet(kvs...)
}

func boolToGaugeValue(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// RegisterEnvironmentHealthCallback registers an OTel callback that observes the health
// gauges for one environment at each metrics collection, reading the current state from the
// given provider. The registration is removed automatically when the environment is removed
// from the Manager or the Manager is closed.
//
// When OTel is disabled the Manager's meter is a no-op, so registering is free and the
// callback is never invoked; callers do not need to check whether metrics are enabled.
func (m *Manager) RegisterEnvironmentHealthCallback(em *EnvironmentManager, provider EnvironmentHealthProvider) error {
	// Private copy of the environment attributes, for the same reason as NewEventMetricsRecorder.
	base := make([]attribute.KeyValue, len(em.envKVs))
	copy(base, em.envKVs)

	instruments := m.instruments
	registration, err := m.meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			status := provider.EnvironmentHealth()
			now := time.Now()

			currentSourceState := strings.ToLower(string(status.DataSource.State))
			for _, state := range allDataSourceStates {
				o.ObserveInt64(instruments.dataSourceState,
					boolToGaugeValue(state == status.DataSource.State),
					metric.WithAttributeSet(setWith(base, stateAttrKey.String(strings.ToLower(string(state))))))
			}
			if !status.DataSource.StateSince.IsZero() {
				o.ObserveFloat64(instruments.dataSourceStateDuration,
					now.Sub(status.DataSource.StateSince).Seconds(),
					metric.WithAttributeSet(setWith(base, stateAttrKey.String(currentSourceState))))
			}

			storeBase := base
			if status.DataStore.DBSystem != "" {
				storeBase = make([]attribute.KeyValue, 0, len(base)+1)
				storeBase = append(storeBase, base...)
				storeBase = append(storeBase, dbSystemAttrKey.String(status.DataStore.DBSystem))
			}
			for _, state := range allDataStoreStates {
				o.ObserveInt64(instruments.dataStoreState,
					boolToGaugeValue(state == status.DataStore.State),
					metric.WithAttributeSet(setWith(storeBase, stateAttrKey.String(strings.ToLower(state)))))
			}
			if !status.DataStore.StateSince.IsZero() {
				o.ObserveFloat64(instruments.dataStoreStateDuration,
					now.Sub(status.DataStore.StateSince).Seconds(),
					metric.WithAttributeSet(setWith(storeBase, stateAttrKey.String(strings.ToLower(status.DataStore.State)))))
			}

			if status.BigSegments != nil {
				baseSet := setWith(base)
				o.ObserveInt64(instruments.bigSegmentsAvailable,
					boolToGaugeValue(status.BigSegments.Available),
					metric.WithAttributeSet(baseSet))
				o.ObserveInt64(instruments.bigSegmentsStale,
					boolToGaugeValue(status.BigSegments.PotentiallyStale),
					metric.WithAttributeSet(baseSet))
				if status.BigSegments.Available && status.BigSegments.LastSynchronized.IsDefined() {
					ageMillis := int64(ldtime.UnixMillisFromTime(now)) - int64(status.BigSegments.LastSynchronized) //nolint:gosec
					o.ObserveFloat64(instruments.bigSegmentsSyncAge,
						float64(ageMillis)/1000,
						metric.WithAttributeSet(baseSet))
				}
			}
			return nil
		},
		instruments.dataSourceState,
		instruments.dataSourceStateDuration,
		instruments.dataStoreState,
		instruments.dataStoreStateDuration,
		instruments.bigSegmentsAvailable,
		instruments.bigSegmentsStale,
		instruments.bigSegmentsSyncAge,
	)
	if err != nil {
		return err
	}
	em.healthRegistration = registration
	return nil
}

// RegisterRelayHealthCallback registers an OTel callback that observes the Relay-level health
// gauges at each metrics collection. It returns a function that removes the registration,
// which should be called when the Relay is shut down.
//
// As with RegisterEnvironmentHealthCallback, this is a no-op when OTel is disabled.
func (m *Manager) RegisterRelayHealthCallback(provider RelayHealthProvider) (func(), error) {
	base := []attribute.KeyValue{relayIDAttrKey.String(m.metricsRelayID)}

	instruments := m.instruments
	registration, err := m.meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			status := provider.RelayHealth()
			o.ObserveInt64(instruments.relayHealthy,
				boolToGaugeValue(status.Healthy),
				metric.WithAttributeSet(setWith(base)))
			o.ObserveInt64(instruments.relayEnvironments,
				int64(status.ConnectedEnvironments),
				metric.WithAttributeSet(setWith(base, statusAttrKey.String("connected"))))
			o.ObserveInt64(instruments.relayEnvironments,
				int64(status.DisconnectedEnvironments),
				metric.WithAttributeSet(setWith(base, statusAttrKey.String("disconnected"))))
			return nil
		},
		instruments.relayHealthy,
		instruments.relayEnvironments,
	)
	if err != nil {
		return nil, err
	}
	return func() { _ = registration.Unregister() }, nil
}
