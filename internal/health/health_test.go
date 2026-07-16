package health

import (
	"errors"
	"testing"
	"time"

	"github.com/launchdarkly/ld-relay/v9/config"
	"github.com/launchdarkly/ld-relay/v9/internal/bigsegments"
	"github.com/launchdarkly/ld-relay/v9/internal/sdks"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldtime"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"github.com/stretchr/testify/assert"

	ct "github.com/launchdarkly/go-configtypes"
)

type stubClient struct {
	initialized  bool
	sourceStatus interfaces.DataSourceStatus
	storeStatus  sdks.DataStoreStatusInfo
}

func (c *stubClient) Initialized() bool                                { return c.initialized }
func (c *stubClient) SecureModeHash(ldcontext.Context) string          { return "" }
func (c *stubClient) GetDataSourceStatus() interfaces.DataSourceStatus { return c.sourceStatus }
func (c *stubClient) GetDataStoreStatus() sdks.DataStoreStatusInfo     { return c.storeStatus }
func (c *stubClient) Close() error                                     { return nil }

func (c *stubClient) AddDataSourceStatusListener() <-chan interfaces.DataSourceStatus {
	return make(chan interfaces.DataSourceStatus)
}

type stubSource struct {
	client       sdks.LDClientContext
	bigSegments  bigsegments.BigSegmentStore
	creationTime time.Time
	storeInfo    sdks.DataStoreEnvironmentInfo
}

func (s *stubSource) GetClient() sdks.LDClientContext                { return s.client }
func (s *stubSource) GetBigSegmentStore() bigsegments.BigSegmentStore { return s.bigSegments }
func (s *stubSource) GetCreationTime() time.Time                     { return s.creationTime }
func (s *stubSource) GetDataStoreInfo() sdks.DataStoreEnvironmentInfo { return s.storeInfo }

var testNow = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals

func testEvaluator() Evaluator {
	e := NewEvaluator(config.MainConfig{})
	e.Now = func() time.Time { return testNow }
	return e
}

func validClient(stateSince time.Time) *stubClient {
	return &stubClient{
		initialized: true,
		sourceStatus: interfaces.DataSourceStatus{
			State:      interfaces.DataSourceStateValid,
			StateSince: stateSince,
		},
		storeStatus: sdks.DataStoreStatusInfo{Available: true, LastUpdated: stateSince},
	}
}

func TestEvaluatorAppliesConfigDefaults(t *testing.T) {
	e := NewEvaluator(config.MainConfig{})
	assert.Equal(t, config.DefaultDisconnectedStatusTime, e.DisconnectedStatusTime)
	assert.Equal(t, config.DefaultBigSegmentsStaleThreshold, e.BigSegmentsStaleThreshold)
	assert.False(t, e.BigSegmentsStaleAsDegraded)

	main := config.MainConfig{
		DisconnectedStatusTime:     ct.NewOptDuration(3 * time.Second),
		BigSegmentsStaleThreshold:  ct.NewOptDuration(4 * time.Second),
		BigSegmentsStaleAsDegraded: true,
	}
	e = NewEvaluator(main)
	assert.Equal(t, 3*time.Second, e.DisconnectedStatusTime)
	assert.Equal(t, 4*time.Second, e.BigSegmentsStaleThreshold)
	assert.True(t, e.BigSegmentsStaleAsDegraded)
}

func TestEvaluateEnvironmentNilClient(t *testing.T) {
	creation := testNow.Add(-30 * time.Second)
	src := &stubSource{
		creationTime: creation,
		storeInfo:    sdks.DataStoreEnvironmentInfo{DBType: "redis"},
	}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.False(t, status.Connected)
	assert.False(t, status.Healthy)
	assert.Equal(t, interfaces.DataSourceStateInitializing, status.DataSource.State)
	assert.Equal(t, creation, status.DataSource.StateSince)
	assert.Equal(t, DataStoreStateInitializing, status.DataStore.State)
	assert.True(t, status.DataStore.StateSince.IsZero())
	assert.Equal(t, "redis", status.DataStore.DBSystem)
	assert.Nil(t, status.BigSegments)
}

func TestEvaluateEnvironmentConnected(t *testing.T) {
	since := testNow.Add(-time.Hour)
	src := &stubSource{client: validClient(since)}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.True(t, status.Connected)
	assert.True(t, status.Healthy)
	assert.Equal(t, interfaces.DataSourceStateValid, status.DataSource.State)
	assert.Equal(t, since, status.DataSource.StateSince)
	assert.Equal(t, DataStoreStateValid, status.DataStore.State)
	assert.Equal(t, since, status.DataStore.StateSince)
	assert.Equal(t, "", status.DataStore.DBSystem)
	assert.Nil(t, status.BigSegments)
}

