# Control Plane Region Integration Guide

## Overview

The PipeOps agent now automatically detects cloud provider regions and geographic location (for bare-metal clusters) during registration. This enables the control plane to make intelligent decisions about registry selection, routing, and resource allocation.

## What Changed in the Agent

### Region Detection Flow

When the agent starts and registers with the control plane, it now:

1. **Detects Cloud Provider & Region** (AWS, GCP, Azure, DigitalOcean, Hetzner, Linode)
2. **Falls back to GeoIP** for bare-metal/on-premises clusters
3. **Sends Region Information** in registration payload

### Registration Payload (Enhanced)

The agent now sends this additional information:

```go
type Agent struct {
    // ... existing fields ...
    
    // NEW: Cloud provider and region detection
    Region          string  `json:"region"`           // e.g., "us-east-1", "eu-west-1", "on-premises"
    CloudProvider   string  `json:"cloud_provider"`   // e.g., "aws", "gcp", "bare-metal"
    
    // NEW: GeoIP data (only for bare-metal/on-premises)
    GeoIP *GeoIPInfo `json:"geoip,omitempty"`
}

type GeoIPInfo struct {
    IP            string  `json:"ip"`
    City          string  `json:"city"`
    Country       string  `json:"country"`
    CountryCode   string  `json:"country_code"`
    ContinentCode string  `json:"continent_code"`
    Latitude      float64 `json:"latitude"`
    Longitude     float64 `json:"longitude"`
}
```

### Example Registration Payloads

#### Cloud Provider (AWS in Ireland)
```json
{
    "agent_id": "agent-abc123",
    "cluster_uuid": "uuid-456",
    "name": "production-cluster",
    "region": "eu-west-1",
    "cloud_provider": "aws",
    "geoip": null
}
```

#### Bare-Metal (Germany)
```json
{
    "agent_id": "agent-xyz789",
    "cluster_uuid": "uuid-999",
    "name": "datacenter-cluster",
    "region": "on-premises",
    "cloud_provider": "bare-metal",
    "geoip": {
        "ip": "203.0.113.42",
        "city": "Frankfurt",
        "country": "Germany",
        "country_code": "DE",
        "continent_code": "EU",
        "latitude": 50.1109,
        "longitude": 8.6821
    }
}
```

#### On-Premises K3s (US)
```json
{
    "agent_id": "agent-k3s001",
    "cluster_uuid": "uuid-777",
    "name": "edge-cluster",
    "region": "on-premises",
    "cloud_provider": "on-premises",
    "geoip": {
        "country": "United States",
        "country_code": "US",
        "continent_code": "NA"
    }
}
```

## Control Plane Integration

### Option 1: Use Existing GetRegistryBasedOnServerRegion (Recommended)

Update your existing registry selection function to handle agent regions:

```go
func (r *RegistryService) GetRegistryBasedOnServerRegion(region string) (string, string) {
    // Handle special agent regions
    switch region {
    case "agent-managed", "on-premises", "local-dev", "":
        // For bare-metal/on-premises, use GeoIP data if available
        // This requires passing the cluster object to access GeoIP data
        // For now, default to US
        return "us", r.GetProvider("us")
    }
    
    // Cloud provider regions: use existing logic
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

### Option 2: Enhanced Registry Selection with GeoIP

Create a new method that considers GeoIP data:

```go
func (r *RegistryService) GetRegistryForCluster(cluster *Cluster) (string, string) {
    // Cloud providers: use region prefix
    if cluster.CloudProvider != "" && 
       cluster.CloudProvider != "bare-metal" && 
       cluster.CloudProvider != "on-premises" &&
       cluster.CloudProvider != "agent" {
        return r.GetRegistryBasedOnServerRegion(cluster.Region)
    }
    
    // Bare-metal/on-premises: use GeoIP
    if cluster.GeoIP != nil {
        return r.GetRegistryFromGeoIP(cluster.GeoIP)
    }
    
    // Fallback to US
    return "us", r.GetProvider("us")
}

