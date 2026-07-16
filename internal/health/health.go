// Package health provides a shared evaluator for the health of Relay environments.
//
// The evaluator is the single source of truth for how Relay determines whether an
// environment is connected, whether its data store is available, whether big segments
// are stale, and whether the Relay as a whole is healthy or degraded. It is used both
// by the /status endpoint and by the OpenTelemetry health metrics, so the two always
// report the same state.
package health

import (
	"time"

	"github.com/launchdarkly/ld-relay/v9/config"
	"github.com/launchdarkly/ld-relay/v9/internal/bigsegments"
	"github.com/launchdarkly/ld-relay/v9/internal/sdks"

	"github.com/launchdarkly/go-sdk-common/v4/ldtime"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// Data store state values, matching the strings reported by the /status endpoint.
const (
	DataStoreStateInitializing = "INITIALIZING"
	DataStoreStateValid        = "VALID"
	DataStoreStateInterrupted  = "INTERRUPTED"
)

// DataSourceHealth describes the state of an environment's data source (the streaming or
// polling connection to LaunchDarkly).
type DataSourceHealth struct {
	// State is the SDK's data source state (INITIALIZING, VALID, INTERRUPTED, or OFF).
	State interfaces.DataSourceState

	// StateSince is when the data source entered its current state. If the SDK client has
	// not been created yet, this is the environment's creation time.
	StateSince time.Time

	// LastError is the most recent data source error, if any (Kind is "" if none).
	LastError interfaces.DataSourceErrorInfo
}

// DataStoreHealth describes the state of an environment's data store.
type DataStoreHealth struct {
	// State is one of the DataStoreState constants.
	State string

	// StateSince is when the data store entered its current state. It is the zero value if
	// the SDK client has not been created yet.
	StateSince time.Time

	// DBSystem is the type of database ("redis", "dynamodb", "consul"), or "" for the
	// default in-memory storage.
	DBSystem string
}

// BigSegmentsHealth describes the state of an environment's big segments store.
type BigSegmentsHealth struct {
	// Available is false if the last attempt to read the synchronization time failed.
	Available bool

	// PotentiallyStale is true if the store has never been synchronized, or was last
	// synchronized longer ago than the staleness threshold. It is always false when
	// Available is false, because staleness cannot be determined without a read.
	PotentiallyStale bool

	// LastSynchronized is the last synchronization time. Use IsDefined() to check whether
	// the store has ever been synchronized. It is undefined when Available is false.
	LastSynchronized ldtime.UnixMillisecondTime
}

// EnvironmentStatus is a snapshot of the health of a single environment.
type EnvironmentStatus struct {
	DataSource DataSourceHealth
	DataStore  DataStoreHealth

	// BigSegments is nil if big segments are not configured for the environment.
	BigSegments *BigSegmentsHealth

	// Connected corresponds to the per-environment "connected"/"disconnected" status
	// reported by the /status endpoint: the SDK client is initialized and the data source
	// has not been in a non-valid state for longer than the disconnection threshold.
	Connected bool

	// Healthy is this environment's contribution to overall Relay health: it is false if
	// the environment is not connected, or if big segments are potentially stale and stale
	// big segments are configured to count as degraded.
	Healthy bool
}

// RelayStatus is a snapshot of the health of the Relay as a whole.
type RelayStatus struct {
	// Healthy corresponds to the top-level "healthy"/"degraded" status reported by the
	// /status endpoint.
	Healthy bool

	// ConnectedEnvironments is the number of environments that are currently connected.
	ConnectedEnvironments int

	// DisconnectedEnvironments is the number of environments that are not currently connected.
	DisconnectedEnvironments int
}

// EnvironmentSource is the view of an environment that the evaluator needs. It is a subset
// of relayenv.EnvContext, which satisfies it implicitly.
type EnvironmentSource interface {
	GetClient() sdks.LDClientContext
	GetBigSegmentStore() bigsegments.BigSegmentStore
	GetCreationTime() time.Time
	GetDataStoreInfo() sdks.DataStoreEnvironmentInfo
}

// Evaluator computes environment health using the same rules and thresholds as the /status
// endpoint. The zero value is not meaningful; use NewEvaluator.
type Evaluator struct {
	// DisconnectedStatusTime is how long the data source may be in a non-valid state before
	// the environment is considered disconnected.
	DisconnectedStatusTime time.Duration

	// BigSegmentsStaleThreshold is how long after the last big segments synchronization the
	// store is considered potentially stale.
	BigSegmentsStaleThreshold time.Duration

	// BigSegmentsStaleAsDegraded indicates whether potentially stale big segments make the
	// environment unhealthy.
	BigSegmentsStaleAsDegraded bool

	// Now returns the current time; if nil, time.Now is used. It is settable for testing.
	Now func() time.Time
}

// NewEvaluator creates an Evaluator from the Relay configuration, applying the default
// values for any thresholds that are not set.
func NewEvaluator(main config.MainConfig) Evaluator {
	return Evaluator{
		DisconnectedStatusTime:     main.DisconnectedStatusTime.GetOrElse(config.DefaultDisconnectedStatusTime),
		BigSegmentsStaleThreshold:  main.BigSegmentsStaleThreshold.GetOrElse(config.DefaultBigSegmentsStaleThreshold),
		BigSegmentsStaleAsDegraded: main.BigSegmentsStaleAsDegraded,
	}
}

func (e Evaluator) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// EvaluateEnvironment computes a health snapshot for a single environment.
func (e Evaluator) EvaluateEnvironment(src EnvironmentSource) EnvironmentStatus {
	var status EnvironmentStatus

	client := src.GetClient()
	if client == nil {
		status.DataSource.State = interfaces.DataSourceStateInitializing
		status.DataSource.StateSince = src.GetCreationTime()
		status.DataStore.State = DataStoreStateInitializing
	} else {
		connected := client.Initialized()

		sourceStatus := client.GetDataSourceStatus()
		status.DataSource = DataSourceHealth{
			State:      sourceStatus.State,
			StateSince: sourceStatus.StateSince,
			LastError:  sourceStatus.LastError,
		}
		if sourceStatus.State != interfaces.DataSourceStateValid &&
			e.now().Sub(sourceStatus.StateSince) >= e.DisconnectedStatusTime {
			connected = false
		}

		storeStatus := client.GetDataStoreStatus()
		status.DataStore.State = DataStoreStateValid
		status.DataStore.StateSince = storeStatus.LastUpdated
		if !storeStatus.Available {
			status.DataStore.State = DataStoreStateInterrupted
		}

		status.Connected = connected
	}
	status.Healthy = status.Connected
	status.DataStore.DBSystem = src.GetDataStoreInfo().DBType

	if bigSegmentStore := src.GetBigSegmentStore(); bigSegmentStore != nil {
		bigSegments := &BigSegmentsHealth{}
		if synchronizedOn, err := bigSegmentStore.GetSynchronizedOn(); err == nil {
			bigSegments.Available = true
			bigSegments.LastSynchronized = synchronizedOn
			now := ldtime.UnixMillisFromTime(e.now())
			if !synchronizedOn.IsDefined() ||
				now > (synchronizedOn+ldtime.UnixMillisecondTime(e.BigSegmentsStaleThreshold.Milliseconds())) { //nolint:gosec
				bigSegments.PotentiallyStale = true
				if e.BigSegmentsStaleAsDegraded {
					status.Healthy = false
				}
			}
		}
		status.BigSegments = bigSegments
	}

	return status
}
