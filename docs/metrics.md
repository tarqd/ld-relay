# LaunchDarkly Relay Proxy - Metrics

[(Back to README)](../README.md)

The Relay Proxy can export metrics via [OpenTelemetry Protocol (OTLP)](https://opentelemetry.io/docs/specs/otlp/) to any compatible backend, such as Prometheus, Datadog, Grafana, or an OpenTelemetry Collector. To learn about configuration, read [Configuration](./configuration.md).

## Available metrics

| Metric | Type | Unit | Description |
|--------|------|------|-------------|
| `http.server.active_requests` | UpDownCounter | `{request}` | The number of currently active stream connections from SDKs to the Relay Proxy. |
| `http.server.request.duration` | Histogram | `s` | The duration of requests to the Relay Proxy's service endpoints, in seconds. |
| `launchdarkly.relay.events.received.size` | Counter | `By` | The cumulative number of event bytes received by the Relay Proxy (measured after decompression). |
| `launchdarkly.relay.events.sent` | Counter | `{event}` | The cumulative number of events successfully sent to LaunchDarkly. |
| `launchdarkly.relay.events.sent.size` | Counter | `By` | The cumulative bytes of event payloads successfully sent to LaunchDarkly. |
| `launchdarkly.relay.events.send.errors` | Counter | `{event}` | The cumulative number of events that failed to send after all retries. |
| `launchdarkly.relay.events.dropped` | Counter | `{event}` | The cumulative number of events dropped due to capacity overflow. |
| `launchdarkly.relay.events.pending` | Gauge | `{event}` | The current number of events buffered in the queue. |

## Health metrics

The Relay Proxy also exports health metrics that report the same state as the [status endpoint](./endpoints.md), using the same rules and thresholds (`DISCONNECTED_STATUS_TIME`, `BIG_SEGMENTS_STALE_THRESHOLD`, and `BIG_SEGMENTS_STALE_AS_DEGRADED` — see [Configuration](./configuration.md)). Use these to dashboard and alert on the Relay Proxy's connection to LaunchDarkly, its persistent data store, and big segments synchronization.

| Metric | Type | Unit | Description |
|--------|------|------|-------------|
| `launchdarkly.relay.data_source.state` | Gauge | | The state of an environment's connection to LaunchDarkly, as a set of `state` series (`initializing`, `valid`, `interrupted`, `off`): the current state reports `1` and every other state reports `0`. For example, alert on `launchdarkly.relay.data_source.state{state="valid"} == 0`. |
| `launchdarkly.relay.data_source.state.duration` | Gauge | `s` | How long the environment's data source has been in its current state (the `state` attribute holds the current state). For a healthy environment, this is how long the stream has been connected. |
| `launchdarkly.relay.data_source.errors` | Counter | `{error}` | The cumulative number of data source connection errors, by `error.type` (`network_error`, `error_response`, `invalid_data`, `store_error`, `unknown`). Use this to alert on error rate or connection flapping. |
| `launchdarkly.relay.data_store.state` | Gauge | | The state of an environment's data store, as a set of `state` series (`initializing`, `valid`, `interrupted`) with the same 0/1 semantics as `data_source.state`. When a persistent store is configured, the `db.system` attribute identifies it. |
| `launchdarkly.relay.data_store.state.duration` | Gauge | `s` | How long the environment's data store has been in its current state. Not reported until the SDK client for the environment has been created. |
| `launchdarkly.relay.big_segments.available` | Gauge | | Whether the environment's big segments store is available (`1`) or the last attempt to read it failed (`0`). Only reported when big segments are configured. |
| `launchdarkly.relay.big_segments.potentially_stale` | Gauge | | Whether the environment's big segments data is potentially stale (`1`): never synchronized, or last synchronized longer ago than the staleness threshold. Only reported when big segments are configured. |
| `launchdarkly.relay.big_segments.synchronization.age` | Gauge | `s` | Time since the environment's big segments were last synchronized. Not reported when the store is unavailable or has never been synchronized (use `potentially_stale` for alerting on those conditions). |
| `launchdarkly.relay.status.healthy` | Gauge | | Whether the Relay Proxy as a whole is healthy (`1`) or degraded (`0`), matching the top-level `status` of the status endpoint. |
| `launchdarkly.relay.environments` | Gauge | `{environment}` | The number of configured environments, as one series per `status` value (`connected`, `disconnected`). |

The gauges are observed at each metrics collection (the OTLP exporter's periodic reader interval, `OTEL_METRIC_EXPORT_INTERVAL`, one minute by default).

## Attributes

All metrics include the following attributes:

| Attribute | Description |
|-----------|-------------|
| `relay.id` | A unique identifier for this Relay Proxy instance, generated at startup. |
| `environment.name` | The name of the LaunchDarkly environment as configured in the Relay Proxy. In automatic configuration or offline mode, this is the actual project and environment name from LaunchDarkly. Example: `MyApplication Staging` (Not present on the Relay-level metrics `launchdarkly.relay.status.healthy` and `launchdarkly.relay.environments`.) |
| `environment.id` | The LaunchDarkly environment ID. Only present when known, which is normally the case in automatic configuration or offline mode, or when `envId` is set in the environment configuration. |
| `environment.key` | The LaunchDarkly environment key. Example: `production`. Only present in automatic configuration or offline mode. |
| `project.key` | The LaunchDarkly project key. Example: `my-application`. Only present in automatic configuration or offline mode. |

The request metrics (`http.server.active_requests`, `http.server.request.duration`, and `launchdarkly.relay.events.received.size`) additionally include:

| Attribute | Description |
|-----------|-------------|
| `platform.category` | The kind of SDK that generated the metric: `server`, `mobile`, or `browser`. |
| `user_agent` | The user agent of the SDK making the request. Example: `Node/3.4.0` |
| `sdk.wrapper` | The SDK wrapper identifier, if provided. Example: `flutter-client/2.0.0` |
| `http.route` | The request URL path template. Variables appear as placeholders rather than actual values. Example: `/sdk/evalx/{envId}/contexts/{context}` |
| `http.request.method` | The HTTP method. Example: `GET` |
| `url.scheme` | The URL scheme. Example: `https` |
| `application.id` | The application identifier, extracted from the `application-id` field of the `X-LaunchDarkly-Tags` header. |
| `application.version` | The application version, extracted from the `application-version` field of the `X-LaunchDarkly-Tags` header. |
| `instance.id` | The SDK instance identifier from the `X-LaunchDarkly-Instance-Id` header. |

The health metrics additionally include, where noted above:

| Attribute | Description |
|-----------|-------------|
| `state` | The state a series reports on: `initializing`, `valid`, `interrupted`, or `off` for the data source; `initializing`, `valid`, or `interrupted` for the data store. |
| `status` | On `launchdarkly.relay.environments`: `connected` or `disconnected`. |
| `error.type` | On `launchdarkly.relay.data_source.errors`: `network_error`, `error_response`, `invalid_data`, `store_error`, or `unknown`. |
| `db.system` | On the data store metrics when a persistent store is configured: `redis`, `dynamodb`, or `consul`. Not present for the default in-memory store. |

## Backend-specific notes

### Prometheus

Prometheus supports OTLP ingestion natively since v2.47.0. Enable it with `--web.enable-otlp-receiver` and configure the Relay Proxy to push metrics to Prometheus's OTLP endpoint:

```
USE_OTLP=true
OTEL_EXPORTER_OTLP_ENDPOINT=http://<prometheus-host>:9090/api/v1/otlp/v1/metrics
OTEL_EXPORTER_OTLP_PROTOCOL=http
```

Prometheus converts OpenTelemetry metric names by replacing dots with underscores, so the metrics will appear as `http_server_active_requests`, `http_server_request_duration_seconds`, `launchdarkly_relay_events_received_size_total`, etc.

### Datadog

The [Datadog Agent](https://docs.datadoghq.com/opentelemetry/setup/otlp_ingest_in_the_agent/) can accept OTLP metrics directly, but OTLP ingestion must be enabled in the Agent's `datadog.yaml`:

```yaml
otlp_config:
  receiver:
    protocols:
      grpc:
        endpoint: "0.0.0.0:4317"
```

Then configure the Relay Proxy:

```
USE_OTLP=true
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta
```

**Important:** Datadog requires delta aggregation temporality. You must set `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta` or Datadog may discard data points. The OpenTelemetry SDK defaults to cumulative temporality.

The `service.name` resource attribute (set via `OTEL_SERVICE_NAME`) maps to Datadog's `service` tag. You can also set `deployment.environment.name` and `service.version` via `OTEL_RESOURCE_ATTRIBUTES` to populate Datadog's unified service tags:

```
OTEL_SERVICE_NAME=ld-relay
OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=production,service.version=9.0.0
```

### OpenTelemetry Collector

For more complex setups — such as routing metrics to multiple backends simultaneously — point the Relay Proxy at an [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/):

```
USE_OTLP=true
OTEL_EXPORTER_OTLP_ENDPOINT=http://<collector-host>:4317
```

The collector can then forward metrics to any supported backend.
