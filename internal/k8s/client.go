package k8s

import (
	"context"
	"fmt"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes client with PipeOps-specific functionality
type Client struct {
	clientset kubernetes.Interface
	config    *rest.Config
}

// NewClient creates a new Kubernetes client
func NewClient(kubeconfig string, inCluster bool) (*Client, error) {
	var config *rest.Config
	var err error

	if inCluster {
		// Create in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
	} else {
		// Create config from kubeconfig file
		if kubeconfig == "" {
			kubeconfig = clientcmd.RecommendedHomeFile
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create config from kubeconfig: %w", err)
		}
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Client{
		clientset: clientset,
		config:    config,
	}, nil
}

// GetConfig returns the Kubernetes REST config
func (c *Client) GetConfig() *rest.Config {
	return c.config
}

// GetClientset returns the Kubernetes clientset
func (c *Client) GetClientset() kubernetes.Interface {
	return c.clientset
}

// GetClusterStatus returns the current status of the cluster
func (c *Client) GetClusterStatus(ctx context.Context) (*types.ClusterStatus, error) {
	status := &types.ClusterStatus{
		Timestamp: time.Now(),
	}

	// Get nodes
	nodes, err := c.getNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}
	status.Nodes = nodes

	// Get namespaces
	namespaces, err := c.getNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespaces: %w", err)
	}
	status.Namespaces = namespaces

	// Get deployments
	deployments, err := c.getDeployments(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployments: %w", err)
	}
	status.Deployments = deployments

	// Get services
	services, err := c.getServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	status.Services = services

	// Get pods
	pods, err := c.getPods(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}
	status.Pods = pods

	// Calculate metrics
	status.Metrics = c.calculateMetrics(nodes, pods)

	return status, nil
}

// CreateDeployment creates a new deployment
func (c *Client) CreateDeployment(ctx context.Context, req *types.DeploymentRequest) error {
	deployment := c.buildDeployment(req)

	_, err := c.clientset.AppsV1().Deployments(req.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	// Create service if ports are specified
	if len(req.Ports) > 0 {
		service := c.buildService(req)
		_, err = c.clientset.CoreV1().Services(req.Namespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create service: %w", err)
		}
	}

	return nil
}

// UpdateDeployment updates an existing deployment
func (c *Client) UpdateDeployment(ctx context.Context, req *types.DeploymentRequest) error {
	deployment := c.buildDeployment(req)

	_, err := c.clientset.AppsV1().Deployments(req.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}

	return nil
}

// DeleteDeployment deletes a deployment and its associated service
func (c *Client) DeleteDeployment(ctx context.Context, name, namespace string) error {
	// Delete deployment
	err := c.clientset.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	// Delete associated service (ignore errors if service doesn't exist)
	_ = c.clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})

	return nil
}

// ScaleDeployment scales a deployment to the specified number of replicas
func (c *Client) ScaleDeployment(ctx context.Context, name, namespace string, replicas int32) error {
	scale, err := c.clientset.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment scale: %w", err)
	}

	scale.Spec.Replicas = replicas

	_, err = c.clientset.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale deployment: %w", err)
	}

	return nil
}

// GetPodLogs retrieves logs from a pod
func (c *Client) GetPodLogs(ctx context.Context, name, namespace, container string, lines int64) (string, error) {
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
		Container: container,
		TailLines: &lines,
	})

	logs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod logs: %w", err)
	}
	defer logs.Close()

	buf := make([]byte, 1024*1024) // 1MB buffer
	n, err := logs.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	return string(buf[:n]), nil
}

// getNodes retrieves node information
func (c *Client) getNodes(ctx context.Context) ([]types.NodeStatus, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var nodes []types.NodeStatus
	for _, node := range nodeList.Items {
		nodeStatus := types.NodeStatus{
			Name:    node.Name,
			Ready:   c.isNodeReady(&node),
			Version: node.Status.NodeInfo.KubeletVersion,
			OS:      node.Status.NodeInfo.OperatingSystem,
			Arch:    node.Status.NodeInfo.Architecture,
			Labels:  node.Labels,
			Resources: types.NodeResources{
				Allocatable: types.ResourceList{
					CPU:    node.Status.Allocatable.Cpu().String(),
					Memory: node.Status.Allocatable.Memory().String(),
				},
				Capacity: types.ResourceList{
					CPU:    node.Status.Capacity.Cpu().String(),
					Memory: node.Status.Capacity.Memory().String(),
				},
			},
		}
		nodes = append(nodes, nodeStatus)
	}

	return nodes, nil
}

// getNamespaces retrieves namespace names
func (c *Client) getNamespaces(ctx context.Context) ([]string, error) {
	namespaceList, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var namespaces []string
	for _, ns := range namespaceList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	return namespaces, nil
}

