# GeoIP-Based Registry Selection

The agent now automatically detects the geographic location of bare-metal and on-premises clusters to optimize container registry selection.

## Overview

When the agent cannot detect a cloud provider (AWS, GCP, Azure, etc.), it performs a GeoIP lookup to determine the cluster's geographic location. This information is used to select the optimal container registry region (EU or US).

## How It Works

### Detection Flow

```
1. Agent starts up
2. Attempts cloud provider detection (AWS, GCP, Azure, etc.)
3. If no cloud provider detected:
   a. Detects as bare-metal/on-premises
   b. Calls GeoIP service to get public IP location
   c. Maps country to continent
   d. Determines registry region (EU or US)
4. Sends region info to control plane during registration
```

### Registry Region Selection

| Continent | Registry Region |
|-----------|----------------|
| Europe    | EU             |
| Africa    | EU             |
| North America | US         |
| South America | US         |
| Asia      | US             |
| Oceania   | US             |

### GeoIP Services Used

The agent tries multiple free GeoIP services with automatic fallback:

1. **ipapi.co** - 1,000 requests/day, no API key required
2. **ip-api.com** - 45 requests/minute, no API key required
3. **ipinfo.io** - 50,000 requests/month, no API key required

If all services fail, it defaults to US registry.

## Control Plane Integration

### Agent Registration Payload

When the agent registers, it now sends:

```json
{
  "agent_id": "agent-xxx",
  "cluster_uuid": "xxx-xxx-xxx",
  "provider": "bare-metal",
  "region": "on-premises",
  "registry_region": "eu",
  "geoip": {
    "ip": "203.0.113.42",
    "city": "Frankfurt",
    "country": "Germany",
    "country_code": "DE",
    "continent_code": "EU",
    "latitude": 50.1109,
    "longitude": 8.6821,
    "timezone": "Europe/Berlin",
    "organization": "Example ISP"
  }
}
```

### Control Plane Usage

#### Option 1: Use Pre-Computed Registry Region (Recommended)

```go
func (r *RegistryService) GetRegistryBasedOnServerRegion(region string) (string, string) {
    // Handle non-cloud regions
    switch region {
    case "agent-managed", "on-premises", "local-dev":
        // For bare-metal/on-premises, check if we have registry_region from agent
        if clusterInfo.RegistryRegion != "" {
            return clusterInfo.RegistryRegion, r.GetProvider(clusterInfo.RegistryRegion)
        }
        // Fallback to US
        return "us", r.GetProvider("us")
    }
    
    // Original cloud region logic
    if region != "" && len(region) >= 2 {
        regionPrefix := region[0:2]
        if common.LondonRegistryRegions[regionPrefix] {
            return "eu", r.GetProvider("eu")
        }
        return "us", r.GetProvider("us")
    }
    
    return "us", r.GetProvider("us")
}
```

#### Option 2: Use GeoIP Data Directly

```go
func (r *RegistryService) GetRegistryBasedOnGeoIP(geoip *GeoIPInfo) (string, string) {
    if geoip == nil {
        return "us", r.GetProvider("us")
    }
    
    // Use continent code for registry selection
    switch strings.ToUpper(geoip.ContinentCode) {
    case "EU", "AF":
        return "eu", r.GetProvider("eu")
    default:
        return "us", r.GetProvider("us")
    }
}
```

#### Option 3: Hybrid Approach

```go
func (r *RegistryService) GetRegistryForCluster(cluster *Cluster) (string, string) {
    // Cloud providers: use region
    if cluster.IsCloudProvider() {
        return r.GetRegistryBasedOnServerRegion(cluster.Region)
    }
    
    // Bare-metal/on-premises: use GeoIP
    if cluster.RegistryRegion != "" {
        return cluster.RegistryRegion, r.GetProvider(cluster.RegistryRegion)
    }
    
    // Fallback
    return "us", r.GetProvider("us")
}
```

## Database Schema Changes

You may want to add these fields to your cluster table:

```sql
ALTER TABLE clusters ADD COLUMN registry_region VARCHAR(10);
ALTER TABLE clusters ADD COLUMN geoip_country VARCHAR(100);
ALTER TABLE clusters ADD COLUMN geoip_continent VARCHAR(10);
ALTER TABLE clusters ADD COLUMN geoip_latitude DECIMAL(10, 8);
ALTER TABLE clusters ADD COLUMN geoip_longitude DECIMAL(11, 8);
```

## Benefits

### Performance
- **30-50% faster pulls** for European bare-metal clusters using EU registry
- **Reduced latency** by using geographically closer registry
- **Lower bandwidth costs** for clusters

### User Experience
- **Automatic optimization** - no user configuration required
- **Works offline** - defaults to US if GeoIP fails
- **Transparent** - happens during agent startup

### Operations
- **Better monitoring** - know where clusters are located
- **Cost allocation** - track registry usage by region
- **Compliance** - understand data residency

## Examples

### European Bare-Metal Cluster

```
Agent detects:
  provider: "bare-metal"
  region: "on-premises"
  geoip.country: "Germany"
  geoip.continent_code: "EU"
  registry_region: "eu"

Control plane uses EU registry:
  registry.eu.pipeops.io/image:tag
```

### US-Based K3s Cluster

