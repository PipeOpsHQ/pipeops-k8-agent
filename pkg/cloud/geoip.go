package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type GeoIPInfo struct {
	IP            string  `json:"ip"`
	City          string  `json:"city"`
	Region        string  `json:"region"`
	RegionCode    string  `json:"region_code"`
	Country       string  `json:"country"`
	CountryCode   string  `json:"country_code"`
	CountryCode3  string  `json:"country_code3"`
	ContinentCode string  `json:"continent_code"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	Timezone      string  `json:"timezone"`
	Organization  string  `json:"org"`
}

type geoIPResponse struct {
	IP          string  `json:"ip"`
	City        string  `json:"city"`
	Region      string  `json:"region"`
	Country     string  `json:"country_name"`
	CountryCode string  `json:"country_code"`
	Continent   string  `json:"continent_code"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Timezone    string  `json:"timezone"`
	Org         string  `json:"org"`
}

const (
	geoIPTimeout = 3 * time.Second
)

// DetectGeoIP detects the geographic location of the agent's public IP
// Uses multiple free GeoIP services with fallbacks
func DetectGeoIP(ctx context.Context, logger *logrus.Logger) *GeoIPInfo {
	// Skip GeoIP in CI environments or when explicitly disabled
	if os.Getenv("SKIP_GEOIP") != "" || os.Getenv("CI") != "" {
		logger.Debug("Skipping GeoIP detection (CI or test environment)")
		return nil
	}

	logger.Debug("Detecting geographic location via GeoIP...")

	// Create a timeout context to prevent hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, geoIPTimeout)
	defer cancel()

	// Try multiple services for redundancy
	services := []func(context.Context, *logrus.Logger) (*GeoIPInfo, error){
		detectViaIPAPI,
		detectViaIPify,
		detectViaIpinfo,
	}

	for i, detectFunc := range services {
		info, err := detectFunc(timeoutCtx, logger)
		if err != nil {
			logger.WithError(err).Debugf("GeoIP service %d failed", i+1)
			continue
		}

		if info != nil {
			logger.WithFields(logrus.Fields{
				"country":   info.Country,
				"continent": info.ContinentCode,
				"ip":        info.IP,
			}).Info("Geographic location detected successfully")
			return info
		}
	}

	logger.Warn("Could not detect geographic location, using default (US)")
	return nil
}

// detectViaIPAPI uses ipapi.co (free, 1000 requests/day, no key required)
func detectViaIPAPI(ctx context.Context, logger *logrus.Logger) (*GeoIPInfo, error) {
	client := &http.Client{Timeout: geoIPTimeout}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://ipapi.co/json/", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data struct {
		IP        string  `json:"ip"`
		City      string  `json:"city"`
		Region    string  `json:"region"`
		Country   string  `json:"country_name"`
		CountryC  string  `json:"country_code"`
		Continent string  `json:"continent_code"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Timezone  string  `json:"timezone"`
		Org       string  `json:"org"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	return &GeoIPInfo{
		IP:            data.IP,
		City:          data.City,
		Region:        data.Region,
		Country:       data.Country,
		CountryCode:   data.CountryC,
		ContinentCode: data.Continent,
		Latitude:      data.Latitude,
		Longitude:     data.Longitude,
		Timezone:      data.Timezone,
		Organization:  data.Org,
	}, nil
}

// detectViaIPify uses ipify + ip-api.com combination (free, no key required)
func detectViaIPify(ctx context.Context, logger *logrus.Logger) (*GeoIPInfo, error) {
	client := &http.Client{Timeout: geoIPTimeout}

	// First get public IP
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org?format=json", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ipData struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ipData); err != nil {
		return nil, err
	}

	if ipData.IP == "" {
		return nil, fmt.Errorf("no IP returned")
	}

	// Then get geo data from ip-api.com (free, 45 requests/minute)
	req, err = http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://ip-api.com/json/%s", ipData.IP), nil)
	if err != nil {
		return nil, err
	}

	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var geoData struct {
		Status      string  `json:"status"`
		Country     string  `json:"country"`
		CountryCode string  `json:"countryCode"`
		Region      string  `json:"region"`
		RegionName  string  `json:"regionName"`
		City        string  `json:"city"`
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
		Timezone    string  `json:"timezone"`
		ISP         string  `json:"isp"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geoData); err != nil {
		return nil, err
	}

	if geoData.Status != "success" {
		return nil, fmt.Errorf("geo lookup failed")
	}

	continent := countryToContinentCode(geoData.CountryCode)

	return &GeoIPInfo{
		IP:            ipData.IP,
		City:          geoData.City,
		Region:        geoData.RegionName,
		RegionCode:    geoData.Region,
		Country:       geoData.Country,
		CountryCode:   geoData.CountryCode,
		ContinentCode: continent,
		Latitude:      geoData.Lat,
		Longitude:     geoData.Lon,
		Timezone:      geoData.Timezone,
		Organization:  geoData.ISP,
	}, nil
}

// detectViaIpinfo uses ipinfo.io (free, 50k requests/month, no key required)
func detectViaIpinfo(ctx context.Context, logger *logrus.Logger) (*GeoIPInfo, error) {
	client := &http.Client{Timeout: geoIPTimeout}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://ipinfo.io/json", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var data struct {
		IP       string `json:"ip"`
		City     string `json:"city"`
		Region   string `json:"region"`
		Country  string `json:"country"`
		Loc      string `json:"loc"`
		Org      string `json:"org"`
		Timezone string `json:"timezone"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var lat, lon float64
	if data.Loc != "" {
		fmt.Sscanf(data.Loc, "%f,%f", &lat, &lon)
	}

	continent := countryToContinentCode(data.Country)

	return &GeoIPInfo{
		IP:            data.IP,
		City:          data.City,
		Region:        data.Region,
		Country:       data.Country,
		CountryCode:   data.Country,
		ContinentCode: continent,
		Latitude:      lat,
		Longitude:     lon,
		Timezone:      data.Timezone,
		Organization:  data.Org,
	}, nil
}

