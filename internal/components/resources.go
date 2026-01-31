package components

import (
	"context"
	"fmt"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ClusterCapacity holds aggregated resource information
type ClusterCapacity struct {
	TotalCPU    resource.Quantity
	TotalMemory resource.Quantity
	NodeCount   int
	Profile     types.ResourceProfile
}

// detectClusterProfile analyzes the cluster nodes to determine the resource profile
func detectClusterProfile(ctx context.Context, client kubernetes.Interface, logger *logrus.Logger) (*ClusterCapacity, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes for resource detection: %w", err)
	}

	totalCPU := resource.NewQuantity(0, resource.DecimalSI)
	totalMem := resource.NewQuantity(0, resource.BinarySI)

	for _, node := range nodes.Items {
		// Use Allocatable instead of Capacity to account for system reservation
		cpu := node.Status.Allocatable[corev1.ResourceCPU]
		mem := node.Status.Allocatable[corev1.ResourceMemory]

		totalCPU.Add(cpu)
		totalMem.Add(mem)
	}

	capacity := &ClusterCapacity{
		TotalCPU:    *totalCPU,
		TotalMemory: *totalMem,
		NodeCount:   len(nodes.Items),
	}

	// Determine profile based on total capacity
	// Note: We look at total cluster capacity, but ideally we should also check per-node capacity
	// for scheduling large pods. For simplicity, we assume a fairly balanced or single-node cluster
	// which is common for "Low" profile scenarios.

	// Memory thresholds (in bytes)
	// 4GB = 4 * 1024 * 1024 * 1024 = 4,294,967,296 bytes
	// 8GB = 8 * 1024 * 1024 * 1024 = 8,589,934,592 bytes
	memBytes := totalMem.Value()
	cpuMillis := totalCPU.MilliValue()

	if memBytes < 4*1024*1024*1024 || cpuMillis < 2000 {
		capacity.Profile = types.ProfileLow
	} else if memBytes < 8*1024*1024*1024 {
		capacity.Profile = types.ProfileMedium
	} else {
		capacity.Profile = types.ProfileHigh
	}

	logger.WithFields(logrus.Fields{
		"profile":      capacity.Profile,
		"total_cpu":    totalCPU.String(),
		"total_memory": totalMem.String(),
		"node_count":   capacity.NodeCount,
	}).Info("Detected cluster resource profile")

	return capacity, nil
}
