---
title: TraceQL metrics functions
menuTitle: TraceQL metrics functions
description: Learn about functions used in TraceQL metrics queries
aliases:
  - ../../traceql/metrics-queries/functions/ # /docs/tempo/next/traceql/metrics-queries/functions/
weight: 600
keywords:
  - metrics query
  - TraceQL metrics
---

# TraceQL metrics functions

<!-- Using a custom admonition because no feature flag is required. -->

{{< docs/shared source="tempo" lookup="traceql-metrics-admonition.md" version="<TEMPO_VERSION>" >}}

<!-- If you add a new function to this page, make sure you also add it to the _index.md#functions section.-->

[TraceQL](http://grafana.com/docs/tempo/<TEMPO_VERSION>/traceql/) supports `rate`, `count_over_time`, `sum_over_time`, `min_over_time`, `avg_over_time`, `quantile_over_time`,
`histogram_over_time`, and `compare` functions. These methods can be appended to any TraceQL query to calculate and
return the desired metrics like:

```
{} | rate()
```

Note that `topk` and `bottomk` are also supported to only return a subset of series. These can only be added
after a metrics query like:

```
{} | rate() by (resource.service.name) | topk(10)
```

Read on for a full listing of functions and examples.

## Available functions

These functions can be added as an operator at the end of any TraceQL query.

`rate`
: Calculates the number of matching spans per second.

`count_over_time`
: Counts the number of matching spans per time interval (refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs)).

`sum_over_time`
: Sums the value for the specified attribute across all matching spans per time interval (refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs)).

