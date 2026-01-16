package upgrade

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// SystemUpgradeNamespace is the namespace where system-upgrade-controller runs
	SystemUpgradeNamespace = "system-upgrade"

	// Plan GVR
	PlanGroup    = "upgrade.cattle.io"
	PlanVersion  = "v1"
	PlanResource = "plans"
)

// UpgradeChannel represents available K3s release channels
type UpgradeChannel string

const (
	ChannelStable UpgradeChannel = "stable"
	ChannelLatest UpgradeChannel = "latest"
	ChannelV1_30  UpgradeChannel = "v1.30"
	ChannelV1_31  UpgradeChannel = "v1.31"
	ChannelV1_32  UpgradeChannel = "v1.32"
	ChannelV1_33  UpgradeChannel = "v1.33"
)

// UpgradeConfig holds configuration for cluster upgrades
type UpgradeConfig struct {
	// Enabled enables automated upgrades
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Channel is the K3s release channel to track (stable, latest, v1.30, etc.)
	Channel UpgradeChannel `yaml:"channel" json:"channel"`

	// Version is a specific version to upgrade to (overrides channel)
	Version string `yaml:"version,omitempty" json:"version,omitempty"`

	// Concurrency is the number of nodes to upgrade simultaneously
	Concurrency int `yaml:"concurrency" json:"concurrency"`

	// Window defines when upgrades can occur
	Window *UpgradeWindow `yaml:"window,omitempty" json:"window,omitempty"`

	// DrainTimeout is the timeout for draining nodes before upgrade
	DrainTimeout time.Duration `yaml:"drain_timeout" json:"drain_timeout"`

	// AutoInstallController automatically installs system-upgrade-controller if missing
	AutoInstallController bool `yaml:"auto_install_controller" json:"auto_install_controller"`
}

// UpgradeWindow defines when upgrades can occur
type UpgradeWindow struct {
	// Days of the week when upgrades are allowed
	Days []string `yaml:"days" json:"days"`

	// StartTime in HH:MM format
	StartTime string `yaml:"start_time" json:"start_time"`

	// EndTime in HH:MM format
	EndTime string `yaml:"end_time" json:"end_time"`

	// TimeZone (e.g., "UTC", "America/New_York")
	TimeZone string `yaml:"timezone" json:"timezone"`
}

// UpgradeStatus represents the current upgrade status
type UpgradeStatus struct {
	// ControllerInstalled indicates if system-upgrade-controller is installed
	ControllerInstalled bool `json:"controller_installed"`

	// ControllerReady indicates if the controller is ready
	ControllerReady bool `json:"controller_ready"`

	// Plans lists the current upgrade plans
	Plans []PlanStatus `json:"plans"`

	// CurrentVersion is the current K3s version
	CurrentVersion string `json:"current_version"`

	// TargetVersion is the version being upgraded to
	TargetVersion string `json:"target_version,omitempty"`

	// LastChecked is when the status was last checked
	LastChecked time.Time `json:"last_checked"`
}

// PlanStatus represents the status of an upgrade plan
type PlanStatus struct {
	Name           string    `json:"name"`
	Channel        string    `json:"channel,omitempty"`
	Version        string    `json:"version,omitempty"`
	LatestVersion  string    `json:"latest_version,omitempty"`
	NodesUpgraded  int       `json:"nodes_upgraded"`
	NodesPending   int       `json:"nodes_pending"`
	NodesTotal     int       `json:"nodes_total"`
	LastUpdateTime time.Time `json:"last_update_time,omitempty"`
}

// DefaultUpgradeConfig returns the default upgrade configuration
func DefaultUpgradeConfig() UpgradeConfig {
	return UpgradeConfig{
		Enabled:               false,
		Channel:               ChannelStable,
		Concurrency:           1,
		DrainTimeout:          5 * time.Minute,
		AutoInstallController: true,
	}
}

