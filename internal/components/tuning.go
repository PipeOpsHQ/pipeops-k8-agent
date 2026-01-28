package components

import (
	"fmt"
)

// getPrometheusValues returns tuned Helm values for kube-prometheus-stack based on profile
func (m *Manager) getPrometheusValues(profile ResourceProfile) map[string]interface{} {
	// Base values
	values := map[string]interface{}{
		"prometheus": map[string]interface{}{
			"prometheusSpec": map[string]interface{}{
				"retention": m.stack.Prometheus.RetentionPeriod,
				"storageSpec": func() map[string]interface{} {
					if m.stack.Prometheus.EnablePersistence {
						return map[string]interface{}{
							"volumeClaimTemplate": map[string]interface{}{
								"spec": map[string]interface{}{
									"storageClassName": m.stack.Prometheus.StorageClass,
									"accessModes":      []string{"ReadWriteOnce"},
									"resources": map[string]interface{}{
										"requests": map[string]interface{}{
											"storage": m.stack.Prometheus.StorageSize,
										},
									},
								},
							},
						}
					}
					return nil
				}(),
				"serviceMonitorSelectorNilUsesHelmValues": false,
				"podMonitorSelectorNilUsesHelmValues":     false,
			},
			"service": map[string]interface{}{
				"type": "ClusterIP",
				"port": m.stack.Prometheus.LocalPort,
			},
		},
		"grafana": func() map[string]interface{} {
			grafanaValues := map[string]interface{}{
				"enabled":       m.stack.Grafana.Enabled,
				"adminUser":     m.stack.Grafana.AdminUser,
				"adminPassword": m.stack.Grafana.AdminPassword,
				"persistence": map[string]interface{}{
					"enabled": m.stack.Grafana.EnablePersistence,
					"storageClassName": func() string {
						if m.stack.Grafana.EnablePersistence {
							return m.stack.Grafana.StorageClass
						}
						return ""
					}(),
					"size": m.stack.Grafana.StorageSize,
				},
				"service": map[string]interface{}{
					"type": "ClusterIP",
					"port": m.stack.Grafana.LocalPort,
				},
				"additionalDataSources": func() []map[string]interface{} {
					if m.stack.Loki != nil && m.stack.Loki.Enabled {
						return []map[string]interface{}{
							{
								"name": "Loki",
								"type": "loki",
								"url": fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
									m.stack.Loki.ReleaseName,
									m.stack.Loki.Namespace,
									m.stack.Loki.LocalPort),
								"access": "proxy",
							},
						}
					}
					return nil
				}(),
			}

			if m.stack.Grafana != nil && (m.stack.Grafana.RootURL != "" || m.stack.Grafana.ServeFromSubPath) {
				serverConfig := map[string]interface{}{}
				if m.stack.Grafana.RootURL != "" {
					serverConfig["root_url"] = m.stack.Grafana.RootURL
				}
				if m.stack.Grafana.ServeFromSubPath {
					serverConfig["serve_from_sub_path"] = true
				}
				if len(serverConfig) > 0 {
					grafanaValues["grafana.ini"] = map[string]interface{}{
						"server": serverConfig,
					}
				}
			}
			return grafanaValues
		}(),
		"alertmanager": map[string]interface{}{
			"alertmanagerSpec": map[string]interface{}{
				"storage": func() map[string]interface{} {
					if m.stack.Prometheus.EnablePersistence {
						return map[string]interface{}{
							"volumeClaimTemplate": map[string]interface{}{
								"spec": map[string]interface{}{
									"storageClassName": m.stack.Prometheus.StorageClass,
									"accessModes":      []string{"ReadWriteOnce"},
									"resources": map[string]interface{}{
										"requests": map[string]interface{}{
											"storage": "2Gi",
										},
									},
								},
							},
						}
					}
					return nil
				}(),
			},
		},
		"kube-state-metrics": map[string]interface{}{
			"enabled": true,
		},
		"prometheus-node-exporter": map[string]interface{}{
			"enabled": true,
		},
	}

	// Tune based on profile
	if profile == ProfileLow {
		m.logger.Info("Applying Low Profile tuning for Prometheus stack")

		// Prometheus Resource Limits
		values["prometheus"].(map[string]interface{})["prometheusSpec"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "50m", "memory": "256Mi"},
			"limits":   map[string]interface{}{"cpu": "500m", "memory": "512Mi"},
		}
		// Reduce retention to save disk/processing
		values["prometheus"].(map[string]interface{})["prometheusSpec"].(map[string]interface{})["retention"] = "2d"

		// Alertmanager Resource Limits
		values["alertmanager"].(map[string]interface{})["alertmanagerSpec"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
			"limits":   map[string]interface{}{"cpu": "50m", "memory": "64Mi"},
		}

		// Node Exporter Resource Limits
		values["prometheus-node-exporter"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "10m", "memory": "16Mi"},
			"limits":   map[string]interface{}{"cpu": "100m", "memory": "64Mi"},
		}

		// Kube State Metrics Resource Limits
		values["kube-state-metrics"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
			"limits":   map[string]interface{}{"cpu": "100m", "memory": "64Mi"},
		}

		// Grafana Resource Limits
		values["grafana"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]interface{}{"cpu": "200m", "memory": "128Mi"},
		}
	} else if profile == ProfileMedium {
		// Medium profile standard defaults (can be tuned if needed)
		// Usually Helm chart defaults are fine for medium, but explicit limits are safer
		values["prometheus"].(map[string]interface{})["prometheusSpec"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "200m", "memory": "512Mi"},
			"limits":   map[string]interface{}{"cpu": "1000m", "memory": "1Gi"},
		}
	}

	return values
}

