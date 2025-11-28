# ReportPortal MCP Server Helm Chart

A Helm chart for deploying the ReportPortal MCP (Model Context Protocol) Server to Kubernetes.

## Description

This Helm chart deploys the ReportPortal MCP Server, which provides an HTTP interface for interacting with ReportPortal instances.

## Prerequisites

- Kubernetes 1.30+
- Helm 3.0+

## Installation

### Basic Installation

```bash
helm install reportportal-mcp-server ./helm-charts/reportportal-mcp-server
```

### Installation with Custom Values

```bash
helm install reportportal-mcp-server ./helm-charts/reportportal-mcp-server -f custom-values.yaml
```

### Installation with Overrides

```bash
helm install reportportal-mcp-server ./helm-charts/reportportal-mcp-server \
  --set rpHost="https://your-reportportal-instance.com" \
  --set ingress.hosts[0].host="mcp.example.com" \
  --set ingress.hosts[0].paths[0].path="/mcp"
```

## Configuration

The following table lists the configurable parameters and their default values:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Container image repository | `reportportal/mcp-server` |
| `image.tag` | Container image tag | `latest` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `service.type` | Service type | `ClusterIP` |
| `service.port` | Service port | `8080` |
| `service.targetPort` | Container port | `8080` |
| `ingress.enabled` | Enable ingress | `true` |
| `ingress.className` | Ingress class name | `nginx` |
| `ingress.hosts` | Ingress host configuration | See values.yaml |
| `autoscaling.enabled` | Enable HPA | `false` |
| `autoscaling.minReplicas` | Minimum replicas | `1` |
| `autoscaling.maxReplicas` | Maximum replicas | `100` |
| `autoscaling.targetCPUUtilizationPercentage` | Target CPU utilization | `80` |
| `extraEnv` | Extra environment variables | `[]` |
| `initContainers` | Init containers | `[]` |
| `sidecarContainers` | Sidecar containers | `[]` |

## Environment Variables

### MCP Environment Variables

The following table lists the MCP-specific environment variables and their corresponding values.yaml parameters:

| Parameter | Environment Variable | Description | Default |
|-----------|---------------------|-------------|---------|
| `mcpMode` | `MCP_MODE` | MCP operation mode | `http` |
| `rpHost` | `RP_HOST` | ReportPortal instance URL | `https://your-reportportal-instance.com` |
| `rpMcpAnalyticsOff` | `RP_MCP_ANALYTICS_OFF` | Disable analytics tracking | `true` |

## Ingress Configuration

The ingress is configured with prefix path support. You can customize the path prefix in the values file:

```yaml
ingress:
  enabled: true
  hosts:
    - host: reportportal.example.com
      paths:
        - path: /mcp
          pathType: Prefix

mcpMode: "http"
rpHost: "https://reportportal.example.com"
rpMcpAnalyticsOff: "true"
```

## Upgrading

```bash
helm upgrade reportportal-mcp-server ./helm-charts/reportportal-mcp-server
```

## Uninstallation

```bash
helm uninstall reportportal-mcp-server
```

## Troubleshooting

### Check Pod Status

```bash
kubectl get pods -l app.kubernetes.io/name=reportportal-mcp-server
```

### View Pod Logs

```bash
kubectl logs -l app.kubernetes.io/name=reportportal-mcp-server
```

### Check Ingress

```bash
kubectl get ingress reportportal-mcp-server
kubectl describe ingress reportportal-mcp-server
```

### Verify Environment Variables

```bash
kubectl get deployment reportportal-mcp-server -o yaml | grep -A 10 env:
```

## Support

For issues and questions, please refer to the ReportPortal documentation or open an issue in the repository.