func (r *RegistryService) GetRegistryFromGeoIP(geoip *GeoIPInfo) (string, string) {
    if geoip == nil {
        return "us", r.GetProvider("us")
    }
    
    // Use continent code for registry selection
    switch strings.ToUpper(geoip.ContinentCode) {
    case "EU", "AF":
        return "eu", r.GetProvider("eu")
    case "AS", "OC", "NA", "SA":
        return "us", r.GetProvider("us")
    default:
        return "us", r.GetProvider("us")
    }
}
```

### Option 3: Pre-Computed Registry Region

Store the computed registry region in the database during registration:

```go
// When agent registers:
func (s *ClusterService) RegisterAgent(agent *Agent) error {
    // ... existing registration logic ...
    
    // Compute and store registry region
    registryRegion := s.computeRegistryRegion(agent)
    cluster.RegistryRegion = registryRegion
    
    // Save to database
    return s.db.Save(&cluster).Error
}

func (s *ClusterService) computeRegistryRegion(agent *Agent) string {
    // Cloud provider: use region prefix
    if agent.CloudProvider != "" && 
       agent.CloudProvider != "bare-metal" && 
       agent.CloudProvider != "on-premises" {
        if len(agent.Region) >= 2 {
            prefix := agent.Region[0:2]
            if prefix == "eu" {
                return "eu"
            }
        }
        return "us"
    }
    
    // Bare-metal/on-premises: use GeoIP
    if agent.GeoIP != nil {
        switch strings.ToUpper(agent.GeoIP.ContinentCode) {
        case "EU", "AF":
            return "eu"
        default:
            return "us"
        }
    }
    
    return "us"
}

// Then simply use the stored value:
func (r *RegistryService) GetRegistryForCluster(cluster *Cluster) (string, string) {
    if cluster.RegistryRegion != "" {
        return cluster.RegistryRegion, r.GetProvider(cluster.RegistryRegion)
    }
    return "us", r.GetProvider("us")
}
```

## Database Schema Updates

### Recommended Additions

```sql
-- Add to your clusters table
ALTER TABLE clusters ADD COLUMN registry_region VARCHAR(10) DEFAULT 'us';
ALTER TABLE clusters ADD COLUMN geoip_country VARCHAR(100);
ALTER TABLE clusters ADD COLUMN geoip_continent VARCHAR(10);
ALTER TABLE clusters ADD COLUMN geoip_latitude DECIMAL(10, 8);
ALTER TABLE clusters ADD COLUMN geoip_longitude DECIMAL(11, 8);

-- Optional: add indexes for queries
CREATE INDEX idx_clusters_registry_region ON clusters(registry_region);
CREATE INDEX idx_clusters_geoip_continent ON clusters(geoip_continent);
```

### Go Model Updates

```go
type Cluster struct {
    // ... existing fields ...
    
    // Region information
    Region          string  `json:"region" gorm:"column:region"`
    CloudProvider   string  `json:"cloud_provider" gorm:"column:cloud_provider"`
    RegistryRegion  string  `json:"registry_region" gorm:"column:registry_region;default:us"`
    
    // GeoIP information (for bare-metal/on-premises)
    GeoIPCountry    string  `json:"geoip_country,omitempty" gorm:"column:geoip_country"`
    GeoIPContinent  string  `json:"geoip_continent,omitempty" gorm:"column:geoip_continent"`
    GeoIPLatitude   float64 `json:"geoip_latitude,omitempty" gorm:"column:geoip_latitude"`
    GeoIPLongitude  float64 `json:"geoip_longitude,omitempty" gorm:"column:geoip_longitude"`
}
```

## Migration Path

### Phase 1: Store Data (Non-Breaking)

1. Update cluster registration endpoint to accept new fields
2. Store region/geoip data in database
3. Keep existing registry selection logic unchanged

```go
// Agent registration handler
func (h *Handler) RegisterAgent(c *gin.Context) {
    var agent types.Agent
    if err := c.ShouldBindJSON(&agent); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // Store GeoIP data if provided
    if agent.GeoIP != nil {
        cluster.GeoIPCountry = agent.GeoIP.Country
        cluster.GeoIPContinent = agent.GeoIP.ContinentCode
        cluster.GeoIPLatitude = agent.GeoIP.Latitude
        cluster.GeoIPLongitude = agent.GeoIP.Longitude
    }
    
    // Compute and store registry region
    cluster.RegistryRegion = computeRegistryRegion(&agent)
    
    // ... rest of registration ...
}
```

### Phase 2: Enable Smart Selection (Gradual Rollout)

1. Add feature flag for GeoIP-based selection
2. Test with subset of clusters
3. Monitor performance improvements

```go
func (r *RegistryService) GetRegistryForCluster(cluster *Cluster) (string, string) {
    // Feature flag
    if config.EnableGeoIPRegistry {
        if cluster.RegistryRegion != "" {
            return cluster.RegistryRegion, r.GetProvider(cluster.RegistryRegion)
        }
    }
    
    // Fallback to existing logic
    return r.GetRegistryBasedOnServerRegion(cluster.Region)
}
```

### Phase 3: Full Rollout

1. Enable for all clusters
2. Remove feature flag
3. Monitor metrics

## Benefits & Metrics

### Expected Performance Improvements

- **30-50% faster pulls** for EU bare-metal clusters (vs US registry)
- **Reduced bandwidth costs** for control plane
- **Lower latency** for image operations

### Metrics to Track

```go
// Registry selection metrics
registrySelectionGeoIP := prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "registry_selection_geoip_total",
        Help: "Total registry selections using GeoIP",
    },
    []string{"region", "continent"},
)