// Manager handles K3s cluster upgrades
type Manager struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	extClient     apiextensionsclient.Interface
	config        UpgradeConfig
	logger        *logrus.Logger
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewManager creates a new upgrade manager
func NewManager(restConfig *rest.Config, config UpgradeConfig, logger *logrus.Logger) (*Manager, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	extClient, err := apiextensionsclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create extensions client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		extClient:     extClient,
		config:        config,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

// Start starts the upgrade manager
func (m *Manager) Start() error {
	if !m.config.Enabled {
		m.logger.Info("[UPGRADE] Automated upgrades disabled")
		return nil
	}

	m.logger.WithFields(logrus.Fields{
		"channel":     m.config.Channel,
		"concurrency": m.config.Concurrency,
	}).Info("[UPGRADE] Starting upgrade manager")

	// Check and install controller if needed
	if m.config.AutoInstallController {
		if err := m.EnsureControllerInstalled(m.ctx); err != nil {
			m.logger.WithError(err).Warn("[UPGRADE] Failed to ensure controller is installed")
		}
	}

	// Create default upgrade plans if they don't exist
	if err := m.EnsureUpgradePlans(m.ctx); err != nil {
		m.logger.WithError(err).Warn("[UPGRADE] Failed to create upgrade plans")
	}

	return nil
}

// Stop stops the upgrade manager
func (m *Manager) Stop() {
	m.cancel()
	m.logger.Info("[UPGRADE] Upgrade manager stopped")
}

// IsControllerInstalled checks if system-upgrade-controller is installed
func (m *Manager) IsControllerInstalled(ctx context.Context) (bool, error) {
	// Check if the CRD exists
	_, err := m.extClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, "plans.upgrade.cattle.io", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// Check if the controller deployment exists
	_, err = m.clientset.AppsV1().Deployments(SystemUpgradeNamespace).Get(ctx, "system-upgrade-controller", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// EnsureControllerInstalled ensures the system-upgrade-controller is installed
func (m *Manager) EnsureControllerInstalled(ctx context.Context) error {
	installed, err := m.IsControllerInstalled(ctx)
	if err != nil {
		return fmt.Errorf("failed to check controller status: %w", err)
	}

	if installed {
		m.logger.Debug("[UPGRADE] System-upgrade-controller already installed")
		return nil
	}

	m.logger.Info("[UPGRADE] Installing system-upgrade-controller")

	// Create namespace
	if err := m.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Create CRD
	if err := m.createPlanCRD(ctx); err != nil {
		return fmt.Errorf("failed to create CRD: %w", err)
	}

	// Create RBAC
	if err := m.createRBAC(ctx); err != nil {
		return fmt.Errorf("failed to create RBAC: %w", err)
	}

	// Create controller deployment
	if err := m.createControllerDeployment(ctx); err != nil {
		return fmt.Errorf("failed to create controller deployment: %w", err)
	}

	m.logger.Info("[UPGRADE] System-upgrade-controller installed successfully")
	return nil
}

// ensureNamespace ensures the system-upgrade namespace exists
func (m *Manager) ensureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SystemUpgradeNamespace,
			Labels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
			},
		},
	}

	_, err := m.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createPlanCRD creates the Plan CRD
func (m *Manager) createPlanCRD(ctx context.Context) error {
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plans.upgrade.cattle.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: PlanGroup,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Plan",
				ListKind: "PlanList",
				Plural:   "plans",
				Singular: "plan",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: boolPtr(true),
						},
					},
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
				},
			},
		},
	}

	_, err := m.extClient.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createRBAC creates the necessary RBAC resources
func (m *Manager) createRBAC(ctx context.Context) error {
	// Service Account
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system-upgrade",
			Namespace: SystemUpgradeNamespace,
		},
	}
	_, err := m.clientset.CoreV1().ServiceAccounts(SystemUpgradeNamespace).Create(ctx, sa, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	// ClusterRoleBinding
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system-upgrade",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "system-upgrade",
				Namespace: SystemUpgradeNamespace,
			},
		},
	}
	_, err = m.clientset.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

