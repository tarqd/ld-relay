package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/launchdarkly/ld-relay/v9/internal/health"

	"github.com/launchdarkly/go-sdk-common/v4/ldtime"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fakeEnvHealthProvider struct {
	mu     sync.Mutex
	status health.EnvironmentStatus
}

func (p *fakeEnvHealthProvider) EnvironmentHealth() health.EnvironmentStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *fakeEnvHealthProvider) setStatus(status health.EnvironmentStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = status
}

type fakeRelayHealthProvider struct {
	status health.RelayStatus
}

func (p *fakeRelayHealthProvider) RelayHealth() health.RelayStatus {
	return p.status
}

func healthyEnvironmentStatus() health.EnvironmentStatus {
	return health.EnvironmentStatus{
		DataSource: health.DataSourceHealth{
			State:      interfaces.DataSourceStateValid,
			StateSince: time.Now().Add(-10 * time.Second),
		},
		DataStore: health.DataStoreHealth{
			State:      health.DataStoreStateValid,
			StateSince: time.Now().Add(-20 * time.Second),
		},
		Connected: true,
		Healthy:   true,
	}
}

func gaugePointsInt64(t *testing.T, rm *metricdata.ResourceMetrics, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	m := findMetric(rm, name)
	if m == nil {
		return nil
	}
	gauge, ok := m.Data.(metricdata.Gauge[int64])
	require.True(t, ok, "expected Gauge[int64] data for %s", name)
	return gauge.DataPoints
}

func gaugePointsFloat64(t *testing.T, rm *metricdata.ResourceMetrics, name string) []metricdata.DataPoint[float64] {
	t.Helper()
	m := findMetric(rm, name)
	if m == nil {
		return nil
	}
	gauge, ok := m.Data.(metricdata.Gauge[float64])
	require.True(t, ok, "expected Gauge[float64] data for %s", name)
	return gauge.DataPoints
}

// pointWithAttrs finds the single data point whose attributes include all of the given key/value
// pairs. Fails the test if there is no match.
func pointWithAttrs[N int64 | float64](t *testing.T, points []metricdata.DataPoint[N], want map[attribute.Key]string) metricdata.DataPoint[N] {
	t.Helper()
	for _, dp := range points {
		matched := true
		for key, wantValue := range want {
			value, ok := dp.Attributes.Value(key)
			if !ok || value.AsString() != wantValue {
				matched = false
				break
			}
		}
		if matched {
			return dp
		}
	}
	require.Fail(t, "no data point matched attributes", "want %v in %d points", want, len(points))
	return metricdata.DataPoint[N]{}
}

func TestEnvironmentHealthMetricsHealthyState(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		status := healthyEnvironmentStatus()
		status.DataStore.DBSystem = "redis"
		status.BigSegments = &health.BigSegmentsHealth{
			Available:        true,
			PotentiallyStale: false,
			LastSynchronized: ldtime.UnixMillisFromTime(time.Now().Add(-30 * time.Second)),
		}
		provider := &fakeEnvHealthProvider{status: status}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		// Data source state set: exactly one series per state, 1 only for the current state.
		statePoints := gaugePointsInt64(t, rm, dataSourceStateMeasureName)
		require.Len(t, statePoints, 4)
		expectedSourceStates := map[string]int64{"initializing": 0, "valid": 1, "interrupted": 0, "off": 0}
		for stateName, expected := range expectedSourceStates {
			dp := pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: stateName, envNameAttrKey: p.envName})
			assert.Equal(t, expected, dp.Value, "state=%s", stateName)
		}

		durationPoints := gaugePointsFloat64(t, rm, dataSourceStateDurationMeasureName)
		require.Len(t, durationPoints, 1)
		durationPoint := pointWithAttrs(t, durationPoints, map[attribute.Key]string{stateAttrKey: "valid"})
		assert.InDelta(t, 10, durationPoint.Value, 5)

		// Data store state set with db.system attribute.
		storePoints := gaugePointsInt64(t, rm, dataStoreStateMeasureName)
		require.Len(t, storePoints, 3)
		expectedStoreStates := map[string]int64{"initializing": 0, "valid": 1, "interrupted": 0}
		for stateName, expected := range expectedStoreStates {
			dp := pointWithAttrs(t, storePoints, map[attribute.Key]string{stateAttrKey: stateName, dbSystemAttrKey: "redis"})
			assert.Equal(t, expected, dp.Value, "state=%s", stateName)
		}

		storeDurationPoints := gaugePointsFloat64(t, rm, dataStoreStateDurationMeasureName)
		storeDurationPoint := pointWithAttrs(t, storeDurationPoints, map[attribute.Key]string{stateAttrKey: "valid", dbSystemAttrKey: "redis"})
		assert.InDelta(t, 20, storeDurationPoint.Value, 5)

		// Big segments gauges.
		availablePoints := gaugePointsInt64(t, rm, bigSegmentsAvailableMeasureName)
		require.Len(t, availablePoints, 1)
		assert.Equal(t, int64(1), availablePoints[0].Value)
		stalePoints := gaugePointsInt64(t, rm, bigSegmentsStaleMeasureName)
		require.Len(t, stalePoints, 1)
		assert.Equal(t, int64(0), stalePoints[0].Value)
		agePoints := gaugePointsFloat64(t, rm, bigSegmentsSyncAgeMeasureName)
		require.Len(t, agePoints, 1)
		assert.InDelta(t, 30, agePoints[0].Value, 5)
	})
}