// getLokiValues returns tuned Helm values for Loki based on profile
func (m *Manager) getLokiValues(profile ResourceProfile) map[string]interface{} {
	values := map[string]interface{}{
		"loki": map[string]interface{}{
			"enabled": true,
			"persistence": map[string]interface{}{
				"enabled": m.stack.Loki.EnablePersistence,
				"storageClassName": func() string {
					if m.stack.Loki.EnablePersistence {
						return m.stack.Loki.StorageClass
					}
					return ""
				}(),
				"size": m.stack.Loki.StorageSize,
			},
			"config": map[string]interface{}{
				"auth_enabled": false,
				"chunk_store_config": map[string]interface{}{
					"max_look_back_period": "0s",
				},
				"table_manager": map[string]interface{}{
					"retention_deletes_enabled": true,
					"retention_period":          "168h", // 7 days
				},
			},
		},
		"promtail": map[string]interface{}{
			"enabled": true,
		},
		"grafana": map[string]interface{}{
			"enabled": false,
		},
	}

	if profile == ProfileLow {
		m.logger.Info("Applying Low Profile tuning for Loki stack")

		// Loki Resource Limits
		values["loki"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "50m", "memory": "128Mi"},
			"limits":   map[string]interface{}{"cpu": "200m", "memory": "256Mi"},
		}
		// Reduce retention
		values["loki"].(map[string]interface{})["config"].(map[string]interface{})["table_manager"].(map[string]interface{})["retention_period"] = "48h"

		// Promtail Resource Limits
		values["promtail"].(map[string]interface{})["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "20m", "memory": "32Mi"},
			"limits":   map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
		}
	}

	return values
}

// getCertManagerValues returns tuned Helm values for cert-manager based on profile
func (m *Manager) getCertManagerValues(profile ResourceProfile) map[string]interface{} {
	values := map[string]interface{}{
		"installCRDs": m.stack.CertManager.InstallCRDs,
		"global": map[string]interface{}{
			"leaderElection": map[string]interface{}{
				"namespace": m.stack.CertManager.Namespace,
			},
		},
	}

	if profile == ProfileLow {
		m.logger.Info("Applying Low Profile tuning for cert-manager")
		// Very conservative limits for Low profile
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
			"limits":   map[string]interface{}{"cpu": "100m", "memory": "64Mi"},
		}
		values["webhook"] = map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "10m", "memory": "24Mi"},
				"limits":   map[string]interface{}{"cpu": "50m", "memory": "48Mi"},
			},
		}
		values["cainjector"] = map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
				"limits":   map[string]interface{}{"cpu": "100m", "memory": "64Mi"},
			},
		}
	} else {
		// Standard limits (from previous implementation)
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
			"limits":   map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
		}
		values["webhook"] = map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
				"limits":   map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
			},
		}
		values["cainjector"] = map[string]interface{}{
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "10m", "memory": "32Mi"},
				"limits":   map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
			},
		}
	}

	return values
}
