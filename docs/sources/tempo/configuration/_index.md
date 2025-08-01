---
title: Configure Tempo
menuTitle: Configure
description: Learn about available options in Tempo and how to configure them.
weight: 400
---

# Configure Tempo

This document explains the configuration options for Tempo as well as the details of what they impact.

{{< admonition type="tip" >}}
Instructions for configuring Tempo data sources are available in the [Grafana Cloud](/docs/grafana-cloud/send-data/traces/) and [Grafana](/docs/grafana/latest/datasources/tempo/) documentation.
{{< /admonition >}}

The Tempo configuration options include:

- [Configure Tempo](#configure-tempo)
  - [Use environment variables in the configuration](#use-environment-variables-in-the-configuration)
  - [Server](#server)
  - [Distributor](#distributor)
    - [Set max attribute size to help control out of memory errors](#set-max-attribute-size-to-help-control-out-of-memory-errors)
    - [gRPC compression](#grpc-compression)
  - [Ingester](#ingester)
    - [Ingester configuration block](#ingester-configuration-block)
  - [Metrics-generator](#metrics-generator)
  - [Query-frontend](#query-frontend)
    - [Limit query size to improve performance and stability](#limit-query-size-to-improve-performance-and-stability)
      - [Limit the spans per spanset](#limit-the-spans-per-spanset)
      - [Cap the maximum query length](#cap-the-maximum-query-length)
  - [Querier](#querier)
  - [Compactor](#compactor)
  - [Storage](#storage)
    - [Local storage recommendations](#local-storage-recommendations)
    - [Storage block configuration example](#storage-block-configuration-example)
  - [Memberlist](#memberlist)
  - [Configuration blocks](#configuration-blocks)
    - [Block config](#block-config)
    - [Filter policy config](#filter-policy-config)
      - [Filter policy](#filter-policy)
      - [Policy match](#policy-match)
      - [Examples](#examples)
    - [KVStore config](#kvstore-config)
    - [Search config](#search-config)
    - [WAL config](#wal-config)
  - [Overrides](#overrides)
    - [Ingestion limits](#ingestion-limits)
      - [Standard overrides](#standard-overrides)
      - [Tenant-specific overrides](#tenant-specific-overrides)
        - [Runtime overrides](#runtime-overrides)
        - [User-configurable overrides](#user-configurable-overrides)
      - [Override strategies](#override-strategies)
  - [Usage-report](#usage-report)
    - [Configure usage-reporting](#configure-usage-reporting)
  - [Cache](#cache)

Additionally, you can review [TLS](network/tls/) to configure the cluster components to communicate over TLS, or receive traces over TLS.

## Use environment variables in the configuration

You can use environment variable references in the configuration file to set values that need to be configurable during deployment. To do this, pass `-config.expand-env=true` and use:

```
${VAR}
```

Where `VAR` is the name of the environment variable.

Each variable reference is replaced at startup by the value of the environment variable.
The replacement is case-sensitive and occurs before the YAML file is parsed.
References to undefined variables are replaced by empty strings unless you specify a default value or custom error text.

To specify a default value, use:

```
${VAR:-default_value}
```

where `default_value` is the value to use if the environment variable is undefined.

You can find more about other supported syntax [here](https://github.com/drone/envsubst/blob/master/readme.md).

## Server

Tempo uses the server from `dskit/server`. For more information on configuration options, refer to [this file](https://github.com/grafana/dskit/blob/main/server/server.go#L66).

```yaml
# Optional. Setting to true enables multitenancy and requires X-Scope-OrgID header on all requests.
[multitenancy_enabled: <bool> | default = false]

# Optional. String prefix for all http api endpoints. Must include beginning slash.
[http_api_prefix: <string>]

server:
    # HTTP server listen host
    [http_listen_address: <string>]

    # HTTP server listen port
    [http_listen_port: <int> | default = 3200]

    # gRPC server listen host
    [grpc_listen_address: <string>]

    # gRPC server listen port
    [grpc_listen_port: <int> | default = 9095]

    # Register instrumentation handlers (/metrics, etc.)
    [register_instrumentation: <boolean> | default = true]

    # Timeout for graceful shutdowns
    [graceful_shutdown_timeout: <duration> | default = 30s]

    # Read timeout for HTTP server
    [http_server_read_timeout: <duration> | default = 30s]

    # Write timeout for HTTP server
    [http_server_write_timeout: <duration> | default = 30s]

    # Idle timeout for HTTP server
    [http_server_idle_timeout: <duration> | default = 120s]

    # Max gRPC message size that can be received
    # This value may need to be increased if you have large traces
    [grpc_server_max_recv_msg_size: <int> | default = 16777216]

    # Max gRPC message size that can be sent
    # This value may need to be increased if you have large traces
    [grpc_server_max_send_msg_size: <int> | default = 16777216]
```

## Distributor

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/distributor/config.go).

Distributors receive spans and forward them to the appropriate ingesters.

The following configuration enables all available receivers with their default configuration. For a production deployment, enable only the receivers you need.
Additional documentation and more advanced configuration options are available in [the receiver README](https://github.com/open-telemetry/opentelemetry-collector/blob/main/receiver/README.md).

```yaml
# Distributor config block
distributor:

    # receiver configuration for different protocols
    # The config is passed down to OpenTelemetry receivers.
    # By default, receivers listen to localhost and need a configured IP to
    # listen on an external interface.
    # For a production deployment, you should only enable the receivers you need.
    receivers:
        otlp:
            protocols:
                grpc:    # default localhost:4317
                http:    # default localhost:4318
        jaeger:
            protocols:
                thrift_http:
                grpc:
                thrift_binary:
                thrift_compact:
        zipkin:
        opencensus:
        kafka:

    # Optional.
    # Configures forwarders that asynchronously replicate ingested traces
    # to specified endpoints. Forwarders work on per-tenant basis, so to
    # fully enable this feature, overrides configuration must also be updated.
    #
    # Note: Forwarders work asynchronously and can fail or decide not to forward
    # some traces. This feature works in a "best-effort" manner.
    forwarders:

        # Forwarder name. Must be unique within the list of forwarders.
        # This name can be referenced in the overrides configuration to
        # enable forwarder for a tenant.
      - name: <string>

        # The forwarder backend to use
        # Should be "otlpgrpc".
        backend: <string>

        # otlpgrpc configuration. Will be used only if value of backend is "otlpgrpc".
        otlpgrpc:

          # List of otlpgrpc compatible endpoints.
          endpoints: <list of string>
          tls:

            # Optional.
            # Disables TLS if set to true.
            [insecure: <boolean> | default = false]

            # Optional.
            # Path to the TLS certificate. This field must be set if insecure = false.
            [cert_file: <string | default = "">]

        # Optional.
        # Configures filtering in forwarder that lets you drop spans and span events using
        # the OpenTelemetry Transformation Language (OTTL) syntax. For detailed overview of
        # the OTTL syntax, please refer to the official Open Telemetry documentation.
        filter:
            traces:
                span: <list of string>
                spanevent: <list of string>
      - (repetition of above...)


    # Optional.
    # Enable to log every received span to help debug ingestion or calculate span error distributions using the logs.
    # This is not recommended for production environments
    log_received_spans:
        [enabled: <boolean> | default = false]
        [include_all_attributes: <boolean> | default = false]
        [filter_by_status_error: <boolean> | default = false]

    # Optional.
    # Enable to log every discarded span to help debug ingestion or calculate span error distributions using the logs.
    log_discarded_spans:
        [enabled: <boolean> | default = false]
        [include_all_attributes: <boolean> | default = false]
        [filter_by_status_error: <boolean> | default = false]

    # Optional.
    # Enable to metric every received span to help debug ingestion
    # This is not recommended for production environments
    metric_received_spans:
        [enabled: <boolean> | default = false]
        [root_only: <boolean> | default = false]

    # Optional.
    # Disables write extension with inactive ingesters. Use this along with ingester.lifecycler.unregister_on_shutdown = true
    #  note that setting these two config values reduces tolerance to failures on rollout b/c there is always one guaranteed to be failing replica
    [extend_writes: <bool>]

    # Optional.
    # Configures the time to retry after returned to the client when Tempo returns a GRPC ResourceExhausted. This parameter
    # defaults to 0 which means that by default ResourceExhausted is not retried. Set this to a duration such as `1s` to
    # instruct the client how to retry.
    [retry_after_on_resource_exhausted: <duration> | default = '0' ]

    # Optional
    # Configures the max size an attribute can be. Any key or value that exceeds this limit will be truncated before storing
    # Setting this parameter to '0' would disable this check against attribute size
    [max_attribute_bytes: <int> | default = '2048']

    # Optional.
    # Configures usage trackers in the distributor which expose metrics of ingested traffic grouped by configurable
    # attributes exposed on /usage_metrics.
    usage:
        cost_attribution:
            # Enables the "cost-attribution" usage tracker. Per-tenant attributes are configured in overrides.
            [enabled: <boolean> | default = false]
            # Maximum number of series per tenant.
            [max_cardinality: <int> | default = 10000]
            # Interval after which a series is considered stale and will be deleted from the registry.
            # Once a metrics series is deleted, it won't be emitted anymore, keeping active series low.
            [stale_duration: <duration> | default = 15m0s]
```

### Set max attribute size to help control out of memory errors

Tempo queriers can run out of memory when fetching traces that have spans with very large attributes.
This issue has been observed when trying to fetch a single trace using the [`tracebyID` endpoint](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#query).
While a trace might not have a lot of spans (roughly 500), it can have a larger size (approximately 250KB).
Some of the spans in that trace had attributes whose values were very large in size.

To avoid these out-of-memory crashes, use `max_attribute_bytes` to limit the maximum allowable size of any individual attribute.
Any key or values that exceed the configured limit are truncated before storing.
The default value is `2048`.

Use the `tempo_distributor_attributes_truncated_total` metric to track how many attributes are truncated.

For additional information, refer to [Troubleshoot out-of-memory errors](https://grafana.com/docs/tempo/<TEMPO_VERSION>/troubleshooting/out-of-memory-errors/).

### gRPC compression

Starting with Tempo 2.7.1, gRPC compression between all components defaults to `snappy`.
Using `snappy` provides a balanced approach to compression between components that will work for most installations.

If you prefer a different balance of CPU/Memory and bandwidth, consider disabling compression or using `zstd`.

For a discussion on alternatives, refer to [this discussion thread](https://github.com/grafana/tempo/discussions/4683). ([#4696](https://github.com/grafana/tempo/pull/4696)).

Disabling compression may provide some performance boosts.
Benchmark testing suggested that without compression, queriers and distributors used less CPU and memory.

However, you may notice an increase in ingester data and network traffic especially for larger clusters.
This increased data can impact billing for Grafana Cloud.

You can configure the gRPC compression in the `querier`, `ingester`, and `metrics_generator` clients of the distributor.

To disable compression, remove `snappy` from the `grpc_compression` lines.

To re-enable the compression, use `snappy` with the following settings:

```yaml
ingester_client:
  grpc_client_config:
    grpc_compression: 'snappy'
metrics_generator_client:
  grpc_client_config:
    grpc_compression: 'snappy'
querier:
  frontend_worker:
    grpc_client_config:
      grpc_compression: 'snappy'
```

## Ingester

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/ingester/config.go).

The ingester is responsible for batching up traces and pushing them to [TempoDB](#storage).

A live, or active, trace is a trace that has received a new batch of spans in more than a configured amount of time (default 10 seconds, set by `ingester.trace_idle_period`).
After 10 seconds (or the configured amount of time), the trace is flushed to disk and appended to the WAL.
When Tempo receives a new batch, a new live trace is created in memory.

Refer to [Data quality metrics](../troubleshooting/querying/long-running-traces) for information about `trace_idle_period` and warning metrics like `disconnected_trace_flushed_to_wal`.

### Ingester configuration block

```yaml
# Ingester configuration block
ingester:

    # Lifecycler is responsible for managing the lifecycle of entries in the ring.
    # For a complete list of config options check the lifecycler section under the ingester config at the following link -
    # https://cortexmetrics.io/docs/configuration/configuration-file/#ingester_config
    lifecycler:
        ring:
            # number of replicas of each span to make while pushing to the backend
            replication_factor: 3
            # set sidecar proxy port
            [port: <int>]

    # amount of time a trace must be idle before flushing it to the wal.
    # (default: 5s)
    [trace_idle_period: <duration>]

    # amount of time after which a trace is flushed to the wal regardless of idle period
    # (default: 30s)
    [trace_live_period: <duration>]

    # how often to sweep all tenants and move traces from live -> wal -> completed blocks.
    # (default: 10s)
    [flush_check_period: <duration>]

    # maximum size of a block before cutting it
    # (default: 524288000 = 500MB)
    [max_block_bytes: <int>]

    # maximum length of time before cutting a block
    # (default: 30m)
    [max_block_duration: <duration>]

    # duration to keep blocks in the ingester after they have been flushed
    # (default: 15m)
    [ complete_block_timeout: <duration>]

    # Flush all traces to backend when ingester is stopped
    [flush_all_on_shutdown: <bool> | default = false]
```

## Metrics-generator

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/generator/config.go).

The metrics-generator processes spans and write metrics using the Prometheus remote write protocol.
For more information on the metrics-generator, refer to the [Metrics-generator documentation](../metrics-from-traces/metrics-generator/).

Metrics-generator processors are disabled by default. To enable it for a specific tenant, set `metrics_generator.processors` in the [overrides](#overrides) section.

{{< admonition type="note" >}}
If you want to enable metrics-generator for your Grafana Cloud account, refer to the [Metrics-generator in Grafana Cloud](https://grafana.com/docs/grafana-cloud/send-data/traces/metrics-generator/) documentation.
{{< /admonition >}}

You can limit spans with end times that occur within a configured duration to be considered in metrics generation using `metrics_ingestion_time_range_slack`.
In Grafana Cloud, this value defaults to 30 seconds so all spans sent to the metrics-generation more than 30 seconds in the past are discarded or rejected.

For more information about the `local-blocks` configuration option, refer to [TraceQL metrics](https://grafana.com/docs/tempo/<TEMPO_VERSION>/metrics-from-traces/metrics-queries/configure-traceql-metrics/#activate-and-configure-the-local-blocks-processor).

```yaml
# Metrics-generator configuration block
metrics_generator:

    # Ring configuration
    ring:
      kvstore: <KVStore config>
        [store: <string> | default = memberlist]
        [prefix: <string> | default = "collectors/"]

      # Period at which to heartbeat the instance
      # 0 disables heartbeat altogether
      [heartbeat_period: <duration> | default = 5s]

      # The heartbeat timeout, after which, the instance is skipped.
      # 0 disables timeout.
      [heartbeat_timeout: <duration> | default = 1m]

      # Our Instance ID to register as in the ring.
      [instance_id: <string> | default = os.Hostname()]

      # Name of the network interface to read address from.
      [instance_interface_names: <list of string> | default = ["eth0", "en0"] ]

      # Our advertised IP address in the ring, (usefull if the local ip =/= the external ip)
      # Will default to the configured `instance_id` ip address,
      # if unset, will fallback to ip reported by `instance_interface_names`
      # (Effected by `enable_inet6`)
      [instance_addr: <string> | default = auto(instance_id, instance_interface_names)]

      # Our advertised port in the ring
      # Defaults to the configured GRPC listing port
      [instance_port: <int> | default = auto(listen_port)]

      # Enables the registering of ipv6 addresses in the ring.
      [enable_inet6: <bool> | default = false]

    # Processor-specific configuration
    processor:

        service_graphs:

            # Wait is the value to wait for an edge to be completed.
            [wait: <duration> | default = 10s]

            # MaxItems is the amount of edges that will be stored in the store.
            [max_items: <int> | default = 10000]

            # Workers is the amount of workers that will be used to process the edges
            [workers: <int> | default = 10]

            # Buckets for the latency histogram in seconds.
            [histogram_buckets: <list of float> | default = 0.1, 0.2, 0.4, 0.8, 1.6, 3.2, 6.4, 12.8]

            # Additional dimensions to add to the metrics. Dimensions are searched for in the
            # resource and span attributes and are added to the metrics if present.
            [dimensions: <list of string>]

            # Prefix additional dimensions with "client_" and "_server". Adds two labels
            # per additional dimension instead of one.
            [enable_client_server_prefix: <bool> | default = false]

            # If enabled another histogram will be produced for interactions over messaging systems middlewares
            # If this feature is relevant over long time ranges (high latencies) - consider increasing
            # `wait` value for this processor.
            [enable_messaging_system_latency_histogram: <bool> | default = false]

            # Attributes that will be used to create a peer edge
            # Attributes are searched in the order they are provided
            # See: https://pkg.go.dev/go.opentelemetry.io/otel/semconv/v1.18.0
            # Example: ["peer.service", "db.name", "db.system", "host.name"]
            [peer_attributes: <list of string> | default = ["peer.service", "db.name", "db.system"] ]

            # Attribute Key to multiply span metrics
            # Note that the attribute name is searched for in both
            # resouce and span level attributes
            [span_multiplier_key: <string> | default = ""]

            # Enables additional labels for services and virtual nodes.
            [enable_virtual_node_label: <bool> | default = false]

        span_metrics:

            # Buckets for the latency histogram in seconds.
            [histogram_buckets: <list of float> | default = 0.002, 0.004, 0.008, 0.016, 0.032, 0.064, 0.128, 0.256, 0.512, 1.024, 2.048, 4.096, 8.192, 16.384]

            # Configure intrinsic dimensions to add to the metrics. Intrinsic dimensions are taken
            # directly from the respective resource and span properties.
            intrinsic_dimensions:
                # Whether to add the name of the service the span is associated with.
                [service: <bool> | default = true]
                # Whether to add the name of the span.
                [span_name: <bool> | default = true]
                # Whether to add the span kind describing the relationship between spans.
                [span_kind: <bool> | default = true]
                # Whether to add the span status code.
                [status_code: <bool> | default = true]
                # Whether to add a status message. Important note: The span status message may
                # contain arbitrary strings and thus have a very high cardinality.
                [status_message: <bool> | default = false]

            # Additional dimensions to add to the metrics along with the intrinsic dimensions.
            # Dimensions are searched for in the resource and span attributes and are added to
            # the metrics if present.
            [dimensions: <list of string>]

            # Custom labeling mapping
            dimension_mappings: <list of label mappings>
                # The new label name
              - [name: <string>]
                # The actual attributes that will make the value of the new label
                [source_labels: <list of strings>]
                # The separator used to join multiple `source_labels`
                [join: <string>]

            # Enable traces_target_info metrics
            [enable_target_info: <bool> | default = false]

            # Attribute Key to multiply span metrics
            # Note that the attribute name is searched for in both
            # resouce and span level attributes
            [span_multiplier_key: <string> | default = ""]

            # List of policies that will be applied to spans for inclusion or exclusion.
            [filter_policies: <list of filter policies config> | default = []]

            # Drop specific labels from `traces_target_info` metrics
            [target_info_excluded_dimensions: <list of string>]

        local_blocks:

            # Block configuration
            block: <Block config>

            # Search configuration
            search: <Search config>

            # How often to run the flush loop to cut idle traces and blocks
            [flush_check_period: <duration> | default = 10s]

            # A trace is considered complete after this period of inactivity (no new spans received)
            [trace_idle_period: <duration> | default = 5s]

            # A trace is flushed after this period regardless of whether it is still receiving spans or not
            [trace_live_period: <duration> | default = 30s]

            # Maximum duration which the head block can be appended to, before cutting it.
            [max_block_duration: <duration> | default = 1m]

            # Maximum size of the head block, before cutting it
            [max_block_bytes: <uint64> | default = 500000000]

            # Duration to keep blocks in the ingester after they have been flushed
            [complete_block_timeout: <duration> | default = 1h]

            # Maximum amount of live traces
            # If this value is exceeded, traces will be dropped with reason: `live_traces_exceeded`
            # A value of 0 disables this limit.
            [max_live_traces: <uint64>]

            # Whether server spans should be filtered in or not.
            # If enabled, only parent spans or spans with the SpanKind of `server` will be retained
            [filter_server_spans: <bool> | default = true]

            # Whether server spans should be flushed to storage.
            # Setting `flush_to_storage` to `true` ensures that metrics blocks are flushed to storage so TraceQL metrics queries against historical data.
            [flush_to_storage: <bool> | default = false]

            # Number of blocks that are allowed to be processed concurrently.
            [concurrent_blocks: <uint> | default = 10]

            # A tuning factor that controls whether the trace-level timestamp columns are used in a metrics query.
            # If a block overlaps the time window by less than this ratio, then the columns are skipped.
            # A value of 1.0 will always load the columns, and 0.0 will never load any.
            [time_overlap_cutoff: <float64> | default = 0.2]

    # Registry configuration
    registry:

        # Interval to collect metrics and remote write them.
        [collection_interval: <duration> | default = 15s]

        # Interval after which a series is considered stale and will be deleted from the registry.
        # Once a metrics series is deleted, it won't be emitted anymore, keeping active series low.
        [stale_duration: <duration> | default = 15m]

        # A list of labels that will be added to all generated metrics.
        [external_labels: <map>]

        # If set, the tenant ID will added as label with the given label name to all generated metrics.
        [inject_tenant_id_as: <string>]

        # The maximum length of label names. Label names exceeding this limit will be truncated.
        [max_label_name_length: <int> | default = 1024]

        # The maximum length of label values. Label values exceeding this limit will be truncated.
        [max_label_value_length: <int> | default = 2048]

    # Configuration block for the Write Ahead Log (WAL)
    traces_storage: <WAL config>

      # Path to store the WAL files.
      # Must be set.
      # Example: "/var/tempo/generator/traces"
      [path: <string> | default = ""]

    # Storage and remote write configuration
    storage:

        # Path to store the WAL. Each tenant will be stored in its own subdirectory.
        path: <string>

        # Configuration for the Prometheus Agent WAL
        # https://github.com/prometheus/prometheus/blob/v2.51.2/tsdb/agent/db.go#L62-L84
        wal: <prometheus agent WAL config>

        # How long to wait when flushing samples on shutdown
        [remote_write_flush_deadline: <duration> | default = 1m]

        # Whether to add X-Scope-OrgID header in remote write requests
        [remote_write_add_org_id_header: <bool> | default = true]

        # A list of remote write endpoints.
        # https://prometheus.io/docs/prometheus/latest/configuration/configuration/#remote_write
        remote_write:
            [- <Prometheus remote write config>]

    # This option only allows spans with end times that occur within the configured duration to be
    # considered in metrics generation.
    # This is to filter out spans that are outdated.
    [metrics_ingestion_time_range_slack: <duration> | default = 30s]

    # Timeout for metric requests
    [query_timeout: <duration> | default = 30s ]

    # Overides the key used to register the metrics-generator in the ring.
    [override_ring_key: <string> | default = "metrics-generator"]
```

## Query-frontend

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/frontend/config.go).

The Query Frontend is responsible for sharding incoming requests for faster processing in parallel (by the queriers).

```yaml
# Query Frontend configuration block
query_frontend:

    # number of times to retry a request sent to a querier
    # (default: 2)
    [max_retries: <int>]

    # The number of goroutines dedicated to consuming, unmarshalling and recombining responses per request. This
    # same parameter is used for all endpoints.
    # (default: 10)
    [response_consumers: <int>]

    # Maximum number of outstanding requests per tenant per frontend; requests beyond this error with HTTP 429.
    # (default: 2000)
    [max_outstanding_per_tenant: <int>]

    # The number of jobs to batch together in one http request to the querier. Set to 1 to
    # disable.
    # (default: 7)
    [max_batch_size: <int>]

    # Enable multi-tenant queries.
    # If enabled, queries can be federated across multiple tenants.
    # The tenant IDs involved need to be specified separated by a '|'
    # character in the 'X-Scope-OrgID' header.
    # note: this is no-op if cluster doesn't have `multitenancy_enabled: true`
    # (default: true)
    [multi_tenant_queries_enabled: <bool>]

    # Comma-separated list of request header names to include in query logs. Applies
    # to both query stats and slow queries logs.
    [log_query_request_headers: <string> | default = ""]

    # Set a maximum timeout for all api queries at which point the frontend will cancel queued jobs
    # and return cleanly. HTTP will return a 503 and GRPC will return a context canceled error.
    # This timeout impacts all http and grpc streaming queries as part of the Tempo api surface such as
    # search, metrics summary, tags and tag values lookups, etc.
    # Generally it is preferred to let the client cancel context. This is a failsafe to prevent a client
    # from imposing more work on Tempo than desired.
    # (default: 0)
    [api_timeout: <duration>]

    # A list of regular expressions for refusing matching requests, these will apply for every request regardless of the endpoint.
    [url_deny_list: <list of strings> | default = <empty list>]]

    # Max allowed TraceQL expression size, in bytes. queries bigger then this size will be rejected.
    # (default: 128 KiB)
    [max_query_expression_size_bytes: <int> | default = 131072]]

    search:

        # The number of concurrent jobs to execute when searching the backend.
        # (default: 1000)
        [concurrent_jobs: <int>]

        # The target number of bytes for each job to handle when performing a backend search.
        # (default: 104857600)
        [target_bytes_per_job: <int>]

        # Limit used for search requests if none is set by the caller
        # (default: 20)
        [default_result_limit: <int>]

        # The maximum allowed value of the limit parameter on search requests. If the search request limit parameter
        # exceeds the value configured here the frontend will return a 400.
        # The default value of 0 disables this limit.
        # (default: 0)
        [max_result_limit: <int>]

        # The maximum allowed time range for a search.
        # 0 disables this limit.
        # (default: 168h)
        [max_duration: <duration>]

        # query_backend_after and query_ingesters_until together control where the query-frontend searches for traces.
        # Time ranges before query_ingesters_until will be searched in the ingesters only.
        # Time ranges after query_backend_after will be searched in the backend/object storage only.
        # Time ranges from query_backend_after through query_ingesters_until will be queried from both locations.
        # query_backend_after must be less than or equal to query_ingesters_until.
        # (default: 15m)
        [query_backend_after: <duration>]

        # (default: 30m)
        [query_ingesters_until: <duration>]

        # If set to a non-zero value, it's value will be used to decide if query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        # NOTE: Requires `duration_slo` AND `throughput_bytes_slo` to be configured.
        [duration_slo: <duration> | default = 0s ]

        # If set to a non-zero value, it's value will be used to decide if query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        [throughput_bytes_slo: <float> | default = 0 ]

        # The number of time windows to break a search up into when doing a most recent TraceQL search. This only impacts TraceQL
        # searches with (most_recent=true).
        [most_recent_shards: <int> | default = 200]

        # The number of shards to break ingester queries into.
        [ingester_shards: <int> | default = 3]

        # The maximum allowed value of spans per span set. 0 disables this limit.
        [max_spans_per_span_set: <int> | default = 100]

        # SLO configuration for Metadata (tags and tag values) endpoints.
        metadata_slo:
            # If set to a non-zero value, it's value will be used to decide if metadata query is within SLO or not.
            # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
            # NOTE: Requires `duration_slo` AND `throughput_bytes_slo` to be configured.
            [duration_slo: <duration> | default = 0s ]


            # If set to a non-zero value, it's value will be used to decide if metadata query is within SLO or not.
            # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
            [throughput_bytes_slo: <float> | default = 0 ]

    # Trace by ID lookup configuration
    trace_by_id:
        # The number of shards to split a trace by id query into.
        # (default: 50)
        [query_shards: <int>]

        # The maximum number of shards to execute at once. If set to 0 query_shards is used.
        # (default: 0)
        [concurrent_shards: <int>]

        # If set to a non-zero value, it's value will be used to decide if metadata query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        # NOTE: Requires `duration_slo` AND `throughput_bytes_slo` to be configured.
        [duration_slo: <duration> | default = 0s ]

        # If set to a non-zero value, it's value will be used to decide if metadata query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        [throughput_bytes_slo: <float> | default = 0 ]

    # Metrics query configuration
    metrics:
        # The number of concurrent jobs to execute when querying the backend.
        [concurrent_jobs: <int> | default = 1000 ]

        # The target number of bytes for each job to handle when querying the backend.
        [target_bytes_per_job: <int> | default = 100MiB ]

        # The maximum allowed time range for a metrics query.
        # 0 disables this limit.
        [max_duration: <duration> | default = 3h ]

        # Maximun number of exemplars per range query. Limited to 100.
        [max_exemplars: <int> | default = 100 ]

        # Maximum number of time series returned for a metrics query.
        # Default is 0, which means there is no limit
        [max_response_series: <int> | default = 0]

        # query_backend_after controls where the query-frontend searches for traces.
        # Time ranges older than query_backend_after will be searched in the backend/object storage only.
        # Time ranges between query_backend_after and now will be queried from the metrics-generators.
        [query_backend_after: <duration> | default = 30m ]

        # The target length of time for each job to handle when querying the backend.
        [interval: <duration> | default = 5m ]

        # If set to a non-zero value, it's value will be used to decide if query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        # NOTE: `duration_slo` and `throughput_bytes_slo` both must be configured for it to work
        [duration_slo: <duration> | default = 0s ]

        # If set to a non-zero value, it's value will be used to decide if query is within SLO or not.
        # Query is within SLO if it returned 200 within duration_slo seconds OR processed throughput_slo bytes/s data.
        [throughput_bytes_slo: <float> | default = 0 ]

```

### Limit query size to improve performance and stability

Querying large tracing data presents several challenges.
Span sets with large number of spans impact query performance and stability.
In a similar manner, excessive queries result size can also negatively impact query performance.

#### Limit the spans per spanset

You can set the maximum spans per spanset by setting `max_spans_per_span_set` for the query-frontend.
The default value is 100.

In Grafana or Grafana Cloud, you can use the **Span Limit** field in the [TraceQL query editor](https://grafana.com/docs/grafana-cloud/connect-externally-hosted/data-sources/tempo/query-editor/) in Grafana Explore.
This field sets the maximum number of spans to return for each span set.
The maximum value that you can set for the **Span Limit** value (or the spss query) is controlled by `max_spans_per_span_set`.
To disable the maximum spans per span set limit, set `max_spans_per_span_set` to `0`.
When set to `0`, there is no maximum and users can put any value in **Span Limit**.
However, this can only be set by a Tempo administrator, not by the user.

#### Cap the maximum query length

You can set the maximum length of a query using `query_frontend.max_query_expression_size_bytes` configuration parameter for the query-frontend. The default value is 128 KB.

This limit is used to protect the system’s stability from potential abuse or mistakes, when running a large potentially expensive query.

You can set the value lower of higher by setting it in the `query_frontend` configuration section, for example:

```
query_frontend:
  max_query_expression_size_bytes: 10000
```

## Querier

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/querier/config.go).

The Querier is responsible for querying the backends/cache for the traceID.

```yaml
# querier config block
querier:

    # The query frontend turns both trace by id (/api/traces/<id>) and search (/api/search?<params>) requests
    # into subqueries that are then pulled and serviced by the queriers.
    # This value controls the overall number of simultaneous subqueries that the querier will service at once. It does
    # not distinguish between the types of queries.
    [max_concurrent_queries: <int> | default = 20]

    # If shuffle sharding is enabled, queriers fetch in-memory traces from the minimum set of required ingesters,
    # selecting only ingesters which might have received series since now - <ingester flush period>. Otherwise, the
    # request is sent to all ingesters.
    [shuffle_sharding_ingesters_enabled: <bool> | default = true]

    # Lookback period to include ingesters that were part of the shuffle sharded subring.
    [shuffle_sharding_ingesters_lookback_period: <duration> | default = 1hr]

    # The query frontend sends sharded requests to ingesters and querier (/api/traces/<id>)
    # By default, all healthy ingesters are queried for the trace id.
    # When true the querier will hash the trace id in the same way that distributors do and then
    # only query those ingesters who own the trace id hash as determined by the ring.
    # If this parameter is set, the number of 404s could increase during rollout or scaling of ingesters.
    [query_relevant_ingesters: <bool> | default = false]

    trace_by_id:
        # Timeout for trace lookup requests
        [query_timeout: <duration> | default = 10s]

    search:
        # Timeout for search requests
        [query_timeout: <duration> | default = 30s]

    # config of the worker that connects to the query frontend
    frontend_worker:

        # the address of the query frontend to connect to, and process queries
        # Example: "frontend_address: query-frontend-discovery.default.svc.cluster.local:9095"
        [frontend_address: <string>]
```

It also queries compacted blocks that fall within the (2 \* BlocklistPoll) range where the value of Blocklist poll duration
is defined in the storage section below.

## Compactor

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/modules/compactor/config.go).

Compactors stream blocks from the storage backend, combine them and write them back. Values shown below are the defaults.

```yaml
compactor:

    # Optional. Disables backend compaction. Default is false.
    # Note: This should only be used in a non-production context for debugging purposes. This will allow blocks to say in the backend for further investigation if desired.
    [disabled: <bool>]

    ring:
        kvstore: <KVStore config>
            [store: <string> | default = memberlist]
            [prefix: <string> | default = "collectors/" ]

    compaction:

        # Optional. Duration to keep blocks. Default is 14 days (336h).
        [block_retention: <duration>]

        # Optional. Duration to keep blocks that have been compacted elsewhere. Default is 1h.
        [compacted_block_retention: <duration>]

        # Optional. Blocks in this time window will be compacted together. Default is 1h.
        [compaction_window: <duration>]

        # Optional. Maximum number of traces in a compacted block. Default is 6 million.
        # WARNING: Deprecated. Use max_block_bytes instead.
        [max_compaction_objects: <int>]

        # Optional. Maximum size of a compacted block in bytes. Default is 100 GB.
        [max_block_bytes: <int>]

        # Optional. Number of tenants to process in parallel during retention. Default is 10.
        [retention_concurrency: <int>]

        # Optional. The maximum amount of time to spend compacting a single tenant before moving to the next. Default is 5m.
        [max_time_per_tenant: <duration>]

        # Optional. The time between compaction cycles. Default is 30s.
        # Note: The default will be used if the value is set to 0.
        [compaction_cycle: <duration>]

        # Optional. Amount of data to buffer from input blocks. Default is 5 MiB.
        [v2_in_buffer_bytes: <int>]

        # Optional. Flush data to backend when buffer is this large. Default is 20 MB.
        [v2_out_buffer_bytes: <int>]

        # Optional. Number of traces to buffer in memory during compaction. Increasing may improve performance but will also increase memory usage. Default is 1000.
        [v2_prefetch_traces_count: <int>]
```

## Storage

Tempo supports Amazon S3, GCS, Azure, and local file system for storage. In addition, you can use Memcached or Redis for increased query performance.

For more information on configuration options, refer to [this file](https://github.com/grafana/tempo/blob/main/tempodb/config.go).

### Local storage recommendations

While you can use local storage, object storage is recommended for production workloads.
A local backend won't correctly retrieve traces with a distributed deployment unless all components have access to the same disk.
Tempo is designed for object storage more than local storage.

At Grafana Labs, we've run Tempo with SSDs when using local storage.
Hard drives haven't been tested.

You can estimate how much storage space you need by considering the ingested bytes and retention.
For example, ingested bytes per day _times_ retention days = stored bytes.

You can not use both local and object storage in the same Tempo deployment.

### Storage block configuration example

The storage block configures TempoDB.
The following example shows common options.
For further platform-specific information, refer to the following:

- [GCS](hosted-storage/gcs/)
- [S3](hosted-storage/s3/)
- [Azure](hosted-storage/azure/)
- [Parquet](parquet/)

```yaml
# Storage configuration for traces
storage:

    trace:

        # The storage backend to use
        # Should be one of "gcs", "s3", "azure" or "local" (only supported in the monolithic mode)
        # CLI flag -storage.trace.backend
        [backend: <string>]

        # GCS configuration. Will be used only if value of backend is "gcs"
        # Check the GCS doc within this folder for information on GCS specific permissions.
        gcs:

            # Bucket name in gcs
            # Tempo requires a bucket to maintain a top-level object structure. You can use prefix option with this to nest all objects within a shared bucket.
            # Example: "bucket_name: tempo"
            [bucket_name: <string>]

            # optional.
            # Prefix name in gcs
            # Tempo has this additional option to support a custom prefix to nest all the objects withing a shared bucket.
            [prefix: <string>]

            # Buffer size for reads. Default is 10MB
            # Example: "chunk_buffer_size: 5_000_000"
            [chunk_buffer_size: <int>]

            # Optional
            # Api endpoint override
            # Example: "endpoint: https://storage.googleapis.com/storage/v1/"
            [endpoint: <string>]

            # Optional. Default is false.
            # Example: "insecure: true"
            # Set to true to disable authentication and certificate checks on gcs requests
            [insecure: <bool>]

            # The number of list calls to make in parallel to the backend per instance.
            # Adjustments here will impact the polling time, as well as the number of Go routines.
            # Default is 3
            [list_blocks_concurrency: <int>]

            # Optional. Default is 0 (disabled)
            # Example: "hedge_requests_at: 500ms"
            # If set to a non-zero value a second request will be issued at the provided duration. Recommended to
            # be set to p99 of GCS requests to reduce long tail latency. This setting is most impactful when
            # used with queriers and has minimal to no impact on other pieces.
            [hedge_requests_at: <duration>]

            # Optional. Default is 2
            # Example: "hedge_requests_up_to: 2"
            # The maximum number of requests to execute when hedging. Requires hedge_requests_at to be set.
            [hedge_requests_up_to: <int>]

            # Optional
            # Example: "object_cache_control: "no-cache""
            # A string to specify the behavior with respect to caching of the objects stored in GCS.
            # See the GCS documentation for more detail: https://cloud.google.com/storage/docs/metadata
            [object_cache_control: <string>]

            # Optional
            # Example: "object_metadata: {'key': 'value'}"
            # A map key value strings for user metadata to store on the GCS objects.
            # See the GCS documentation for more detail: https://cloud.google.com/storage/docs/metadata
            [object_metadata: <map[string]string>]


        # S3 configuration. Will be used only if value of backend is "s3"
        # Check the S3 doc within this folder for information on s3 specific permissions.
        s3:

            # Bucket name in s3
            # Tempo requires a bucket to maintain a top-level object structure. You can use prefix option with this to nest all objects within a shared bucket.
            [bucket: <string>]

            # optional.
            # Prefix name in s3
            # Tempo has this additional option to support a custom prefix to nest all the objects withing a shared bucket.
            [prefix: <string>]

            # api endpoint to connect to. use AWS S3 or any S3 compatible object storage endpoint.
            # Example: "endpoint: s3.dualstack.us-east-2.amazonaws.com"
            [endpoint: <string>]

            # The number of list calls to make in parallel to the backend per instance.
            # Adjustments here will impact the polling time, as well as the number of Go routines.
            # Default is 3
            [list_blocks_concurrency: <int>]

            # optional.
            # By default the region is inferred from the endpoint,
            # but is required for some S3-compatible storage engines.
            # Example: "region: us-east-2"
            [region: <string>]

            # optional.
            # access key when using static credentials.
            [access_key: <string>]

            # optional.
            # secret key when using static credentials.
            [secret_key: <string>]

            # optional.
            # session token when using static credentials.
            [session_token: <string>]

            # optional.
            # enable if endpoint is http
            [insecure: <bool>]

            # optional.
            # Path to the client certificate file.
            [tls_cert_path: <string>]

            # optional.
            # Path to the private client key file.
            [tls_key_path: <string>]

            # optional.
            # Path to the CA certificate file.
            [tls_ca_path: <string>]

            # optional.
            # Path to the CA certificate file.
            [tls_server_name: <string>]

            # optional.
            # Set to true to disable verification of a TLS endpoint. The default value is false.
            [tls_insecure_skip_verify: <bool>]

            # optional.
            # Override the default cipher suite list, separated by commas.
            [tls_cipher_suites: <string>]

            # optional.
            # Override the default minimum TLS version. The default value is VersionTLS12. Allowed values: VersionTLS10, VersionTLS11, VersionTLS12, VersionTLS13
            [tls_min_version: <string>]

            # optional.
            # enable to use path-style requests.
            [forcepathstyle: <bool>]

            # Optional.
            # Enable to use dualstack endpoint for DNS resolution.
            # Check out the (S3 documentation on dualstack endpoints)[https://docs.aws.amazon.com/AmazonS3/latest/userguide/dual-stack-endpoints.html]
            [enable_dual_stack: <bool>]

            # Optional. Default is 0
            # Example: "bucket_lookup_type: 0"
            # options: 0: BucketLookupAuto, 1: BucketLookupDNS, 2: BucketLookupPath
            # See the [S3 documentation on virtual-hosted–style and path-style](https://docs.aws.amazon.com/AmazonS3/latest/userguide/VirtualHosting.html#path-style-access) for more detail.
            # See the [Minio-API documentation on opts.BucketLookup](https://github.com/minio/minio-go/blob/master/docs/API.md#newendpoint-string-opts-options-client-error)] for more detail.
            # Notice: ignore this option if `forcepathstyle` is set true, this option allow expose minio's sdk configure.
            [bucket_lookup_type: <int> | default = 0]

            # Optional. Default is 0 (disabled)
            # Example: "hedge_requests_at: 500ms"
            # If set to a non-zero value a second request will be issued at the provided duration. Recommended to
            # be set to p99 of S3 requests to reduce long tail latency. This setting is most impactful when
            # used with queriers and has minimal to no impact on other pieces.
            [hedge_requests_at: <duration>]

            # Optional. Default is 2
            # Example: "hedge_requests_up_to: 2"
            # The maximum number of requests to execute when hedging. Requires hedge_requests_at to be set.
            [hedge_requests_up_to: <int>]

            # Optional
            # Example: "tags: {'key': 'value'}"
            # A map of key value strings for user tags to store on the S3 objects. This helps set up filters in S3 lifecycles.
            # See the [S3 documentation on object tagging](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-tagging.html) for more detail.
            [tags: <map[string]string>]


            [sse: <map[string]string>]:
              # Optional
              # Example: type: SSE-S3
              # Type of encryption to use with s3 bucket, either SSE-KMS or SSE-S3
              [type: string]:

              # Optional
              # Example: kms_key_id: "1234abcd-12ab-34cd-56ef-1234567890ab"
              # the kms key id is the identification of the key in an account or region
              kms_key_id:
              # Optional
              # Example: kms_encryption_context: "encryptionContext": {"department": "10103.0"}
              # KMS Encryption Context used for object encryption. It expects JSON formatted string
              kms_encryption_context:

        # azure configuration. Will be used only if value of backend is "azure"
        # EXPERIMENTAL
        azure:

            # store traces in this container.
            # Tempo requires bucket to  maintain a top-level object structure. You can use prefix option to nest all objects within a shared bucket
            [container_name: <string>]

            # optional.
            # Prefix for azure.
            # Tempo has this additional option to support a custom prefix to nest all the objects withing a shared bucket.
            [prefix: <string>]

            # optional.
            # Azure endpoint to use, defaults to Azure global(core.windows.net) for other
            # regions this needs to be changed e.g Azure China(blob.core.chinacloudapi.cn),
            # Azure German(blob.core.cloudapi.de), Azure US Government(blob.core.usgovcloudapi.net).
            [endpoint_suffix: <string>]

            # Name of the azure storage account
            [storage_account_name: <string>]

            # optional.
            # access key when using access key credentials.
            [storage_account_key: <string>]

            # optional.
            # use Azure Managed Identity to access Azure storage.
            [use_managed_identity: <bool>]

            # optional.
            # Use a Federated Token to authenticate to the Azure storage account.
            # Enable if you want to use Azure Workload Identity. Expects AZURE_CLIENT_ID,
            # AZURE_TENANT_ID, AZURE_AUTHORITY_HOST and AZURE_FEDERATED_TOKEN_FILE envs to be present
            # (these are set automatically when using Azure Workload Identity).
            [use_federated_token: <bool>]

            # optional.
            # The Client ID for the user-assigned Azure Managed Identity used to access Azure storage.
            [user_assigned_id: <bool>]

            # Optional. Default is 0 (disabled)
            # Example: "hedge_requests_at: 500ms"
            # If set to a non-zero value a second request will be issued at the provided duration. Recommended to
            # be set to p99 of Azure Block Storage requests to reduce long tail latency. This setting is most impactful when
            # used with queriers and has minimal to no impact on other pieces.
            [hedge_requests_at: <duration>]

            # Optional. Default is 2
            # Example: "hedge_requests_up_to: 2"
            # The maximum number of requests to execute when hedging. Requires hedge_requests_at to be set.
            [hedge_requests_up_to: <int>]

        # How often to repoll the backend for new blocks. Default is 5m
        [blocklist_poll: <duration>]

        # Number of blocks to process in parallel during polling. Default is 50.
        [blocklist_poll_concurrency: <int>]

        # By default components will pull the blocklist from the tenant index. If that fails the component can
        # fallback to scanning the entire bucket. Set to false to disable this behavior. Default is true.
        [blocklist_poll_fallback: <bool>]

        # Maximum number of compactors that should build the tenant index. All other components will download
        # the index. Default 2.
        [blocklist_poll_tenant_index_builders: <int>]

        # Number of tenants to poll concurrently. Default is 1.
        [blocklist_poll_tenant_concurrency: <int>]

        # The oldest allowable tenant index. If an index is pulled that is older than this duration,
        # the polling will consider this an error. Note that `blocklist_poll_fallback` applies here.
        # If fallback is true and a tenant index exceeds this duration, it will fall back to listing
        # the bucket contents.
        # Default 0 (disabled).
        [blocklist_poll_stale_tenant_index: <duration>]

        # Offsets the concurrent blocklist polling by a random amount. The maximum amount of offset
        # is the provided value in milliseconds. This configuration value can be used if the polling
        # cycle is overwhelming your backend with concurrent requests.
        # Default 0 (disabled)
        [blocklist_poll_jitter_ms: <int>]

        # Polling will tolerate this many consecutive errors during the poll of
        # a single tenant before marking the tenant as failed.
        # This can be set to 0 which means a single error is sufficient to mark the tenant failed
        # and exit early.  Any previous results for the failing tenant will be kept.
        # See also `blocklist_poll_tolerate_tenant_failures` below.
        # Default 1
        [blocklist_poll_tolerate_consecutive_errors: <int>]

        # Polling will tolerate this number of tenants which have failed to poll.
        # This can be set to 0 which means a single tenant failure  sufficient to fail and exit
        # early.
        # Default 1
        [blocklist_poll_tolerate_tenant_failures: <int>]

        # Used to tune how quickly the poller will delete any remaining backend
        # objects found in the tenant path.  This functionality requires enabling
        # below.
        # Default: 12h
        [empty_tenant_deletion_age: <duration>]

        # Polling will delete the index for a tenant if no blocks are found to
        # exist.  If this setting is enabled, the poller will also delete any
        # remaining backend objects found in the tenant path.  This is used to
        # clean up partial blocks which may have not been cleaned up by the
        # retention.
        [empty_tenant_deletion_enabled: <bool> | default = false]

        # Cache type to use. Should be one of "redis", "memcached"
        # Example: "cache: memcached"
        # Deprecated. See [cache](#cache) section.
        [cache: <string>]

        # Minimum compaction level of block to qualify for bloom filter caching. Default is 0 (disabled), meaning
        # that compaction level is not used to determine if the bloom filter should be cached.
        # Example: "cache_min_compaction_level: 2"
        [cache_min_compaction_level: <int>]

        # Max block age to qualify for bloom filter caching. Default is 0 (disabled), meaning that block age is not
        # used to determine if the bloom filter should be cached.
        # Example: "cache_max_block_age: 48h"
        [cache_max_block_age: <duration>]

        # Configuration parameters that impact trace search
        search: <Search config>

        # Background cache configuration. Requires having a cache configured.
        # Deprecated. See [cache](#cache) section.
        background_cache:

        # Memcached caching configuration block
        # Deprecated. See [cache](#cache) section.
        memcached:

        # Redis configuration block
        # EXPERIMENTAL
        # Deprecated. See [cache](#cache) section.
        redis:

        # the worker pool is used primarily when finding traces by id, but is also used by other
        pool:

            # total number of workers pulling jobs from the queue
            [max_workers: <int> | default = 30]

            # length of job queue. important for querier as it queues a job for every block it has to search
            [queue_depth: <int> | default = 10000 ]

        # configuration block for the Write Ahead Log (WAL)
        wal: <WAL config>
          [path: <string> | default = "/var/tempo/wal"]
          [v2_encoding: <string> | default = snappy]
          [search_encoding: <string> | default = none]
          [ingestion_time_range_slack: <duration> | default = 2m]

        # block configuration
        block: <Block config>
```

## Memberlist

[Memberlist](https://github.com/hashicorp/memberlist) is the default mechanism for all of the Tempo pieces to coordinate with each other.

```yaml
memberlist:
    # Name of the node in memberlist cluster. Defaults to hostname.
    [node_name: <string> | default = ""]

    # Add random suffix to the node name.
    [randomize_node_name: <boolean> | default = true]

    # The timeout for establishing a connection with a remote node, and for
    # read/write operations.
    [stream_timeout: <duration> | default = 10s]

    # Multiplication factor used when sending out messages (factor * log(N+1)).
    [retransmit_factor: <int> | default = 2]

    # How often to use pull/push sync.
    [pull_push_interval: <duration> | default = 30s]

    # How often to gossip.
    [gossip_interval: <duration> | default = 1s]

    # How many nodes to gossip to.
    [gossip_nodes: <int> | default = 2]

    # How long to keep gossiping to dead nodes, to give them chance to refute their
    # death.
    [gossip_to_dead_nodes_time: <duration> | default = 30s]

    # How soon can dead node's name be reclaimed with new address. Defaults to 0,
    # which is disabled.
    [dead_node_reclaim_time: <duration> | default = 0s]

    # Other cluster members to join. Can be specified multiple times. It can be an
    # IP, hostname or an entry specified in the DNS Service Discovery format (see
    # https://cortexmetrics.io/docs/configuration/arguments/#dns-service-discovery
    # for more details).
    # A "Headless" Cluster IP service in Kubernetes.
    # Example:
    #   - gossip-ring.tracing.svc.cluster.local:7946
    [join_members: <list of string> | default = ]

    # Min backoff duration to join other cluster members.
    [min_join_backoff: <duration> | default = 1s]

    # Max backoff duration to join other cluster members.
    [max_join_backoff: <duration> | default = 1m]

    # Max number of retries to join other cluster members.
    [max_join_retries: <int> | default = 10]

    # If this node fails to join memberlist cluster, abort.
    [abort_if_cluster_join_fails: <boolean> | default = true]

    # If not 0, how often to rejoin the cluster. Occasional rejoin can help to fix
    # the cluster split issue, and is harmless otherwise. For example when using
    # only few components as a seed nodes (via -memberlist.join), then it's
    # recommended to use rejoin. If -memberlist.join points to dynamic service that
    # resolves to all gossiping nodes (eg. Kubernetes headless service), then rejoin
    # is not needed.
    [rejoin_interval: <duration> | default = 0s]

    # How long to keep LEFT ingesters in the ring.
    [left_ingesters_timeout: <duration> | default = 5m]

    # Timeout for leaving memberlist cluster.
    [leave_timeout: <duration> | default = 5s]

    # IP address to listen on for gossip messages.
    # Multiple addresses may be specified.
    [bind_addr: <list of string> | default = ["0.0.0.0"] ]

    # Port to listen on for gossip messages.
    [bind_port: <int> | default = 7946]

    # Timeout used when connecting to other nodes to send packet.
    [packet_dial_timeout: <duration> | default = 5s]

    # Timeout for writing 'packet' data.
    [packet_write_timeout: <duration> | default = 5s]

```

## Configuration blocks

Defines re-used configuration blocks.

### Block config

```yaml
# block format version. options: v2, vParquet2, vParquet3, vParquet4
[version: <string> | default = vParquet4]

# bloom filter false positive rate. lower values create larger filters but fewer false positives
[bloom_filter_false_positive: <float> | default = 0.01]

# maximum size of each bloom filter shard
[bloom_filter_shard_size_bytes: <int> | default = 100KiB]

# number of bytes per index record
[v2_index_downsample_bytes: <uint64> | default = 1MiB]

# block encoding/compression. options: none, gzip, lz4-64k, lz4-256k, lz4-1M, lz4, snappy, zstd, s2
[v2_encoding: <string> | default = zstd]

# search data encoding/compression. same options as block encoding.
[search_encoding: <string> | default = snappy]

# number of bytes per search page
[search_page_size_bytes: <int> | default = 1MiB]

# an estimate of the number of bytes per row group when cutting Parquet blocks. lower values will
#  create larger footers but will be harder to shard when searching. It is difficult to calculate
#  this field directly and it may vary based on workload. This is roughly a lower bound.
[parquet_row_group_size_bytes: <int> | default = 100MB]

# Configures attributes to be stored in dedicated columns within the parquet file, rather than in the
# generic attribute key-value list. This allows for more efficient searching of these attributes.
# Up to 10 span attributes and 10 resource attributes can be configured as dedicated columns.
# Requires vParquet3
parquet_dedicated_columns: <list of columns>

      # name of the attribute
    - [name: <string>]

      # type of the attribute. options: string
      [type: <string>]

      # scope of the attribute.
      # options: resource, span
      [scope: <string>]
```

### Filter policy config

Span filter config block

#### Filter policy

```yaml
# Exclude filters (positive matching)
[include: <policy match>]

# Exclude filters (negative matching)
[exclude: <policy match>]
```

#### Policy match

```yaml
# How to match the value of attributes
# Options: "strict", "regex"
[match_type: <string>]

# List of attributes to match
attributes: <list of policy atributes>

    # Attribute key
  - [key: <string>]

    # Attribute value
    [value: <any>]
```

#### Examples

```yaml
exclude:
  match_type: 'regex'
  attributes:
    - key: 'resource.service.name'
      value: 'unknown_service:myservice'
```

```yaml
include:
  match_type: 'strict'
  attributes:
    - key: 'foo.bar'
      value: 'baz'
```

### KVStore config

The kvstore configuration block

```yaml
# Set backing store to use
[store: <string> | default = "consul"]

# What prefix to use for keys
[prefix: <string> | default = "ring."]

# Store specific configs
consul:
  [host: <string> | default = "localhost:8500"]
  [acl_token: <secret string> | default = "" ]
  [http_client_timeout: <duration> | default = 20s]
  [consistent_reads: <bool> | default = false]
  [watch_rate_limit: <float64> | default = 1.0]
  [watch_burst_size: <int> | default = 1]
  [cas_retry_delay: <duration> | default 1s]

etcd:
  [endpoints: <list of string> | default = [] ]
  [dial_timeout: <duration> | default = 10s]
  [max_retries: <int> | default = 10 ]
  [tls_enabled: <bool> | default = false]

  # TLS config
  [tls_cert_path: <string> | default = ""]
  [tls_key_path: <string> | default = ""]
  [tls_ca_path: <string> | default = ""]
  [tls_server_name: <string> | default = ""]
  [tls_insecure_skip_verify: <bool> | default = false]
  [tls_cipher_suites: <string> | default = ""]
  [tls_min_version: <string> | default = ""]

  [username: <string> | default = ""]
  [password: <secret string> | default = ""]

multi:
  [primary: <string> | default = ""]
  [secondary: <string> | default = ""]
  [mirror_enabled: <bool> | default = false]
  [mirror_timeout: <bool> | default = 2s]
```

### Search config

```yaml
# Target number of bytes per GET request while scanning blocks. Default is 1MB. Reducing
# this value could positively impact trace search performance at the cost of more requests
# to object storage.
[chunk_size_bytes: <uint32> | default = 1000000]

# Number of traces to prefetch while scanning blocks. Default is 1000. Increasing this value
# can improve trace search performance at the cost of memory.
[prefetch_trace_count: <int> | default = 1000]

# Number of read buffers used when performing search on a vparquet block. This value times the  read_buffer_size_bytes
# is the total amount of bytes used for buffering when performing search on a parquet block.
[read_buffer_count: <int> | default = 32]

# Size of read buffers used when performing search on a vparquet block. This value times the read_buffer_count
# is the total amount of bytes used for buffering when performing search on a parquet block.
[read_buffer_size_bytes: <int> | default = 1048576]

# Granular cache control settings for parquet metadata objects
# Deprecated. See [Cache](#cache) section.
cache_control:

    # Specifies if footer should be cached
    [footer: <bool> | default = false]

    # Specifies if column index should be cached
    [column_index: <bool> | default = false]

    # Specifies if offset index should be cached
    [offset_index: <bool> | default = false]
```

### WAL config

The storage WAL configuration block.

```yaml
# Where to store the wal files while they are being appended to.
# Must be set.
# Example: "/var/tempo/wal
[path: <string> | default = ""]

# WAL encoding/compression.
# options: none, gzip, lz4-64k, lz4-256k, lz4-1M, lz4, snappy, zstd, s2
[v2_encoding: <string> | default = "zstd" ]

# Defines the search data encoding/compression protocol.
# Options: none, gzip, lz4-64k, lz4-256k, lz4-1M, lz4, snappy, zstd, s2
[search_encoding: <string> | default = "snappy"]

# When a span is written to the WAL it adjusts the start and end times of the block it is written to.
# This block start and end time range is then used when choosing blocks for search.
# This is also used for querying traces by ID when the start and end parameters are specified. To prevent spans too far
# in the past or future from impacting the block start and end times we use this configuration option.
# This option only allows spans that occur within the configured duration to adjust the block start and
# end times.
# This can result in trace not being found if the trace falls outside the slack configuration value as the
# start and end times of the block will not be updated in this case.
[ingestion_time_range_slack: <duration> | default = unset]

# WAL file format version
# Options: v2, vParquet, vParquet2, vParquet3
[version: <string> | default = "vParquet3"]
```

## Overrides

Tempo provides an overrides module for users to set global or per-tenant override settings.

### Ingestion limits

The default limits in Tempo may not be sufficient in high-volume tracing environments.
Errors including `RATE_LIMITED`/`TRACE_TOO_LARGE`/`LIVE_TRACES_EXCEEDED` occur when these limits are exceeded.
See below for how to override these limits globally or per tenant.

#### Standard overrides

You can create an `overrides` section to configure ingestion limits that apply to all tenants of the cluster.
A snippet of a `config.yaml` file showing how the overrides section is [here](https://github.com/grafana/tempo/blob/a000a0d461221f439f585e7ed55575e7f51a0acd/integration/bench/config.yaml#L39-L40).

```yaml
# Overrides configuration block
overrides:

  # Global ingestion limits configurations
  defaults:

    # Ingestion related overrides
    ingestion:

      # Specifies whether the ingestion rate limits should be applied by each instance
      # of the distributor and ingester individually, or the limits are to be shared
      # across all instances. See the "override strategies" section for an example.
      [rate_strategy: <global|local> | default = local]

      # Burst size (bytes) used in ingestion.
      # Results in errors like
      #   RATE_LIMITED: ingestion rate limit (20000000 bytes) exceeded while
      #   adding 10 bytes
      [burst_size_bytes: <int> | default = 20000000 (20MB) ]

      # Per-user ingestion rate limit (bytes) used in ingestion.
      # Results in errors like
      #   RATE_LIMITED: ingestion rate limit (15000000 bytes) exceeded while
      #   adding 10 bytes
      [rate_limit_bytes: <int> | default = 15000000 (15MB) ]

      # Maximum number of active traces per user, per ingester.
      # A value of 0 disables the check.
      # Results in errors like
      #    LIVE_TRACES_EXCEEDED: max live traces per tenant exceeded:
      #    per-user traces limit (local: 10000 global: 0 actual local: 1) exceeded
      # This override limit is used by the ingester.
      [max_traces_per_user: <int> | default = 10000]

      # Maximum number of active traces per user, across the cluster.
      # A value of 0 disables the check.
      [max_global_traces_per_user: <int> | default = 0]

      # Shuffle sharding shards used for this user. A value of 0 uses all ingesters in the ring.
      # Should not be lower than RF.
      [tenant_shard_size: <int> | default = 0]

      # Maximum bytes any attribute can be for both keys and values.
      [max_attribute_bytes: <int> | default = 0]

      # Pad push requests with an artificial delay, if set push requests will be delayed to ensure
      # an average latency of at least artificial_delay.
      [artificial_delay: <duration> | default = 0ms]

    # Read related overrides
    read:
      # Maximum size in bytes of a tag-values query. Tag-values query is used mainly
      # to populate the autocomplete dropdown. This limit protects the system from
      # tags with high cardinality or large values such as HTTP URLs or SQL queries.
      # This override limit is used by the ingester and the querier.
      # A value of 0 disables the limit.
      [max_bytes_per_tag_values_query: <int> | default = 1000000 (1MB) ]

      # Maximum number of blocks to be inspected for a tag values query. Tag-values
      # query is used mainly to populate the autocomplete dropdown. This limit
      # protects the system from long block lists in the ingesters.
      # This override limit is used by the ingester and the querier.
      # A value of 0 disables the limit.
      [max_blocks_per_tag_values_query: <int> | default = 0 (disabled) ]

      # Per-user max search duration. If this value is set to 0 (default), then max_duration
      #  in the front-end configuration is used.
      [max_search_duration: <duration> | default = 0s]

      # Per-user max duration for metrics queries. If this value is set to 0 (default), then metrics max_duration
      #  in the front-end configuration is used.
      [max_metrics_duration: <duration> | default = 0s]

    # Compaction related overrides
    compaction:
      # Per-user block retention. If this value is set to 0 (default),
      # then block_retention in the compactor configuration is used.
      [block_retention: <duration> | default = 0s]
      # Per-user compaction window. If this value is set to 0 (default),
      # then block_retention in the compactor configuration is used.
      [compaction_window: <duration> | default = 0s]
      # Allow compaction and retention to be deactivated on a per-tenant basis. Default value
      # is false (compaction active). Useful to perform operations on the backend
      # that require compaction to be disabled for a period of time.
      [compaction_disabled: <bool> | default = false]

    # Metrics-generator related overrides
    metrics_generator:

      # Per-user configuration of the metrics-generator ring size. If set, the tenant will use a
      # ring with at most the given amount of instances. Shuffle sharding is used to spread out
      # smaller rings across all instances. If the value 0 or a value larger than the total amount
      # of instances is used, all instances will be included in the ring.
      #
      # Together with metrics_generator.max_active_series this can be used to control the total
      # amount of active series. The total max active series for a specific tenant will be:
      #   metrics_generator.ring_size * metrics_generator.max_active_series
      [ring_size: <int>]

      # Per-user configuration of the metrics-generator processors. The following processors are
      # supported:
      #  - service-graphs
      #  - span-metrics
      #  - local-blocks
      [processors: <list of strings>]

      # Maximum number of active series in the registry, per instance of the metrics-generator. A
      # value of 0 disables this check.
      # If the limit is reached, no new series will be added but existing series will still be
      # updated. The amount of limited series can be observed with the metric
      #   tempo_metrics_generator_registry_series_limited_total
      [max_active_series: <int>]

      # Per-user configuration of the collection interval. A value of 0 means the global default is
      # used set in the metrics_generator config block.
      [collection_interval: <duration>]

      # Per-user flag of the registry collection operation. If set, the registry will not be
      # collected and no samples will be exported from the metrics-generator. The metrics-generator
      # will still ingest spans and update its internal counters, including the amount of active
      # series. To disable metrics generation entirely, clear metrics_generator.processors for this
      # tenant.
      #
      # This setting is useful if you wish to test how many active series a tenant will generate, without
      # actually writing these metrics.
      [disable_collection: <bool> | default = false]

      # Per-user configuration of the trace-id label name. This value will be used as name for the label to store the
      # trace ID of exemplars in generated metrics. If not set, the default value "trace_id" will be used.
      [trace_id_label_name: <string> | default = "trace_id"]

      # This option only allows spans with end time that occur within the configured duration to be
      # considered in metrics generation.
      # This is to filter out spans that are outdated.
      [ingestion_time_range_slack: <duration>]

      # Configures the histogram implementation to use for span metrics and
      # service graphs processors.  If native histograms are desired, the
      # receiver must be configured to ingest native histograms.
      [generate_native_histograms: <classic|native|both> | default = classic]

      # Distributor -> metrics-generator forwarder related overrides
      forwarder:
        # Spans are stored in a queue in the distributor before being sent to the metrics-generators.
        # The length of the queue and the amount of workers pulling from the queue can be configured.
        [queue_size: <int> | default = 100]
        [workers: <int> | default = 2]

      # Per processor configuration
      processor:

        # Configuration for the service-graphs processor
        service_graphs:
          [histogram_buckets: <list of float>]
          [dimensions: <list of string>]
          [peer_attributes: <list of string>]
          [enable_client_server_prefix: <bool>]
          [enable_messaging_system_latency_histogram: <bool>]

        # Configuration for the span-metrics processor
        span_metrics:
          [histogram_buckets: <list of float>]
          # Allowed keys for intrinsic dimensions are: service, span_name, span_kind, status_code, and status_message.
          [dimensions: <list of string>]
          [intrinsic_dimensions: <map string to bool>]
          [filter_policies: [
            [
              include/exclude:
                match_type: <string> # options: strict, regexp
                attributes:
                  - key: <string>
                    value: <any>
            ]
          ]
          [dimension_mappings: <list of map>]
          # Enable target_info metrics
          [enable_target_info: <bool>]
          # Drop specific resource labels from traces_target_info
          [target_info_excluded_dimensions: <list of string>]

        # Configuration for the local-blocks processor
        local-blocks:
          [max_live_traces: <int>]
          [max_block_duration: <duration>]
          [max_block_bytes: <int>]
          [flush_check_period: <duration>]
          [trace_idle_period: <duration>]
          [complete_block_timeout: <duration>]
          [concurrent_blocks: <duration>]
          [filter_server_spans: <bool>]

    # Generic forwarding configuration

    # Per-user configuration of generic forwarder feature. Each forwarder in the list
    # must refer by name to a forwarder defined in the distributor.forwarders configuration.
    forwarders: <list of string>

    # Global enforced overrides
    global:
      # Maximum size of a single trace in bytes. A value of 0 disables the size
      # check.
      # This limit is used in 3 places:
      #  - During search, traces will be skipped when they exceed this threshold.
      #  - During ingestion, traces that exceed this threshold will be refused.
      #  - During compaction, traces that exceed this threshold will be partially dropped.
      # During ingestion, exceeding the threshold results in errors like
      #    TRACE_TOO_LARGE: max size of trace (5000000) exceeded while adding 387 bytes
      [max_bytes_per_trace: <int> | default = 5000000 (5MB) ]

    # Storage enforced overrides
    storage:
      # Configures attributes to be stored in dedicated columns within the parquet file, rather than in the
      # generic attribute key-value list. This allows for more efficient searching of these attributes.
      # Up to 10 span attributes and 10 resource attributes can be configured as dedicated columns.
      # Requires vParquet3
      parquet_dedicated_columns:
        [
          name: <string>, # name of the attribute
          type: <string>, # type of the attribute. options: string
          scope: <string> # scope of the attribute. options: resource, span
        ]

    # Cost attribution usage tracker configuration
    cost_attribution:
      # List of attributes to group ingested data by.  Map value is optional. Can be used to rename and
      # combine attributes.
      dimensions: <map string to string>


  # Tenant-specific overrides settings configuration file. The empty string (default
  # value) disables using an overrides file.
  [per_tenant_override_config: <string> | default = ""]

  # How frequent tenant-specific overrides are read from the configuration file.
  [per_tenant_override_period: <duration> | default = 10s]

  # User-configurable overrides configuration
  user_configurable_overrides:

    # Enable the user-configurable overrides module
    [enabled: <bool> | default = false]

    # How often to poll the backend for new user-configurable overrides
    [poll_interval: <duration> | default = 60s]

    client:
      # The storage backend to use
      # Should be one of "gcs", "s3", "azure" or "local"
      [backend: <string>]

      # Backend-specific configuration, support the same configuration options as the
      # trace backend configuration
      local:
      gcs:
      s3:
      azure:

      # Check whether the backend supports versioning at startup. If enabled Tempo will not start if
      # the backend doesn't support versioning.
      [confirm_versioning: <bool> | default = true]

    api:
      # When enabled, Tempo will refuse request that modify overrides that are already set in the
      # runtime overrides. For more details, see user-configurable overrides docs.
      [check_for_conflicting_runtime_overrides: <bool> | default = false]
```

#### Tenant-specific overrides

There are two types of tenant-specific overrides:

- runtime overrides
- user-configurable overrides

##### Runtime overrides

You can set tenant-specific overrides settings in a separate file and point `per_tenant_override_config` to it.
This overrides file is dynamically loaded.
It can be changed at runtime and reloaded by Tempo without restarting the application.
These override settings can be set per tenant.

```yaml
# /conf/tempo.yaml
# Overrides configuration block
overrides:
   per_tenant_override_config: /conf/overrides.yaml

---
# /conf/overrides.yaml
# Tenant-specific overrides configuration
overrides:

  "<tenant-id>":
      ingestion:
        [burst_size_bytes: <int>]
        [rate_limit_bytes: <int>]
        [max_traces_per_user: <int>]
      global:
        [max_bytes_per_trace: <int>]

  # A "wildcard" override can be used that will apply to all tenants if a match is not found otherwise.
  "*":
    ingestion:
      [burst_size_bytes: <int>]
      [rate_limit_bytes: <int>]
      [max_traces_per_user: <int>]
    global:
      [max_bytes_per_trace: <int>]
```

##### User-configurable overrides

These tenant-specific overrides are stored in an object store and can be modified using API requests.
User-configurable overrides have priority over runtime overrides.
Refer to [user-configurable overrides](https://grafana.com/docs/tempo/<TEMPO_VERSION>/operations/manage-advanced-systems/user-configurable-overrides/) for more details.

#### Override strategies

The trace limits specified by the various parameters are, by default, applied as per-distributor limits.
For example, a `max_traces_per_user` setting of 10000 means that each distributor within the cluster has a limit of 10000 traces per user.
This is known as a `local` strategy in that the specified trace limits are local to each distributor.

A setting that applies at a local level is quite helpful in ensuring that each distributor independently can process traces up to the limit without affecting the tracing limits on other distributors.

However, as a cluster grows quite large, this can lead to quite a large quantity of traces.
An alternative strategy may be to set a `global` trace limit that establishes a total budget of all traces across all distributors in the cluster.
The global limit is averaged across all distributors by using the distributor ring.

```yaml
# /conf/tempo.yaml
overrides:
  defaults:
    ingestion: [rate_strategy: <global|local> | default = local]
```

For example, this configuration specifies that each instance of the distributor will apply a limit of `15MB/s`.

```yaml
overrides:
  defaults:
    ingestion:
      strategy: local
      limit_bytes: 15000000
```

This configuration specifies that together, all distributor instances will apply a limit of `15MB/s`.
So if there are 5 instances, each instance will apply a local limit of `(15MB/s / 5) = 3MB/s`.

```yaml
overrides:
  defaults:
    ingestion:
      strategy: global
      limit_bytes: 15000000
```

## Usage-report

By default, Tempo reports anonymous usage data about the shape of a deployment to Grafana Labs.
This data is used to determine how common the deployment of certain features are, if a feature flag has been enabled,
and which replication factor or compression levels are used.

By providing information on how people use Tempo, usage reporting helps the Tempo team decide where to focus their development and documentation efforts. No private information is collected, and all reports are completely anonymous.

The following configuration values are used:

- Receivers enabled
- Frontend concurrency and version
- Storage cache, backend, WAL and block encodings
- Ring replication factor, and `kvstore`
- Features toggles enabled

No performance data is collected.

You can view the report by visiting this address on your Tempo instance:
`http://localhost:3200/status/usage-stats`

Refer to [Anonymous usage reporting](../configuration/anonymous-usage-reporting/) for detailed information on the information included in the report.

### Configure usage-reporting

Reporting is controlled by a configuration option.
You can disable the automatic reporting of this generic information using the following
configuration:

```yaml
usage_report:
  reporting_enabled: false
```

If you are using a Helm chart, you can enable or disable usage reporting by changing the `reportingEnabled` value.
This value is available in the [tempo-distributed](https://github.com/grafana/helm-charts/tree/main/charts/tempo-distributed) and the [tempo](https://github.com/grafana/helm-charts/tree/main/charts/tempo) Helm charts.

```yaml
# -- If true, Tempo will report anonymous usage data about the shape of a deployment to Grafana Labs
reportingEnabled: true
```

## Cache

Use this block to configure caches available throughout the application. Multiple caches can be created and assigned roles
which determine how they are used by Tempo.

```yaml
cache:
    # Background cache configuration. Requires having a cache configured. These settings apply
    # to all configured caches.
    background:

        # At what concurrency to write back to cache. Default is 10.
        [writeback_goroutines: <int>]

        # How many key batches to buffer for background write-back. Default is 10000.
        [writeback_buffer: <int>]

    caches:

        # Roles determine how this cache is used in Tempo. Roles must be unique across all caches and
        # every cache must have at least one role.
        # Allowed values:
        #   bloom              - Bloom filters for trace id lookup.
        #   parquet-footer     - Parquet footer values. Useful for search and trace by id lookup.
        #   parquet-page       - Parquet "pages". WARNING: This will attempt to cache most reads from parquet and, as a result, is very high volume.
        #   frontend-search    - Frontend search job results.

    -   roles:
        - <role1>
        - <role2>

        # Memcached caching configuration block
        memcached:

            # Hostname for memcached service to use. If empty and if addresses is unset, no memcached will be used.
            # Example: "host: memcached"
            [host: <string>]

            # Optional
            # SRV service used to discover memcache servers. (default: memcached)
            # Example: "service: memcached-client"
            [service: <string>]

            # Optional
            # Comma separated addresses list in DNS Service Discovery format. Refer - https://cortexmetrics.io/docs/configuration/arguments/#dns-service-discovery.
            # (default: "")
            # Example: "addresses: dns+memcached:11211"
            [addresses: <comma separated strings>]

            # Optional
            # Maximum time to wait before giving up on memcached requests.
            # (default: 100ms)
            [timeout: <duration>]

            # Optional
            # Maximum number of idle connections in pool.
            # (default: 16)
            [max_idle_conns: <int>]

            # Optional
            # Period with which to poll DNS for memcache servers.
            # (default: 1m)
            [update_interval: <duration>]

            # Optional
            # Use consistent hashing to distribute keys to memcache servers.
            # (default: true)
            [consistent_hash: <bool>]

            # Optional
            # Trip circuit-breaker after this number of consecutive dial failures.
            # (default: 10)
            [circuit_breaker_consecutive_failures: 10]

            # Optional
            # Duration circuit-breaker remains open after tripping.
            # (default: 10s)
            [circuit_breaker_timeout: 10s]

            # Optional
            # Reset circuit-breaker counts after this long.
            # (default: 10s)
            [circuit_breaker_interval: 10s]

            # Enable connecting to Memcached with TLS.
            # CLI flag: -<prefix>.memcached.tls-enabled
            [tls_enabled: <boolean> | default = false]

            # Path to the client certificate, which will be used for authenticating with
            # the server. Also requires the key path to be configured.
            # CLI flag: -<prefix>.memcached.tls-cert-path
            [tls_cert_path: <string> | default = ""]

            # Path to the key for the client certificate. Also requires the client
            # certificate to be configured.
            # CLI flag: -<prefix>.memcached.tls-key-path
            [tls_key_path: <string> | default = ""]

            # Path to the CA certificates to validate server certificate against. If not
            # set, the host's root CA certificates are used.
            # CLI flag: -<prefix>.memcached.tls-ca-path
            [tls_ca_path: <string> | default = ""]

            # Override the expected name on the server certificate.
            # CLI flag: -<prefix>.memcached.tls-server-name
            [tls_server_name: <string> | default = ""]

            # Skip validating server certificate.
            # CLI flag: -<prefix>.memcached.tls-insecure-skip-verify
            [tls_insecure_skip_verify: <boolean> | default = false]

            # Override the default cipher suite list (separated by commas). Allowed
            # values:
            #
            # Secure Ciphers:
            # - TLS_RSA_WITH_AES_128_CBC_SHA
            # - TLS_RSA_WITH_AES_256_CBC_SHA
            # - TLS_RSA_WITH_AES_128_GCM_SHA256
            # - TLS_RSA_WITH_AES_256_GCM_SHA384
            # - TLS_AES_128_GCM_SHA256
            # - TLS_AES_256_GCM_SHA384
            # - TLS_CHACHA20_POLY1305_SHA256
            # - TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA
            # - TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA
            # - TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA
            # - TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA
            # - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
            # - TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
            # - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
            # - TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
            # - TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
            # - TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
            #
            # Insecure Ciphers:
            # - TLS_RSA_WITH_RC4_128_SHA
            # - TLS_RSA_WITH_3DES_EDE_CBC_SHA
            # - TLS_RSA_WITH_AES_128_CBC_SHA256
            # - TLS_ECDHE_ECDSA_WITH_RC4_128_SHA
            # - TLS_ECDHE_RSA_WITH_RC4_128_SHA
            # - TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA
            # - TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256
            # - TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256
            # CLI flag: -<prefix>.memcached.tls-cipher-suites
            [tls_cipher_suites: <string> | default = ""]

            # Override the default minimum TLS version. Allowed values: VersionTLS10,
            # VersionTLS11, VersionTLS12, VersionTLS13
            # CLI flag: -<prefix>.memcached.tls-min-version
            [tls_min_version: <string> | default = ""]

        # Redis configuration block
        # EXPERIMENTAL
        redis:

            # Redis endpoint to use when caching.
            [endpoint: <string>]

            # optional.
            # Maximum time to wait before giving up on redis requests. (default 100ms)
            [timeout: 500ms]

            # optional.
            # Redis Sentinel master name. (default "")
            # Example: "master-name: redis-master"
            [master-name: <string>]

            # optional.
            # Database index. (default 0)
            [db: <int>]

            # optional.
            # How long keys stay in the redis. (default 0)
            [expiration: <duration>]

            # optional.
            # Enable connecting to redis with TLS. (default false)
            [tls-enabled: <bool>]

            # optional.
            # Skip validating server certificate. (default false)
            [tls-insecure-skip-verify: <bool>]

            # optional.
            # Maximum number of connections in the pool. (default 0)
            [pool-size: <int>]

            # optional.
            # Password to use when connecting to redis. (default "")
            [password: <string>]

            # optional.
            # Close connections after remaining idle for this duration. (default 0s)
            [idle-timeout: <duration>]

            # optional.
            # Close connections older than this duration. (default 0s)
            [max-connection-age: <duration>]

            # optional.
            # Password to use when connecting to redis sentinel. (default "")
            [sentinel_password: <string>]
```

Example configuration:

```yaml
cache:
  background:
    writeback_goroutines: 5
  caches:
    - roles:
        - parquet-footer
      memcached:
        host: memcached-instance
    - roles:
        - bloom
      redis:
        endpoint: redis-instance
```