// createControllerDeployment creates the system-upgrade-controller deployment
func (m *Manager) createControllerDeployment(ctx context.Context) error {
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system-upgrade-controller",
			Namespace: SystemUpgradeNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"upgrade.cattle.io/controller": "system-upgrade-controller",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"upgrade.cattle.io/controller": "system-upgrade-controller",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "system-upgrade",
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-role.kubernetes.io/control-plane",
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{"true"},
											},
										},
									},
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "CriticalAddonsOnly",
							Operator: corev1.TolerationOpExists,
						},
						{
							Key:      "node-role.kubernetes.io/master",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
						{
							Key:      "node-role.kubernetes.io/control-plane",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
						{
							Key:      "node-role.kubernetes.io/etcd",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoExecute,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "system-upgrade-controller",
							Image: "rancher/system-upgrade-controller:v0.14.2",
							Env: []corev1.EnvVar{
								{
									Name: "SYSTEM_UPGRADE_CONTROLLER_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: "SYSTEM_UPGRADE_CONTROLLER_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "etc-ssl",
									MountPath: "/etc/ssl",
									ReadOnly:  true,
								},
								{
									Name:      "etc-pki",
									MountPath: "/etc/pki",
									ReadOnly:  true,
								},
								{
									Name:      "etc-ca-certificates",
									MountPath: "/etc/ca-certificates",
									ReadOnly:  true,
								},
								{
									Name:      "tmp",
									MountPath: "/tmp",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "etc-ssl",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/ssl",
									Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: "etc-pki",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/pki",
									Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: "etc-ca-certificates",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/etc/ca-certificates",
									Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: "tmp",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	_, err := m.clientset.AppsV1().Deployments(SystemUpgradeNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// EnsureUpgradePlans ensures the upgrade plans exist
func (m *Manager) EnsureUpgradePlans(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	// Check if server plan exists
	_, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Get(ctx, "k3s-server", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			if err := m.createServerPlan(ctx); err != nil {
				return fmt.Errorf("failed to create server plan: %w", err)
			}
		} else {
			return err
		}
	}

	// Check if agent plan exists
	_, err = m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Get(ctx, "k3s-agent", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			if err := m.createAgentPlan(ctx); err != nil {
				return fmt.Errorf("failed to create agent plan: %w", err)
			}
		} else {
			return err
		}
	}

	return nil
}

// createServerPlan creates the server upgrade plan
func (m *Manager) createServerPlan(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	plan := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "upgrade.cattle.io/v1",
			"kind":       "Plan",
			"metadata": map[string]interface{}{
				"name":      "k3s-server",
				"namespace": SystemUpgradeNamespace,
				"labels": map[string]interface{}{
					"pipeops.io/managed": "true",
				},
			},
			"spec": m.buildPlanSpec(true),
		},
	}

	_, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Create(ctx, plan, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	m.logger.Info("[UPGRADE] Created k3s-server upgrade plan")
	return nil
}

// createAgentPlan creates the agent upgrade plan
func (m *Manager) createAgentPlan(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	plan := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "upgrade.cattle.io/v1",
			"kind":       "Plan",
			"metadata": map[string]interface{}{
				"name":      "k3s-agent",
				"namespace": SystemUpgradeNamespace,
				"labels": map[string]interface{}{
					"pipeops.io/managed": "true",
				},
			},
			"spec": m.buildPlanSpec(false),
		},
	}

	_, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Create(ctx, plan, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	m.logger.Info("[UPGRADE] Created k3s-agent upgrade plan")
	return nil
}

// buildPlanSpec builds the plan spec based on configuration
func (m *Manager) buildPlanSpec(isServer bool) map[string]interface{} {
	spec := map[string]interface{}{
		"concurrency":        m.config.Concurrency,
		"cordon":             true,
		"serviceAccountName": "system-upgrade",
		"upgrade": map[string]interface{}{
			"image": "rancher/k3s-upgrade",
		},
	}

	// Set channel or version
	if m.config.Version != "" {
		spec["version"] = m.config.Version
	} else {
		spec["channel"] = m.getChannelURL()
	}

	// Set node selector
	if isServer {
		spec["nodeSelector"] = map[string]interface{}{
			"matchExpressions": []interface{}{
				map[string]interface{}{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "In",
					"values":   []interface{}{"true"},
				},
			},
		}
	} else {
		spec["nodeSelector"] = map[string]interface{}{
			"matchExpressions": []interface{}{
				map[string]interface{}{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "DoesNotExist",
				},
			},
		}
		// Agent plan waits for server plan
		spec["prepare"] = map[string]interface{}{
			"args":  []interface{}{"prepare", "k3s-server"},
			"image": "rancher/k3s-upgrade",
		}
	}

	// Set upgrade window if configured
	if m.config.Window != nil {
		window := map[string]interface{}{}
		if len(m.config.Window.Days) > 0 {
			days := make([]interface{}, len(m.config.Window.Days))
			for i, d := range m.config.Window.Days {
				days[i] = d
			}
			window["days"] = days
		}
		if m.config.Window.StartTime != "" {
			window["startTime"] = m.config.Window.StartTime
		}
		if m.config.Window.EndTime != "" {
			window["endTime"] = m.config.Window.EndTime
		}
		if m.config.Window.TimeZone != "" {
			window["timeZone"] = m.config.Window.TimeZone
		}
		if len(window) > 0 {
			spec["window"] = window
		}
	}

	return spec
}

// getChannelURL returns the K3s release channel URL
func (m *Manager) getChannelURL() string {
	channel := string(m.config.Channel)
	if channel == "" {
		channel = "stable"
	}
	return fmt.Sprintf("https://update.k3s.io/v1-release/channels/%s", channel)
}

// GetStatus returns the current upgrade status
func (m *Manager) GetStatus(ctx context.Context) (*UpgradeStatus, error) {
	status := &UpgradeStatus{
		LastChecked: time.Now(),
	}

	// Check controller installation
	installed, err := m.IsControllerInstalled(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check controller status: %w", err)
	}
	status.ControllerInstalled = installed

	if installed {
		// Check controller readiness
		deployment, err := m.clientset.AppsV1().Deployments(SystemUpgradeNamespace).Get(ctx, "system-upgrade-controller", metav1.GetOptions{})
		if err == nil {
			status.ControllerReady = deployment.Status.ReadyReplicas > 0
		}
	}

	// Get current K3s version
	version, err := m.clientset.Discovery().ServerVersion()
	if err == nil {
		status.CurrentVersion = version.GitVersion
	}

	// Get plan status
	if installed {
		plans, err := m.getPlansStatus(ctx)
		if err == nil {
			status.Plans = plans
		}
	}

	return status, nil
}

// getPlansStatus gets the status of all upgrade plans
func (m *Manager) getPlansStatus(ctx context.Context) ([]PlanStatus, error) {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	list, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var plans []PlanStatus
	for _, item := range list.Items {
		plan := PlanStatus{
			Name: item.GetName(),
		}

		spec, _, _ := unstructured.NestedMap(item.Object, "spec")
		if channel, ok, _ := unstructured.NestedString(spec, "channel"); ok {
			plan.Channel = channel
		}
		if version, ok, _ := unstructured.NestedString(spec, "version"); ok {
			plan.Version = version
		}

		status, _, _ := unstructured.NestedMap(item.Object, "status")
		if latestVersion, ok, _ := unstructured.NestedString(status, "latestVersion"); ok {
			plan.LatestVersion = latestVersion
		}

		plans = append(plans, plan)
	}

	return plans, nil
}

// UpdateConfig updates the upgrade configuration
func (m *Manager) UpdateConfig(config UpgradeConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = config

	// If enabled, ensure plans are updated
	if config.Enabled {
		if err := m.updatePlans(m.ctx); err != nil {
			return fmt.Errorf("failed to update plans: %w", err)
		}
	}

	return nil
}

// updatePlans updates existing plans with new configuration
func (m *Manager) updatePlans(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	// Update server plan
	serverPlan, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Get(ctx, "k3s-server", metav1.GetOptions{})
	if err == nil {
		serverPlan.Object["spec"] = m.buildPlanSpec(true)
		_, err = m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Update(ctx, serverPlan, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update server plan: %w", err)
		}
	}

	// Update agent plan
	agentPlan, err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Get(ctx, "k3s-agent", metav1.GetOptions{})
	if err == nil {
		agentPlan.Object["spec"] = m.buildPlanSpec(false)
		_, err = m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Update(ctx, agentPlan, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update agent plan: %w", err)
		}
	}

	return nil
}

// TriggerUpgrade manually triggers an upgrade to a specific version
func (m *Manager) TriggerUpgrade(ctx context.Context, version string) error {
	if version == "" {
		return fmt.Errorf("version is required")
	}

	// Validate version format
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	m.logger.WithField("version", version).Info("[UPGRADE] Triggering manual upgrade")

	// Update plans with specific version
	originalVersion := m.config.Version
	m.config.Version = version

	if err := m.updatePlans(ctx); err != nil {
		m.config.Version = originalVersion
		return err
	}

	return nil
}

// DeletePlans removes all PipeOps-managed upgrade plans
func (m *Manager) DeletePlans(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    PlanGroup,
		Version:  PlanVersion,
		Resource: PlanResource,
	}

	// Delete server plan
	err := m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Delete(ctx, "k3s-server", metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete server plan: %w", err)
	}

	// Delete agent plan
	err = m.dynamicClient.Resource(gvr).Namespace(SystemUpgradeNamespace).Delete(ctx, "k3s-agent", metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete agent plan: %w", err)
	}

	m.logger.Info("[UPGRADE] Deleted upgrade plans")
	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

func hostPathTypePtr(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}