registryPullLatency := prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name: "registry_pull_latency_seconds",
        Help: "Registry pull latency by region",
        Buckets: prometheus.DefBuckets,
    },
    []string{"registry_region", "cluster_continent"},
)

// Query examples
// - How many clusters are in EU?
SELECT COUNT(*) FROM clusters WHERE geoip_continent = 'EU';

// - What's the registry distribution?
SELECT registry_region, COUNT(*) FROM clusters GROUP BY registry_region;

// - Performance by continent
SELECT 
    geoip_continent,
    AVG(pull_latency_ms) as avg_latency
FROM cluster_metrics
WHERE metric_type = 'image_pull'
GROUP BY geoip_continent;
```

## Testing

### Unit Tests

```go
func TestComputeRegistryRegion(t *testing.T) {
    tests := []struct {
        name     string
        agent    *Agent
        expected string
    }{
        {
            name: "AWS EU region",
            agent: &Agent{
                Region:        "eu-west-1",
                CloudProvider: "aws",
            },
            expected: "eu",
        },
        {
            name: "Bare-metal EU",
            agent: &Agent{
                Region:        "on-premises",
                CloudProvider: "bare-metal",
                GeoIP: &GeoIPInfo{
                    ContinentCode: "EU",
                },
            },
            expected: "eu",
        },
        {
            name: "US on-premises",
            agent: &Agent{
                Region:        "on-premises",
                CloudProvider: "on-premises",
                GeoIP: &GeoIPInfo{
                    ContinentCode: "NA",
                },
            },
            expected: "us",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := computeRegistryRegion(tt.agent)
            if result != tt.expected {
                t.Errorf("expected %s, got %s", tt.expected, result)
            }
        })
    }
}
```

### Integration Testing

```bash
# Test agent registration with GeoIP
curl -X POST https://api.pipeops.io/v1/agents/register \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "test-agent",
    "region": "on-premises",
    "cloud_provider": "bare-metal",
    "geoip": {
        "country": "Germany",
        "continent_code": "EU"
    }
}'

# Verify registry selection
curl https://api.pipeops.io/v1/clusters/$CLUSTER_ID \
  -H "Authorization: Bearer $TOKEN" \
  | jq '.registry_region'
# Expected: "eu"
```

## FAQ

**Q: What if GeoIP detection fails in the agent?**  
A: Agent defaults to `cloud_provider: "bare-metal"`, `region: "on-premises"`, `geoip: null`. Control plane should default to US registry.

**Q: Can users override the auto-detected region?**  
A: Yes, add a manual override field in cluster settings that takes precedence.

**Q: Does this work for existing clusters?**  
A: New data is sent on next agent heartbeat/re-registration. Consider a migration script for bulk updates.

**Q: What about clusters behind VPN/proxy?**  
A: GeoIP will detect the exit point location. Provide manual override for accuracy.

**Q: How do I disable GeoIP for privacy concerns?**  
A: Agent can set `DISABLE_GEOIP=true` env var to skip GeoIP lookup.

## Summary

The agent now provides comprehensive region detection:
- **Automatic** for cloud providers (AWS, GCP, Azure, etc.)
- **GeoIP-based** for bare-metal/on-premises
- **Pre-computed** registry region for easy integration
- **Backward compatible** with existing control plane code

Choose your integration approach:
1. **Quick**: Use pre-computed `registry_region` field
2. **Smart**: Implement GeoIP-aware registry selection
3. **Hybrid**: Feature flag gradual rollout

All changes are non-breaking and provide immediate value.
