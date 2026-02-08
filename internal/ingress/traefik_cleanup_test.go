package ingress

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestCleanupStaleNginxAdmissionWebhooks_FallbackToPatchWhenDeleteForbidden(t *testing.T) {
	fail := admissionregistrationv1.Fail

	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ingress-nginx-admission",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "validate.nginx.ingress.kubernetes.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Name:      "ingress-nginx-controller-admission",
						Namespace: "ingress-nginx",
					},
				},
				FailurePolicy: &fail,
			},
		},
	}

	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ingress-nginx-admission",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "mutate.nginx.ingress.kubernetes.io",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Name:      "ingress-nginx-controller-admission",
						Namespace: "ingress-nginx",
					},
				},
				FailurePolicy: &fail,
			},
		},
	}

	client := fake.NewSimpleClientset(vwc, mwc)
	client.Fake.PrependReactor("delete", "validatingwebhookconfigurations", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "validatingwebhookconfigurations"},
			"ingress-nginx-admission",
			nil,
		)
	})
	client.Fake.PrependReactor("delete", "mutatingwebhookconfigurations", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "mutatingwebhookconfigurations"},
			"ingress-nginx-admission",
			nil,
		)
	})

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	if err := cleanupStaleNginxAdmissionWebhooksForClient(context.Background(), client, logger); err != nil {
		t.Fatalf("cleanup should succeed with patch fallback, got error: %v", err)
	}

	gotVWC, err := client.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), "ingress-nginx-admission", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected validating webhook config to still exist, got error: %v", err)
	}
	if gotVWC.Webhooks[0].FailurePolicy == nil || *gotVWC.Webhooks[0].FailurePolicy != admissionregistrationv1.Ignore {
		t.Fatalf("expected validating failurePolicy=Ignore, got %+v", gotVWC.Webhooks[0].FailurePolicy)
	}

	gotMWC, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(context.Background(), "ingress-nginx-admission", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected mutating webhook config to still exist, got error: %v", err)
	}
	if gotMWC.Webhooks[0].FailurePolicy == nil || *gotMWC.Webhooks[0].FailurePolicy != admissionregistrationv1.Ignore {
		t.Fatalf("expected mutating failurePolicy=Ignore, got %+v", gotMWC.Webhooks[0].FailurePolicy)
	}
}

func TestCleanupStaleNginxAdmissionWebhooks_RefusesWhenServiceExists(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller-admission",
			Namespace: "ingress-nginx",
		},
	}

	client := fake.NewSimpleClientset(svc)
	logger := logrus.New()

	err := cleanupStaleNginxAdmissionWebhooksForClient(context.Background(), client, logger)
	if err == nil {
		t.Fatal("expected cleanup to refuse when admission service exists")
	}
	if !strings.Contains(err.Error(), "refusing to delete webhook configurations") {
		t.Fatalf("unexpected error: %v", err)
	}
}