// GetRegistryRegion returns the appropriate registry region (eu/us) based on GeoIP
func (g *GeoIPInfo) GetRegistryRegion() string {
	if g == nil {
		return "us"
	}

	// Use continent code for registry selection
	switch strings.ToUpper(g.ContinentCode) {
	case "EU", "EUROPE":
		return "eu"
	case "AF", "AFRICA":
		return "eu" // Africa typically uses EU registry
	case "AS", "ASIA":
		return "us" // Default to US for now, can add asia registry later
	case "OC", "OCEANIA":
		return "us" // Australia/Pacific uses US registry
	case "NA", "NORTH AMERICA":
		return "us"
	case "SA", "SOUTH AMERICA":
		return "us"
	default:
		return "us"
	}
}

// countryToContinentCode maps country code to continent code
func countryToContinentCode(countryCode string) string {
	// European countries
	europeanCountries := map[string]bool{
		"AT": true, "BE": true, "BG": true, "HR": true, "CY": true,
		"CZ": true, "DK": true, "EE": true, "FI": true, "FR": true,
		"DE": true, "GR": true, "HU": true, "IE": true, "IT": true,
		"LV": true, "LT": true, "LU": true, "MT": true, "NL": true,
		"PL": true, "PT": true, "RO": true, "SK": true, "SI": true,
		"ES": true, "SE": true, "GB": true, "UK": true, "NO": true,
		"CH": true, "IS": true, "AL": true, "BA": true, "BY": true,
		"ME": true, "MK": true, "MD": true, "RS": true, "RU": true,
		"UA": true, "XK": true,
	}

	// Asian countries
	asianCountries := map[string]bool{
		"CN": true, "IN": true, "ID": true, "JP": true, "PK": true,
		"BD": true, "PH": true, "VN": true, "TR": true, "IR": true,
		"TH": true, "MM": true, "KR": true, "IQ": true, "AF": true,
		"SA": true, "UZ": true, "MY": true, "YE": true, "NP": true,
		"KP": true, "TW": true, "SY": true, "LK": true, "KH": true,
		"JO": true, "AZ": true, "TJ": true, "IL": true, "HK": true,
		"LA": true, "SG": true, "LB": true, "KG": true, "TM": true,
		"OM": true, "KW": true, "GE": true, "MN": true, "AM": true,
		"QA": true, "BH": true, "PS": true, "BT": true, "MV": true,
		"BN": true, "MO": true,
	}

	// African countries
	africanCountries := map[string]bool{
		"NG": true, "ET": true, "EG": true, "CD": true, "ZA": true,
		"TZ": true, "KE": true, "UG": true, "DZ": true, "SD": true,
		"MA": true, "AO": true, "GH": true, "MZ": true, "MG": true,
		"CM": true, "CI": true, "NE": true, "BF": true, "ML": true,
		"MW": true, "ZM": true, "SN": true, "SO": true, "TD": true,
		"ZW": true, "GN": true, "RW": true, "BJ": true, "TN": true,
		"BI": true, "SS": true, "TG": true, "SL": true, "LY": true,
		"LR": true, "MR": true, "CF": true, "ER": true, "GM": true,
		"BW": true, "GA": true, "GW": true, "GQ": true, "MU": true,
		"SZ": true, "DJ": true, "RE": true, "KM": true, "CV": true,
		"ST": true, "SC": true,
	}

	// Oceania countries
	oceaniaCountries := map[string]bool{
		"AU": true, "PG": true, "NZ": true, "FJ": true, "SB": true,
		"NC": true, "PF": true, "VU": true, "WS": true, "GU": true,
		"KI": true, "FM": true, "TO": true, "PW": true, "MH": true,
		"NR": true, "TV": true, "NU": true, "CK": true,
	}

	// South American countries
	southAmericanCountries := map[string]bool{
		"BR": true, "CO": true, "AR": true, "PE": true, "VE": true,
		"CL": true, "EC": true, "BO": true, "PY": true, "UY": true,
		"GY": true, "SR": true, "GF": true, "FK": true,
	}

	cc := strings.ToUpper(countryCode)

	if europeanCountries[cc] {
		return "EU"
	}
	if asianCountries[cc] {
		return "AS"
	}
	if africanCountries[cc] {
		return "AF"
	}
	if oceaniaCountries[cc] {
		return "OC"
	}
	if southAmericanCountries[cc] {
		return "SA"
	}

	// Default to North America
	return "NA"
}
