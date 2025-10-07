# Next Steps After PipeOps Agent Setup

Congratulations! Your k3s cluster with the PipeOps Agent is now running. This guide will help you make the most of your new Kubernetes infrastructure.

## Table of Contents

- [Immediate Actions](#immediate-actions)
- [Deploy Your First Application](#deploy-your-first-application)
- [Set Up CI/CD Pipelines](#set-up-cicd-pipelines)
- [Configure Monitoring and Observability](#configure-monitoring-and-observability)
- [Implement Security Best Practices](#implement-security-best-practices)
- [Scale Your Infrastructure](#scale-your-infrastructure)
- [Disaster Recovery Setup](#disaster-recovery-setup)
- [Cost Optimization](#cost-optimization)
- [Advanced Features](#advanced-features)
- [Production Readiness Checklist](#production-readiness-checklist)

---

## Immediate Actions

### 1. Verify Installation

First, ensure everything is working correctly:

```bash
# Check all nodes are ready
kubectl get nodes

# Verify PipeOps agent is running
kubectl get pods -n pipeops-system

# Test agent connectivity
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system &
curl http://localhost:8080/health
curl http://localhost:8080/api/health/detailed | jq
```

### 2. Access the Real-Time Dashboard

Open the agent dashboard to see your cluster in real-time:

```bash
# Port forward the dashboard
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system

# Open in browser (macOS)
open http://localhost:8080/dashboard

# Or for Linux
xdg-open http://localhost:8080/dashboard
```

### 3. Configure kubectl for Easy Access

Add an alias for quick access:

```bash
# Add to ~/.zshrc or ~/.bashrc
echo "alias k='kubectl'" >> ~/.zshrc
echo "alias kgp='kubectl get pods'" >> ~/.zshrc
echo "alias kgs='kubectl get services'" >> ~/.zshrc
echo "alias kgn='kubectl get nodes'" >> ~/.zshrc
echo "alias kpf='kubectl port-forward'" >> ~/.zshrc

# Reload shell
source ~/.zshrc
```

### 4. Connect to PipeOps Dashboard

Log into your PipeOps Control Plane:

1. Go to [https://dashboard.pipeops.io](https://dashboard.pipeops.io)
2. Navigate to **Clusters** section
3. Verify your cluster appears and shows "Connected"
4. Explore real-time metrics and logs

---

## Deploy Your First Application

### Simple Nginx Deployment

```bash
# Create a namespace for your apps
kubectl create namespace demo-apps

# Deploy nginx
kubectl create deployment nginx \
  --image=nginx:latest \
  --replicas=3 \
  --namespace=demo-apps

# Expose as a service
kubectl expose deployment nginx \
  --port=80 \
  --type=LoadBalancer \
  --namespace=demo-apps

# Check status
kubectl get all -n demo-apps

# Get the service URL
kubectl get svc nginx -n demo-apps
```

### Deploy a Sample Application with Ingress

```bash
# Create deployment
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello-app
  namespace: demo-apps
spec:
  replicas: 3
  selector:
    matchLabels:
      app: hello
  template:
    metadata:
      labels:
        app: hello
    spec:
      containers:
      - name: hello
        image: gcr.io/google-samples/hello-app:1.0
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: hello-service
  namespace: demo-apps
spec:
  selector:
    app: hello
  ports:
  - port: 80
    targetPort: 8080
  type: ClusterIP
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello-ingress
  namespace: demo-apps
spec:
  rules:
  - host: hello.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: hello-service
            port:
              number: 80
EOF
```

### Deploy a Database (PostgreSQL)

```bash
# Create StatefulSet for PostgreSQL
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: postgres-secret
  namespace: demo-apps
type: Opaque
stringData:
  POSTGRES_PASSWORD: "yourpassword"
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-pvc
  namespace: demo-apps
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
  namespace: demo-apps
spec:
  serviceName: postgres
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
      - name: postgres
        image: postgres:15-alpine
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-secret
              key: POSTGRES_PASSWORD
        volumeMounts:
        - name: postgres-storage
          mountPath: /var/lib/postgresql/data
      volumes:
      - name: postgres-storage
        persistentVolumeClaim:
          claimName: postgres-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: demo-apps
spec:
  selector:
    app: postgres
  ports:
  - port: 5432
    targetPort: 5432
  clusterIP: None
EOF
```

---

## Set Up CI/CD Pipelines

### Configure GitHub Actions

Create `.github/workflows/deploy.yml`:

```yaml
name: Deploy to PipeOps

on:
  push:
    branches: [ main ]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v3
    
    - name: Build Docker Image
      run: |
        docker build -t myapp:${{ github.sha }} .
        
    - name: Push to Registry
      run: |
        echo ${{ secrets.DOCKER_PASSWORD }} | docker login -u ${{ secrets.DOCKER_USERNAME }} --password-stdin
        docker push myapp:${{ github.sha }}
        
    - name: Deploy via PipeOps API
      run: |
        curl -X POST https://api.pipeops.io/v1/deployments \
          -H "Authorization: Bearer ${{ secrets.PIPEOPS_TOKEN }}" \
          -H "Content-Type: application/json" \
          -d '{
            "cluster": "${{ secrets.CLUSTER_NAME }}",
            "namespace": "production",
            "deployment": "myapp",
            "image": "myapp:${{ github.sha }}"
          }'
```

### GitLab CI/CD

Create `.gitlab-ci.yml`:

```yaml
stages:
  - build
  - deploy

build:
  stage: build
  image: docker:latest
  services:
    - docker:dind
  script:
    - docker build -t $CI_REGISTRY_IMAGE:$CI_COMMIT_SHA .
    - docker push $CI_REGISTRY_IMAGE:$CI_COMMIT_SHA

deploy:
  stage: deploy
  image: bitnami/kubectl:latest
  script:
    - kubectl config use-context pipeops-cluster
    - kubectl set image deployment/myapp myapp=$CI_REGISTRY_IMAGE:$CI_COMMIT_SHA -n production
    - kubectl rollout status deployment/myapp -n production
```

### ArgoCD for GitOps

```bash
# Install ArgoCD
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# Access ArgoCD UI
kubectl port-forward svc/argocd-server -n argocd 8080:443

# Get admin password
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" | base64 -d

# Create application
cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: myapp
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/yourorg/yourrepo
    targetRevision: HEAD
    path: k8s
  destination:
    server: https://kubernetes.default.svc
    namespace: production
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
EOF
```

---

## Configure Monitoring and Observability

### Install Prometheus and Grafana

```bash
# Add Helm repo
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# Install Prometheus + Grafana stack
helm install monitoring prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set prometheus.prometheusSpec.retention=30d \
  --set prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage=50Gi

# Access Grafana
kubectl port-forward svc/monitoring-grafana 3000:80 -n monitoring
# Default credentials: admin / prom-operator

# Access Prometheus
kubectl port-forward svc/monitoring-kube-prometheus-prometheus 9090:9090 -n monitoring
```

### Set Up Loki for Log Aggregation

```bash
# Add Loki repo
helm repo add grafana https://grafana.github.io/helm-charts

# Install Loki
helm install loki grafana/loki-stack \
  --namespace monitoring \
  --set grafana.enabled=false \
  --set prometheus.enabled=false

# Configure Grafana to use Loki
# Add data source in Grafana UI: http://loki:3100
```

### Configure Alerts

```bash
# Create AlertManager configuration
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: alertmanager-config
  namespace: monitoring
data:
  alertmanager.yml: |
    global:
      slack_api_url: 'YOUR_SLACK_WEBHOOK_URL'
    
    route:
      group_by: ['alertname', 'cluster', 'service']
      group_wait: 10s
      group_interval: 10s
      repeat_interval: 12h
      receiver: 'slack-notifications'
    
    receivers:
    - name: 'slack-notifications'
      slack_configs:
      - channel: '#alerts'
        title: 'Kubernetes Alert'
        text: '{{ range .Alerts }}{{ .Annotations.summary }}{{ end }}'
EOF
```

---

## Implement Security Best Practices

### 1. Network Policies

```bash
# Create default deny policy
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: demo-apps
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  - Egress
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-same-namespace
  namespace: demo-apps
spec:
  podSelector: {}
  policyTypes:
  - Ingress
  ingress:
  - from:
    - podSelector: {}
EOF
```

### 2. Pod Security Standards

```bash
# Enable restricted Pod Security Standard
kubectl label namespace demo-apps \
  pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/audit=restricted \
  pod-security.kubernetes.io/warn=restricted
```

### 3. RBAC Configuration

```bash
# Create read-only user
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: readonly-user
  namespace: demo-apps
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: readonly-role
  namespace: demo-apps
rules:
- apiGroups: ["", "apps", "batch"]
  resources: ["*"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: readonly-binding
  namespace: demo-apps
subjects:
- kind: ServiceAccount
  name: readonly-user
  namespace: demo-apps
roleRef:
  kind: Role
  name: readonly-role
  apiGroup: rbac.authorization.k8s.io
EOF
```

### 4. Secrets Management with Sealed Secrets

```bash
# Install sealed-secrets controller
kubectl apply -f https://github.com/bitnami-labs/sealed-secrets/releases/download/v0.24.0/controller.yaml

# Install kubeseal CLI
wget https://github.com/bitnami-labs/sealed-secrets/releases/download/v0.24.0/kubeseal-darwin-amd64 -O kubeseal
chmod +x kubeseal
sudo mv kubeseal /usr/local/bin/

# Create sealed secret
echo -n mypassword | kubectl create secret generic mysecret \
  --dry-run=client \
  --from-file=password=/dev/stdin \
  -o yaml | \
  kubeseal -o yaml > mysealedsecret.yaml

# Apply sealed secret
kubectl apply -f mysealedsecret.yaml
```

### 5. Image Security Scanning

```bash
# Install Trivy operator
helm repo add aqua https://aquasecurity.github.io/helm-charts/
helm install trivy-operator aqua/trivy-operator \
  --namespace trivy-system \
  --create-namespace

# Scan images
kubectl get vulnerabilityreports -A
```

---

## Scale Your Infrastructure

### Add Worker Nodes

```bash
# On master node, get join token
sudo cat /var/lib/rancher/k3s/server/node-token

# On new worker node
export K3S_URL="https://MASTER_IP:6443"
export K3S_TOKEN="YOUR_TOKEN_HERE"
curl -sfL https://get.k3s.io | sh -s - agent

# Verify node joined
kubectl get nodes
```

### Horizontal Pod Autoscaling

```bash
# Install metrics-server (usually included in k3s)
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

# Create HPA
kubectl autoscale deployment nginx \
  --cpu-percent=70 \
  --min=3 \
  --max=10 \
  -n demo-apps

# View HPA status
kubectl get hpa -n demo-apps
```

### Vertical Pod Autoscaling

```bash
# Install VPA
git clone https://github.com/kubernetes/autoscaler.git
cd autoscaler/vertical-pod-autoscaler
./hack/vpa-up.sh

# Create VPA
cat <<EOF | kubectl apply -f -
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: nginx-vpa
  namespace: demo-apps
spec:
  targetRef:
    apiVersion: "apps/v1"
    kind: Deployment
    name: nginx
  updatePolicy:
    updateMode: "Auto"
EOF
```

### Cluster Autoscaling

For cloud environments:

```bash
# AWS example
helm install cluster-autoscaler autoscaler/cluster-autoscaler \
  --namespace kube-system \
  --set autoDiscovery.clusterName=your-cluster \
  --set awsRegion=us-east-1
```

---

## Disaster Recovery Setup

### 1. Automated Etcd Snapshots

```bash
# Configure automatic snapshots (on master node)
sudo systemctl edit --full k3s

# Add snapshot schedule to ExecStart:
# --etcd-snapshot-schedule-cron="0 */6 * * *" \
# --etcd-snapshot-retention=10

sudo systemctl restart k3s

# Verify snapshots
sudo k3s etcd-snapshot ls
```

### 2. Backup Configuration

```bash
# Install Velero for cluster backups
wget https://github.com/vmware-tanzu/velero/releases/download/v1.12.0/velero-v1.12.0-darwin-amd64.tar.gz
tar -xvf velero-v1.12.0-darwin-amd64.tar.gz
sudo mv velero-v1.12.0-darwin-amd64/velero /usr/local/bin/

# Configure with S3-compatible storage
velero install \
  --provider aws \
  --plugins velero/velero-plugin-for-aws:v1.8.0 \
  --bucket your-backup-bucket \
  --secret-file ./credentials-velero \
  --backup-location-config region=us-east-1 \
  --use-volume-snapshots=false

# Create backup schedule
velero schedule create daily-backup --schedule="0 2 * * *"
```

### 3. Test Disaster Recovery

```bash
# Create test backup
velero backup create test-backup

# Simulate disaster by deleting namespace
kubectl delete namespace demo-apps

# Restore from backup
velero restore create --from-backup test-backup

# Verify restoration
kubectl get all -n demo-apps
```

---

## Cost Optimization

### Resource Requests and Limits

```bash
# Analyze current usage
kubectl top pods -A
kubectl top nodes

# Set appropriate limits
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ResourceQuota
metadata:
  name: compute-quota
  namespace: demo-apps
spec:
  hard:
    requests.cpu: "10"
    requests.memory: 20Gi
    limits.cpu: "20"
    limits.memory: 40Gi
EOF
```

### Use Spot Instances (Cloud)

```bash
# AWS: Label nodes
kubectl label nodes <node-name> node.kubernetes.io/instance-type=spot

# Configure tolerations for spot instances
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: batch-job
spec:
  template:
    spec:
      tolerations:
      - key: "node.kubernetes.io/instance-type"
        operator: "Equal"
        value: "spot"
        effect: "NoSchedule"
      nodeSelector:
        node.kubernetes.io/instance-type: spot
EOF
```

### Optimize Storage

```bash
# Set up dynamic provisioning
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: fast-ssd
provisioner: local-path
parameters:
  type: pd-ssd
volumeBindingMode: WaitForFirstConsumer
EOF
```

---

## Advanced Features

### Service Mesh with Istio

```bash
# Install Istio
curl -L https://istio.io/downloadIstio | sh -
cd istio-*
export PATH=$PWD/bin:$PATH
istioctl install --set profile=demo -y

# Enable sidecar injection
kubectl label namespace demo-apps istio-injection=enabled

# Deploy sample app with Istio
kubectl apply -f samples/bookinfo/platform/kube/bookinfo.yaml -n demo-apps
```

### Serverless with Knative

```bash
# Install Knative Serving
kubectl apply -f https://github.com/knative/serving/releases/download/knative-v1.11.0/serving-crds.yaml
kubectl apply -f https://github.com/knative/serving/releases/download/knative-v1.11.0/serving-core.yaml

# Deploy serverless function
cat <<EOF | kubectl apply -f -
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: hello
  namespace: demo-apps
spec:
  template:
    spec:
      containers:
      - image: gcr.io/knative-samples/helloworld-go
        env:
        - name: TARGET
          value: "Knative"
EOF
```

### Machine Learning with Kubeflow

```bash
# Install Kubeflow
export PIPELINE_VERSION=1.8.5
kubectl apply -k "github.com/kubeflow/pipelines/manifests/kustomize/cluster-scoped-resources?ref=$PIPELINE_VERSION"
kubectl wait --for condition=established --timeout=60s crd/applications.app.k8s.io
kubectl apply -k "github.com/kubeflow/pipelines/manifests/kustomize/env/platform-agnostic-pns?ref=$PIPELINE_VERSION"
```

---

## Production Readiness Checklist

Before going to production, ensure you've completed:

### Infrastructure

- Multi-node cluster with odd number of control planes (3 or 5)
- Worker nodes in multiple availability zones
- Load balancer configured for ingress
- DNS properly configured
- TLS certificates set up (Let's Encrypt recommended)
- Network policies implemented
- Resource quotas configured per namespace

### Security

- RBAC properly configured
- Pod Security Standards enforced
- Network policies restricting traffic
- Secrets encrypted at rest
- Image scanning implemented
- Regular security audits scheduled
- Vulnerability management process in place

### Monitoring

- Prometheus collecting metrics
- Grafana dashboards configured
- Log aggregation with Loki/ELK
- Alerts configured for critical issues
- On-call rotation established
- Runbooks documented

### Backup and Recovery

- Automated etcd snapshots (every 6 hours minimum)
- Application backups with Velero
- Disaster recovery tested monthly
- RTO and RPO defined and tested
- Backup retention policy documented

### CI/CD

- Automated deployment pipeline
- Blue-green or canary deployments
- Automated testing in pipeline
- Rollback procedures documented and tested
- GitOps implemented (ArgoCD/Flux)

### Performance

- Resource requests and limits set
- Horizontal Pod Autoscaling configured
- Load testing performed
- Performance benchmarks established
- CDN configured for static assets

### Documentation

- Architecture documented
- Deployment procedures documented
- Troubleshooting guides created
- Contact information for escalations
- Change management process defined

---

## Learning Resources

### Official Documentation

- [Kubernetes Documentation](https://kubernetes.io/docs/)
- [K3s Documentation](https://docs.k3s.io/)
- [PipeOps Documentation](https://docs.pipeops.io/)

### Training

- [Kubernetes Basics (Free)](https://kubernetes.io/docs/tutorials/kubernetes-basics/)
- [CNCF Kubernetes Certifications](https://www.cncf.io/certification/)
- [KodeKloud Kubernetes Courses](https://kodekloud.com/)

### Community

- [Kubernetes Slack](https://kubernetes.slack.com/)
- [PipeOps Community](https://community.pipeops.io/)
- [CNCF Meetups](https://www.meetup.com/pro/cncf/)

---

## Getting Support

### PipeOps Support

- **Documentation**: <https://docs.pipeops.io>
- **GitHub Issues**: <https://github.com/PipeOpsHQ/pipeops-k8-agent/issues>
- **Email Support**: <support@pipeops.io>
- **Community Slack**: <https://pipeops.io/slack>

### Emergency Support

For critical production issues:

1. Check agent status: `kubectl logs -f deployment/pipeops-agent -n pipeops-system`
2. View cluster events: `kubectl get events -A --sort-by='.lastTimestamp'`
3. Contact PipeOps support with:
   - Cluster ID
   - Error messages
   - Recent changes
   - Impact assessment

---

**You're all set!** Your Kubernetes journey with PipeOps has just begun. Happy deploying!