func TestEvaluateEnvironmentInterruptedWithinThresholdIsStillConnected(t *testing.T) {
	client := validClient(testNow.Add(-time.Hour))
	client.sourceStatus.State = interfaces.DataSourceStateInterrupted
	client.sourceStatus.StateSince = testNow.Add(-time.Second) // less than DisconnectedStatusTime
	client.sourceStatus.LastError = interfaces.DataSourceErrorInfo{
		Kind: interfaces.DataSourceErrorKindNetworkError,
		Time: testNow.Add(-time.Second),
	}
	src := &stubSource{client: client}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.True(t, status.Connected)
	assert.True(t, status.Healthy)
	assert.Equal(t, interfaces.DataSourceStateInterrupted, status.DataSource.State)
	assert.Equal(t, interfaces.DataSourceErrorKindNetworkError, status.DataSource.LastError.Kind)
}

func TestEvaluateEnvironmentInterruptedBeyondThresholdIsDisconnected(t *testing.T) {
	client := validClient(testNow.Add(-time.Hour))
	client.sourceStatus.State = interfaces.DataSourceStateInterrupted
	client.sourceStatus.StateSince = testNow.Add(-config.DefaultDisconnectedStatusTime)
	src := &stubSource{client: client}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.False(t, status.Connected)
	assert.False(t, status.Healthy)
}

func TestEvaluateEnvironmentUninitializedClientIsDisconnected(t *testing.T) {
	client := validClient(testNow.Add(-time.Second))
	client.initialized = false
	client.sourceStatus.State = interfaces.DataSourceStateInitializing
	src := &stubSource{client: client}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.False(t, status.Connected)
	assert.False(t, status.Healthy)
	assert.Equal(t, interfaces.DataSourceStateInitializing, status.DataSource.State)
}

func TestEvaluateEnvironmentStoreUnavailable(t *testing.T) {
	client := validClient(testNow.Add(-time.Hour))
	client.storeStatus.Available = false
	src := &stubSource{client: client, storeInfo: sdks.DataStoreEnvironmentInfo{DBType: "dynamodb"}}

	status := testEvaluator().EvaluateEnvironment(src)

	assert.Equal(t, DataStoreStateInterrupted, status.DataStore.State)
	assert.Equal(t, "dynamodb", status.DataStore.DBSystem)
	// Store unavailability by itself does not make the environment unhealthy, matching /status.
	assert.True(t, status.Connected)
	assert.True(t, status.Healthy)
}

func TestEvaluateEnvironmentBigSegmentsSynchronized(t *testing.T) {
	syncedOn := ldtime.UnixMillisFromTime(testNow.Add(-time.Second))
	src := &stubSource{
		client:      validClient(testNow.Add(-time.Hour)),
		bigSegments: bigsegments.NewStaticBigSegmentStore(syncedOn, nil),
	}

	status := testEvaluator().EvaluateEnvironment(src)

	if assert.NotNil(t, status.BigSegments) {
		assert.True(t, status.BigSegments.Available)
		assert.False(t, status.BigSegments.PotentiallyStale)
		assert.Equal(t, syncedOn, status.BigSegments.LastSynchronized)
	}
	assert.True(t, status.Healthy)
}

func TestEvaluateEnvironmentBigSegmentsStoreError(t *testing.T) {
	src := &stubSource{
		client:      validClient(testNow.Add(-time.Hour)),
		bigSegments: bigsegments.NewStaticBigSegmentStore(0, errors.New("store failure")),
	}

	status := testEvaluator().EvaluateEnvironment(src)

	if assert.NotNil(t, status.BigSegments) {
		assert.False(t, status.BigSegments.Available)
		// Staleness cannot be determined on a read failure, so it stays false, matching /status.
		assert.False(t, status.BigSegments.PotentiallyStale)
		assert.False(t, status.BigSegments.LastSynchronized.IsDefined())
	}
	assert.True(t, status.Healthy)
}

func TestEvaluateEnvironmentBigSegmentsStale(t *testing.T) {
	staleSync := ldtime.UnixMillisFromTime(testNow.Add(-config.DefaultBigSegmentsStaleThreshold - time.Second))

	for _, staleAsDegraded := range []bool{false, true} {
		e := testEvaluator()
		e.BigSegmentsStaleAsDegraded = staleAsDegraded
		src := &stubSource{
			client:      validClient(testNow.Add(-time.Hour)),
			bigSegments: bigsegments.NewStaticBigSegmentStore(staleSync, nil),
		}

		status := e.EvaluateEnvironment(src)

		if assert.NotNil(t, status.BigSegments) {
			assert.True(t, status.BigSegments.Available)
			assert.True(t, status.BigSegments.PotentiallyStale)
		}
		assert.Equal(t, !staleAsDegraded, status.Healthy, "staleAsDegraded=%t", staleAsDegraded)
		assert.True(t, status.Connected) // staleness never affects connectedness
	}
}

func TestEvaluateEnvironmentBigSegmentsNeverSynchronizedIsStale(t *testing.T) {
	src := &stubSource{
		client:      validClient(testNow.Add(-time.Hour)),
		bigSegments: bigsegments.NewStaticBigSegmentStore(0, nil), // 0 = undefined
	}

	status := testEvaluator().EvaluateEnvironment(src)

	if assert.NotNil(t, status.BigSegments) {
		assert.True(t, status.BigSegments.Available)
		assert.True(t, status.BigSegments.PotentiallyStale)
		assert.False(t, status.BigSegments.LastSynchronized.IsDefined())
	}
}
