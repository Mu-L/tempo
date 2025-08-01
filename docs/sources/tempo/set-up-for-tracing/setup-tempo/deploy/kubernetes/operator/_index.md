---
title: Deploy Tempo with Tempo Operator
menuTitle: Deploy with operator
description: Learn how to deploy Tempo with Tempo Operator
weight: 375
aliases:
  - ../../../../operator/operator/ # /docs/tempo/next/operator/operator/
  - ../../../../setup/operator/ # /docs/tempo/next/setup/operator/
---

# Deploy Tempo with Tempo Operator

The Tempo Operator allows you to configure, install, upgrade, and operate Grafana Tempo on Kubernetes and OpenShift clusters.

Some of the operator features are:

- **Resource Limits** - Specify overall resource requests and limits in the `TempoStack` CR; the operator assigns fractions of it to each component
- **AuthN and AuthZ** - Supports OpenID Control (OIDC) and role-based access control (RBAC)
- **Managed upgrades** - Updating the operator will automatically update all managed Tempo clusters
- **Multitenancy** - Multiple tenants can send traces to the same Tempo cluster
- **mTLS** - Communication between the Tempo components can be secured via mTLS
- **Jaeger UI** - Traces can be visualized in Jaeger UI and exposed via Ingress or OpenShift Route
- **Observability** - The operator and `TempoStack` operands expose telemetry (metrics, traces) and integrate with Prometheus `ServiceMonitor` and `PrometheusRule`

The source of the Tempo Operator can be found at [grafana/tempo-operator](https://github.com/grafana/tempo-operator).

## Installation

The operator can be installed from:

- [Kubernetes manifest](https://github.com/grafana/tempo-operator/releases/latest/download/tempo-operator.yaml) file on a Kubernetes cluster
- [operatorhub.io](https://operatorhub.io/operator/tempo-operator) on a Kubernetes cluster
- OperatorHub on an OpenShift cluster

## Compatibility

### Tempo

The supported Tempo version by the operator can be found in the [changelog](https://github.com/grafana/tempo-operator/blob/main/CHANGELOG.md) or on the [release page](https://github.com/grafana/tempo-operator/releases).

### Kubernetes

The supported Kubernetes versions can be found in the [changelog](https://github.com/grafana/tempo-operator/blob/main/CHANGELOG.md) or on the [release page](https://github.com/grafana/tempo-operator/releases).

### cert-manager

The operator Kubernetes manifest installation files use cert-manger `v1` custom resources to provision certificates for admission webhooks.

## Community

- Reach out to us on [#tempo-operator](https://grafana.slack.com/archives/C0414EUU39A) Grafana Slack channel.
- Participate on [Tempo community call](https://grafana.com/docs/tempo/<TEMPO_VERSION>/community/).