func TestEnvironmentHealthMetricsUnhealthyState(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		status := healthyEnvironmentStatus()
		status.DataSource.State = interfaces.DataSourceStateInterrupted
		status.DataStore.State = health.DataStoreStateInterrupted
		status.BigSegments = &health.BigSegmentsHealth{Available: false} // e.g. store read error
		status.Connected = false
		status.Healthy = false
		provider := &fakeEnvHealthProvider{status: status}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		statePoints := gaugePointsInt64(t, rm, dataSourceStateMeasureName)
		assert.Equal(t, int64(1), pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: "interrupted"}).Value)
		assert.Equal(t, int64(0), pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: "valid"}).Value)

		// No db.system attribute when using the default in-memory store.
		storePoints := gaugePointsInt64(t, rm, dataStoreStateMeasureName)
		interruptedPoint := pointWithAttrs(t, storePoints, map[attribute.Key]string{stateAttrKey: "interrupted"})
		assert.Equal(t, int64(1), interruptedPoint.Value)
		_, hasDBSystem := interruptedPoint.Attributes.Value(dbSystemAttrKey)
		assert.False(t, hasDBSystem)

		assert.Equal(t, int64(0), gaugePointsInt64(t, rm, bigSegmentsAvailableMeasureName)[0].Value)
		assert.Equal(t, int64(0), gaugePointsInt64(t, rm, bigSegmentsStaleMeasureName)[0].Value)
		// Synchronization age is not observed when the store is unavailable.
		assert.Empty(t, gaugePointsFloat64(t, rm, bigSegmentsSyncAgeMeasureName))
	})
}

func TestEnvironmentHealthMetricsWithoutBigSegments(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		provider := &fakeEnvHealthProvider{status: healthyEnvironmentStatus()}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		assert.Empty(t, gaugePointsInt64(t, rm, bigSegmentsAvailableMeasureName))
		assert.Empty(t, gaugePointsInt64(t, rm, bigSegmentsStaleMeasureName))
		assert.Empty(t, gaugePointsFloat64(t, rm, bigSegmentsSyncAgeMeasureName))
	})
}

func TestEnvironmentHealthMetricsSkipsUndefinedDurations(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		// Matches the snapshot produced before the SDK client exists: the data source state
		// dates from environment creation, but the data store state has no timestamp yet.
		status := health.EnvironmentStatus{
			DataSource: health.DataSourceHealth{
				State:      interfaces.DataSourceStateInitializing,
				StateSince: time.Now().Add(-3 * time.Second),
			},
			DataStore: health.DataStoreHealth{State: health.DataStoreStateInitializing},
		}
		provider := &fakeEnvHealthProvider{status: status}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		storePoints := gaugePointsInt64(t, rm, dataStoreStateMeasureName)
		assert.Equal(t, int64(1), pointWithAttrs(t, storePoints, map[attribute.Key]string{stateAttrKey: "initializing"}).Value)
		assert.Empty(t, gaugePointsFloat64(t, rm, dataStoreStateDurationMeasureName))

		durationPoints := gaugePointsFloat64(t, rm, dataSourceStateDurationMeasureName)
		require.Len(t, durationPoints, 1)
		assert.InDelta(t, 3, durationPoints[0].Value, 3)
	})
}

func TestEnvironmentHealthMetricsIncludeIdentityAttributes(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		env, err := p.manager.AddEnvironment(EnvironmentAttrs{
			Name:    "Project Production",
			EnvID:   "1234567890abcdef",
			EnvKey:  "production",
			ProjKey: "project",
		}, nil)
		require.NoError(t, err)
		provider := &fakeEnvHealthProvider{status: healthyEnvironmentStatus()}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		statePoints := gaugePointsInt64(t, rm, dataSourceStateMeasureName)
		pointWithAttrs(t, statePoints, map[attribute.Key]string{
			envNameAttrKey: "Project Production",
			envIDAttrKey:   "1234567890abcdef",
			envKeyAttrKey:  "production",
			projKeyAttrKey: "project",
			stateAttrKey:   "valid",
		})

		// The identity attributes also flow into the pre-existing request metrics via envKVs.
		WithGauge(env, p.instruments, RequestInfo{UserAgent: userAgentValue}, func() {
			rm, err := p.collectMetrics()
			require.NoError(t, err)
			connMetric := findMetric(rm, connMeasureName)
			require.NotNil(t, connMetric)
			sum, ok := connMetric.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			pointWithAttrs(t, sum.DataPoints, map[attribute.Key]string{
				envIDAttrKey:   "1234567890abcdef",
				envKeyAttrKey:  "production",
				projKeyAttrKey: "project",
			})
		}, ServerConns)
	})
}