`min_over_time`
: Returns the minimum value for the specified attribute across all matching spans per time interval (refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#traceql-metrics)).

`max_over_time`
: Returns the maximum value for the specified attribute across all matching spans per time interval (refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#traceql-metrics)).

`avg_over_time`
: Returns the average value for the specified attribute across all matching spans per time interval (refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#traceql-metrics)).

`quantile_over_time`
: The quantile of the values in the specified interval.

`histogram_over_time`
: Evaluate frequency distribution over time. Example: `histogram_over_time(duration) by (span.foo)`.

`compare`
: Used to split the stream of spans into two groups: a selection and a baseline. The function returns time-series for
all attributes found on the spans to highlight the differences between the two groups.

## The `rate` function

The `rate` function calculates the number of matching spans per second that match the given span selectors.

### Parameters

None.

### Examples

The following query shows the rate of errors by service and span name.
This is a TraceQL specific way of gathering rate metrics that would otherwise be generated by the span metrics
processor.

For example, this query:

```
{ status = error } | rate() by (resource.service.name, name)
```

Is an equivalent to using span-generated metrics and running the query.

This example calculates the rate of the erroring spans coming from the service `foo`.
Rate is a `spans/sec` quantity.

```
{ resource.service.name = "foo" && status = error } | rate()
```

Combined with the `by()` operator, this can be even more powerful.

```
{ resource.service.name = "foo" && status = error } | rate() by (span.http.route)
```

This example still rates the erroring spans in the service `foo` but the metrics are broken
down by HTTP route.
This might let you determine that `/api/sad` had a higher rate of erroring
spans than `/api/happy`, for example.

## The `count_over_time` function

The `count_over_time()` function counts the number of matching spans per time interval.
The time interval that the count is computed over is set by the `step` parameter.

### The `step` parameter

Ths `step` parameter defines the granularity of the returned time-series.
For example, `step=15s` returns a data point every 15s within the time range.
By default, `step` automatically chooses a dynamic value based on the query start time and end time.

Any value used for `step` needs to include a duration value, such as `30s` for `s`econds or `1m` for `m`inutes.

You can configure this parameter using Grafana Explore or using the Tempo API.
Refer to the [`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#traceql-metrics) for information about using the API.

To check or change the `step` value using Grafana Explore:

1. Select your Tempo data source.
1. Select either the **Search** or **TraceQL** query type tab.
1. Expand the **Metrics options** to view the **Step** value.

![The Step value in the Metrics options in Grafana Explore](/media/docs/tempo/tempo-ds-query-metrics-options-step.png)

### Example

This example counts the number of spans with name `"GET /:endpoint"` broken down by status code. You might see that
there are 10 `"GET /:endpoint"` spans with status code 200 and 15 `"GET /:endpoint"` spans with status code 400.

```
{ name = "GET /:endpoint" } | count_over_time() by (span.http.status_code)

```

## The `sum_over_time`, `min_over_time`, `max_over_time`, and `avg_over_time` functions

The `sum_over_time()` lets you aggregate numerical values by computing the sum value of them.
The time interval that the sum is computed over is set by the `step` parameter.

The `min_over_time()` function lets you aggregate numerical attributes by calculating their minimum value.
For example, you could choose to calculate the minimum duration of a group of spans, or you could choose to calculate
the minimum value of a custom attribute you've attached to your spans, like `span.shopping.cart.entries`.
The time interval that the minimum is computed over is set by the `step` parameter.

The `max_over_time()` lets you aggregate numerical values by computing the maximum value of them, such as the all
important span duration.
The time interval that the maximum is computed over is set by the `step` parameter.

The `avg_over_time()` function lets you aggregate numerical values by computing the maximum value of them, such as the
all important span duration.
The time interval that the maximum is computer over is set by the `step` parameter.

For more information, refer to the [
`step` API parameter](https://grafana.com/docs/tempo/<TEMPO_VERSION>/api_docs/#traceql-metrics).

### Parameters

Numerical field that you want to calculate the minimum, maximum, or average of.

### Examples

This example computes the minimum duration for each `http.target` of all spans named `"GET /:endpoint"`.
Any numerical attribute on the span is fair game.

```
{ name = "GET /:endpoint" } | min_over_time(duration) by (span.http.target)
```

This example computes the minimum status code value of all spans named `"GET /:endpoint"`.

```
{ name = "GET /:endpoint" } | min_over_time(span.http.status_code)
```

This example computes the maximum duration for each `http.target` of all spans named `"GET /:endpoint"`.

```
{ name = "GET /:endpoint" } | max_over_time(duration) by (span.http.target)
```

```
{ name = "GET /:endpoint" } | max_over_time(span.http.response.size)
```

This example computes the average duration for each `http.status_code` of all spans named `"GET /:endpoint"`.

```
{ name = "GET /:endpoint" } | avg_over_time(duration) by (span.http.status_code)
```

```
{ name = "GET /:endpoint" } | avg_over_time(span.http.response.size)
```

## The `quantile_over_time` and `histogram_over_time` functions

The `quantile_over_time()` and `histogram_over_time()` functions let you aggregate numerical values, such as the all
important span duration.
You can specify multiple quantiles in the same query.

The example below computes the 99th, 90th, and 50th percentile of the duration attribute on all spans with name
`GET /:endpoint`.

```
{ name = "GET /:endpoint" } | quantile_over_time(duration, .99, .9, .5)
```

You can group by any span or resource attribute.

```
{ name = "GET /:endpoint" } | quantile_over_time(duration, .99) by (span.http.target)
```

Quantiles aren't limited to span duration.
Any numerical attribute on the span is fair game.
To demonstrate this flexibility, consider this nonsensical quantile on `span.http.status_code`:

```
{ name = "GET /:endpoint" } | quantile_over_time(span.http.status_code, .99, .9, .5)
```

This computes the 99th, 90th, and 50th percentile of the values of the `status_code` attribute for all spans named
`GET /:endpoint`.
This is unlikely to tell you anything useful (what does a median status code of `347` mean?), but it works.

As a further example, imagine a custom attribute like `span.temperature`.
You could use a similar query to know what the 50th percentile and 95th percentile temperatures were across all your
spans.

## `topk` and `bottomk` functions

TraceQL supports the `topk` and `bottomk` functions that let you aggregate and process TraceQL metrics.
These functions are similar to their equivalent PromQL functions. For example:

- `{ } | rate() by(resource.service.name) | bottomk(5)`
- `{ } | rate() by(resource.service.name) | topk(5)`

When a query response is larger than the maximum, you can use these functions to return only the specified number
from 1 through `k` of the number of the top or bottom results.

For example:

```
{ resource.service.name = "foo" } | rate() by (span.http.url)  | topk(10)
```

The first part, `{ resource.service.name = "foo" }`, takes all spans in the service `foo`.
The spans are rated by the URL, for example, the most active endpoints on a service.

Adding `topk(10)` returns the top 10 most common instead of the entire list.
Conversely, you can use `bottomk(10)` to see the least most used ones.

In TraceQL, `topk` and `bottomk` work similar to how they function in PromQL. Both `topk` and `bottomk`
are evaluated at each data point.

If you do a `topk` of 10, you might get a 20 series. For example, on this data point, the top 10 are `A` through `J`.

On the next data point, `A` through `I` might still be the top 9, but `J` might have fallen off for `K`.
Because it's evaluated at each data point, you'll get the top series for each data point.

## The `compare` function

The `compare` function is used to split a set of spans into two groups: a selection and a baseline.
It returns time-series for all attributes found on the spans to highlight the differences between the two groups.

This powerful function is best understood by using the [**Comparison** tab in Traces Drilldown](https://grafana.com/docs/grafana/<GRAFANA_VERSION>/explore/simplified-exploration/traces/investigate/analyze-tracing-data/#use-the-comparison-tab).
You can also under this function by looking at example outputs below.

The function is used like other metrics functions: when it's placed after any trace query, it converts the query into a
metrics query:
`...any spanset pipeline... | compare({subset filters}, <topN>, <start timestamp>, <end timestamp>)`

Example:

```
{ resource.service.name="a" && span.http.path="/myapi" } | compare({status=error})
```

This function is generally run as an instant query.
An instant query gives a single value at the end of the selected time range.
[Instant queries](https://prometheus.io/docs/prometheus/latest/querying/api/#instant-queries) are quicker to execute and
it often easier to understand their results
The returns may exceed gRPC payloads when run as a range query.

### Parameters

The `compare` function has four parameters:

1. Required. The first parameter is a spanset filter for choosing the subset of spans. This filter is executed against
   the incoming spans. If it matches, then the span is considered to be part of the selection. Otherwise, it is part of
   the baseline. Common filters are expected to be things like `{status=error}` (what is different about errors?) or
   `{duration>1s}` (what is different about slow spans?)

2. Optional. The second parameter is the top `N` values to return per attribute. If an attribute exceeds this limit in
   either the selection group or baseline group, then only the top `N` values (based on frequency) are returned, and an
   error indicator for the attribute is included output (see below). Defaults to `10`.

3. Optional. Start and End timestamps in Unix nanoseconds, which can be used to constrain the selection window by time,
   in addition to the filter. For example, the overall query could cover the past hour, and the selection window only a
   5 minute time period in which there was an anomaly. These timestamps must both be given, or neither.

### Output

The outputs are flat time-series for each attribute/value found in the spans.

Each series has a label `__meta_type` which denotes which group it is in, either `selection` or `baseline`.

Example output series:

```
{ __meta_type="baseline", resource.cluster="prod" } 123
{ __meta_type="baseline", resource.cluster="qa" } 124
{ __meta_type="selection", resource.cluster="prod" } 456   <--- significant difference detected
{ __meta_type="selection", resource.cluster="qa" } 125
{ __meta_type="selection", resource.cluster="dev"} 126  <--- cluster=dev was found in the highlighted spans but not in the baseline
```

When an attribute reaches the topN limit, there will also be present an error indicator.
This example means the attribute `resource.cluster` had too many values.

```
{ __meta_error="__too_many_values__", resource.cluster=<nil> }
```