// getDeployments retrieves deployment information
func (c *Client) getDeployments(ctx context.Context) ([]types.ResourceInfo, error) {
	deploymentList, err := c.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var deployments []types.ResourceInfo
	for _, dep := range deploymentList.Items {
		deployment := types.ResourceInfo{
			Name:      dep.Name,
			Namespace: dep.Namespace,
			Labels:    dep.Labels,
			Ready:     dep.Status.ReadyReplicas == dep.Status.Replicas,
			CreatedAt: dep.CreationTimestamp.Time,
		}
		deployments = append(deployments, deployment)
	}

	return deployments, nil
}

// getServices retrieves service information
func (c *Client) getServices(ctx context.Context) ([]types.ResourceInfo, error) {
	serviceList, err := c.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var services []types.ResourceInfo
	for _, svc := range serviceList.Items {
		service := types.ResourceInfo{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Labels:    svc.Labels,
			Ready:     true, // Services are generally always ready
			CreatedAt: svc.CreationTimestamp.Time,
		}
		services = append(services, service)
	}

	return services, nil
}

// getPods retrieves pod information
func (c *Client) getPods(ctx context.Context) ([]types.PodInfo, error) {
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var pods []types.PodInfo
	for _, pod := range podList.Items {
		restarts := c.calculateRestarts(&pod)

		podInfo := types.PodInfo{
			ResourceInfo: types.ResourceInfo{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Labels:    pod.Labels,
				Ready:     c.isPodReady(&pod),
				CreatedAt: pod.CreationTimestamp.Time,
			},
			Phase:      string(pod.Status.Phase),
			NodeName:   pod.Spec.NodeName,
			Containers: len(pod.Spec.Containers),
			Restarts:   restarts,
		}
		pods = append(pods, podInfo)
	}

	return pods, nil
}

// buildDeployment builds a Kubernetes deployment from a deployment request
func (c *Client) buildDeployment(req *types.DeploymentRequest) *appsv1.Deployment {
	labels := req.Labels
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["app"] = req.Name

	// Build environment variables
	var envVars []corev1.EnvVar
	for key, value := range req.Environment {
		envVars = append(envVars, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}

	// Build ports
	var ports []corev1.ContainerPort
	for _, port := range req.Ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != "" {
			protocol = corev1.Protocol(port.Protocol)
		}

		ports = append(ports, corev1.ContainerPort{
			Name:          port.Name,
			ContainerPort: port.ContainerPort,
			Protocol:      protocol,
		})
	}

	// Build resource requirements
	resources := corev1.ResourceRequirements{}
	if req.Resources.Limits.CPU != "" || req.Resources.Limits.Memory != "" {
		resources.Limits = corev1.ResourceList{}
		if req.Resources.Limits.CPU != "" {
			resources.Limits[corev1.ResourceCPU] = parseQuantity(req.Resources.Limits.CPU)
		}
		if req.Resources.Limits.Memory != "" {
			resources.Limits[corev1.ResourceMemory] = parseQuantity(req.Resources.Limits.Memory)
		}
	}

	if req.Resources.Requests.CPU != "" || req.Resources.Requests.Memory != "" {
		resources.Requests = corev1.ResourceList{}
		if req.Resources.Requests.CPU != "" {
			resources.Requests[corev1.ResourceCPU] = parseQuantity(req.Resources.Requests.CPU)
		}
		if req.Resources.Requests.Memory != "" {
			resources.Requests[corev1.ResourceMemory] = parseQuantity(req.Resources.Requests.Memory)
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   req.Namespace,
			Labels:      labels,
			Annotations: req.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &req.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": req.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:      req.Name,
							Image:     req.Image,
							Ports:     ports,
							Env:       envVars,
							Resources: resources,
						},
					},
				},
			},
		},
	}
}

// buildService builds a Kubernetes service from a deployment request
func (c *Client) buildService(req *types.DeploymentRequest) *corev1.Service {
	var servicePorts []corev1.ServicePort
	for _, port := range req.Ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != "" {
			protocol = corev1.Protocol(port.Protocol)
		}

		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       port.Name,
			Port:       port.ContainerPort,
			TargetPort: intstr.FromInt(int(port.ContainerPort)),
			Protocol:   protocol,
		})
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    req.Labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": req.Name,
			},
			Ports: servicePorts,
			Type:  corev1.ServiceTypeClusterIP,
		},
	}
}

// Helper functions

func (c *Client) isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (c *Client) isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (c *Client) calculateRestarts(pod *corev1.Pod) int32 {
	var restarts int32
	for _, containerStatus := range pod.Status.ContainerStatuses {
		restarts += containerStatus.RestartCount
	}
	return restarts
}

func (c *Client) calculateMetrics(nodes []types.NodeStatus, pods []types.PodInfo) types.ClusterMetrics {
	return types.ClusterMetrics{
		CPUUsage:    0.0, // Would need metrics server for actual CPU usage
		MemoryUsage: 0.0, // Would need metrics server for actual memory usage
		PodCount:    len(pods),
		NodeCount:   len(nodes),
	}
}

func parseQuantity(s string) resource.Quantity {
	// Parse the quantity string using Kubernetes resource parsing
	quantity, err := resource.ParseQuantity(s)
	if err != nil {
		// Return zero quantity if parsing fails
		return resource.Quantity{}
	}
	return quantity
}