func TestEnvironmentHealthCallbackIsUnregisteredOnRemoveEnvironment(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		provider := &fakeEnvHealthProvider{status: healthyEnvironmentStatus()}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)
		require.NotEmpty(t, gaugePointsInt64(t, rm, dataSourceStateMeasureName))

		p.manager.RemoveEnvironment(p.env)

		rm, err = p.collectMetrics()
		require.NoError(t, err)
		assert.Empty(t, gaugePointsInt64(t, rm, dataSourceStateMeasureName))
	})
}

func TestEnvironmentHealthMetricsObserveCurrentState(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		provider := &fakeEnvHealthProvider{status: healthyEnvironmentStatus()}
		require.NoError(t, p.manager.RegisterEnvironmentHealthCallback(p.env, provider))

		rm, err := p.collectMetrics()
		require.NoError(t, err)
		statePoints := gaugePointsInt64(t, rm, dataSourceStateMeasureName)
		assert.Equal(t, int64(1), pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: "valid"}).Value)

		// The next collection reflects a state change with no re-registration needed.
		status := provider.status
		status.DataSource.State = interfaces.DataSourceStateOff
		provider.setStatus(status)

		rm, err = p.collectMetrics()
		require.NoError(t, err)
		statePoints = gaugePointsInt64(t, rm, dataSourceStateMeasureName)
		assert.Equal(t, int64(0), pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: "valid"}).Value)
		assert.Equal(t, int64(1), pointWithAttrs(t, statePoints, map[attribute.Key]string{stateAttrKey: "off"}).Value)
	})
}

func TestRecordDataSourceError(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		ctx := context.Background()
		RecordDataSourceError(ctx, p.instruments, p.env, interfaces.DataSourceErrorKindNetworkError)
		RecordDataSourceError(ctx, p.instruments, p.env, interfaces.DataSourceErrorKindNetworkError)
		RecordDataSourceError(ctx, p.instruments, p.env, interfaces.DataSourceErrorKindErrorResponse)

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		m := findMetric(rm, dataSourceErrorsMeasureName)
		require.NotNil(t, m)
		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok)
		networkPoint := pointWithAttrs(t, sum.DataPoints, map[attribute.Key]string{errorTypeAttrKey: "network_error", envNameAttrKey: p.envName})
		assert.Equal(t, int64(2), networkPoint.Value)
		responsePoint := pointWithAttrs(t, sum.DataPoints, map[attribute.Key]string{errorTypeAttrKey: "error_response"})
		assert.Equal(t, int64(1), responsePoint.Value)
	})
}

func TestRecordDataSourceErrorIsNilSafe(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		assert.NotPanics(t, func() {
			RecordDataSourceError(context.Background(), nil, p.env, interfaces.DataSourceErrorKindUnknown)
			RecordDataSourceError(context.Background(), p.instruments, nil, interfaces.DataSourceErrorKindUnknown)
		})
	})
}

func TestRelayHealthCallback(t *testing.T) {
	testWithOTel(t, func(p testWithOTelParams) {
		provider := &fakeRelayHealthProvider{status: health.RelayStatus{
			Healthy:                  true,
			ConnectedEnvironments:    3,
			DisconnectedEnvironments: 1,
		}}
		unregister, err := p.manager.RegisterRelayHealthCallback(provider)
		require.NoError(t, err)

		rm, err := p.collectMetrics()
		require.NoError(t, err)

		healthyPoints := gaugePointsInt64(t, rm, relayHealthyMeasureName)
		healthyPoint := pointWithAttrs(t, healthyPoints, map[attribute.Key]string{relayIDAttrKey: p.relayID})
		assert.Equal(t, int64(1), healthyPoint.Value)

		envPoints := gaugePointsInt64(t, rm, relayEnvironmentsMeasureName)
		assert.Equal(t, int64(3), pointWithAttrs(t, envPoints, map[attribute.Key]string{statusAttrKey: "connected"}).Value)
		assert.Equal(t, int64(1), pointWithAttrs(t, envPoints, map[attribute.Key]string{statusAttrKey: "disconnected"}).Value)

		unregister()

		rm, err = p.collectMetrics()
		require.NoError(t, err)
		assert.Empty(t, gaugePointsInt64(t, rm, relayHealthyMeasureName))
	})
}