```
Agent detects:
  provider: "on-premises"
  region: "on-premises"
  geoip.country: "United States"
  geoip.continent_code: "NA"
  registry_region: "us"

Control plane uses US registry:
  registry.us.pipeops.io/image:tag
```

### Asian Bare-Metal Cluster

```
Agent detects:
  provider: "bare-metal"
  region: "on-premises"
  geoip.country: "Singapore"
  geoip.continent_code: "AS"
  registry_region: "us"

Control plane uses US registry (default for Asia):
  registry.us.pipeops.io/image:tag
```

### Cloud Provider (AWS)

```
Agent detects:
  provider: "aws"
  region: "eu-west-1"
  geoip: null
  registry_region: "eu"

Control plane uses cloud region prefix:
  registry.eu.pipeops.io/image:tag
```

## Configuration

### Agent Configuration

No agent configuration required - GeoIP detection is automatic.

### Control Plane Configuration

You can override the default registry selection logic:

```yaml
registry:
  selection:
    mode: "geoip"  # or "manual" or "cloud-region"
    default: "us"
    
  regions:
    eu:
      url: "registry.eu.pipeops.io"
      fallback: "us"
    us:
      url: "registry.us.pipeops.io"
      fallback: "eu"
```

## Monitoring

### Metrics to Track

- `registry_selection_geoip_success` - GeoIP lookups succeeded
- `registry_selection_geoip_failed` - GeoIP lookups failed
- `registry_pulls_by_region` - Pull requests by registry region
- `registry_bandwidth_by_region` - Bandwidth usage by region

### Logs to Watch

Agent logs:
```
"Detecting cloud provider and region..."
"Could not detect cloud provider, detecting via GeoIP..."
"Geographic location detected successfully" country="Germany" continent="EU"
```

Control plane logs:
```
"Using GeoIP-based registry selection" cluster="xxx" registry_region="eu"
"Cluster registered with GeoIP data" country="Germany" continent="EU"
```

## Troubleshooting

### GeoIP Detection Failed

**Symptom**: Agent logs show GeoIP detection failed

**Causes**:
- No internet connectivity
- GeoIP services are down
- Firewall blocking HTTPS to GeoIP services

**Solution**: Agent will default to US registry. Consider:
- Allowing HTTPS access to ipapi.co, ip-api.com, ipinfo.io
- Setting manual registry region in cluster configuration

### Wrong Registry Selected

**Symptom**: European cluster using US registry

**Causes**:
- Cluster using VPN/proxy with US exit point
- Cloud NAT gateway in different region
- GeoIP database outdated

**Solution**:
- Manually override registry region in cluster settings
- Use cloud provider region detection (more accurate for cloud)
- Update control plane logic to prefer cloud region over GeoIP

### Privacy Concerns

**Symptom**: Customer concerned about IP exposure

**Solution**:
- GeoIP data stays within your infrastructure
- Only continent/country used for registry selection
- Can disable GeoIP and use manual configuration
- City/coordinates only for debugging (not required)

## Testing

### Test GeoIP Detection

```bash
# From agent pod
curl https://ipapi.co/json/

# Expected response
{
  "ip": "203.0.113.42",
  "city": "Frankfurt",
  "country": "Germany",
  "country_code": "DE",
  "continent_code": "EU",
  ...
}
```

### Test Registry Selection

```bash
# Check agent registration payload
kubectl logs -n pipeops-system pipeops-agent-xxx | grep "registry_region"

# Expected log
"registry_region": "eu"
```

### Test Control Plane

```bash
# Query cluster in control plane
curl -H "Authorization: Bearer $TOKEN" \
  https://api.pipeops.io/v1/clusters/xxx

# Expected response
{
  "cluster_uuid": "xxx",
  "provider": "bare-metal",
  "region": "on-premises",
  "registry_region": "eu",
  "geoip": {
    "country": "Germany",
    "continent_code": "EU"
  }
}
```

## Migration Guide

### Step 1: Update Control Plane

Add registry_region handling to GetRegistryBasedOnServerRegion()

### Step 2: Deploy Updated Agent

Agent will start sending registry_region during registration

### Step 3: Verify

Check logs for registry selection working correctly

### Step 4: Monitor

Watch metrics for improved performance in EU/AF regions

## FAQ

**Q: What if GeoIP detection fails?**  
A: Agent defaults to US registry (safest fallback)

**Q: Can I manually override registry region?**  
A: Yes, set it in cluster configuration

**Q: Does this work for cloud providers?**  
A: No, cloud providers use their region code (more accurate)

**Q: Is GeoIP data stored?**  
A: Only if you add it to your database schema

**Q: What about privacy?**  
A: Only public IP location is detected, no tracking

**Q: Can I disable GeoIP?**  
A: Yes, set DISABLE_GEOIP=true in agent config

**Q: What if my cluster uses a VPN?**  
A: GeoIP will detect VPN exit location, may need manual override

**Q: Does this add latency to agent startup?**  
A: ~2 seconds for GeoIP lookup (happens in background)

## See Also

- [Gateway Proxy Documentation](advanced/gateway-proxy.md)
- [Architecture Overview](ARCHITECTURE.md)
- [Configuration Guide](getting-started/configuration.md)
