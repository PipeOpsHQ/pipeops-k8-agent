package cloud

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes/fake"
)

func TestMapOrgToProvider(t *testing.T) {
	tests := []struct {
		org              string
		expectedProvider Provider
		expectedName     string
		ok               bool
	}{
		{"Amazon.com, Inc.", ProviderAWS, "AWS", true},
		{"AWS Cloud", ProviderAWS, "AWS", true},
		{"Google LLC", ProviderGCP, "Google Cloud", true},
		{"Microsoft Corporation", ProviderAzure, "Azure", true},
		{"DigitalOcean, LLC", ProviderDigitalOcean, "DigitalOcean", true},
		{"Hetzner Online GmbH", ProviderHetzner, "Hetzner Cloud", true},
		{"Linode, LLC", ProviderLinode, "Linode", true},
		{"Akamai Technologies", ProviderLinode, "Linode", true},
		{"Vultr Holdings, LLC", ProviderVultr, "Vultr", true},
		{"Choopa, LLC", ProviderVultr, "Vultr", true},
		{"OVH SAS", ProviderOVH, "OVH", true},
		{"Scaleway", ProviderScaleway, "Scaleway", true},
		{"Online SAS", ProviderScaleway, "Scaleway", true},
		{"Contabo GmbH", ProviderContabo, "Contabo", true},
		{"Nobus Cloud", ProviderNobus, "Nobus Cloud", true},
		{"Some Random ISP", ProviderUnknown, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.org, func(t *testing.T) {
			provider, name, ok := mapOrgToProvider(tt.org)
			if ok != (tt.expectedProvider != ProviderUnknown) && ok != true {
				// Special case for ok check if expectedProvider is ProviderUnknown but we want it false
			}
			
			// Simple check based on tt.ok
			if tt.ok {
				if !ok {
					t.Errorf("mapOrgToProvider(%s) ok = false, want true", tt.org)
				}
				if provider != tt.expectedProvider {
					t.Errorf("mapOrgToProvider(%s) provider = %v, want %v", tt.org, provider, tt.expectedProvider)
				}
				if name != tt.expectedName {
					t.Errorf("mapOrgToProvider(%s) name = %v, want %v", tt.org, name, tt.expectedName)
				}
			} else {
				if ok {
					t.Errorf("mapOrgToProvider(%s) ok = true, want false", tt.org)
				}
			}
		})
	}
}

func TestDetectFromGeoIP(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()

	t.Run("Valid GeoIP with known provider", func(t *testing.T) {
		geoIP := &GeoIPInfo{
			Organization: "DigitalOcean, LLC",
			RegionCode:   "NYC3",
		}
		info, detected := detectFromGeoIP(ctx, clientset, logger, geoIP)
		if !detected {
			t.Fatal("detectFromGeoIP() detected = false, want true")
		}
		if info.Provider != ProviderDigitalOcean {
			t.Errorf("info.Provider = %v, want %v", info.Provider, ProviderDigitalOcean)
		}
		if info.Region != "nyc3" {
			t.Errorf("info.Region = %v, want nyc3", info.Region)
		}
	})

	t.Run("Valid GeoIP with unknown provider", func(t *testing.T) {
		geoIP := &GeoIPInfo{
			Organization: "Local ISP",
		}
		_, detected := detectFromGeoIP(ctx, clientset, logger, geoIP)
		if detected {
			t.Errorf("detectFromGeoIP() detected = true, want false")
		}
	})

	t.Run("Nil GeoIP", func(t *testing.T) {
		_, detected := detectFromGeoIP(ctx, clientset, logger, nil)
		if detected {
			t.Errorf("detectFromGeoIP() detected = true, want false")
		}
	})
}
