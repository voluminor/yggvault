package route

import "testing"

// // // // // // // // // //

func TestIsMetrics(t *testing.T) {
	caseArr := []struct {
		pathText string
		want     bool
	}{
		{Metrics, true},
		{MetricsInternal, true},
		{Metrics + "/unknown", true},
		{Metrics + "x/core", false},
		{Health, false},
	}
	for _, caseObj := range caseArr {
		if got := IsMetrics(caseObj.pathText); got != caseObj.want {
			t.Fatalf("IsMetrics(%q)=%v want %v", caseObj.pathText, got, caseObj.want)
		}
	}
}

func TestIsService(t *testing.T) {
	caseArr := []struct {
		pathText string
		want     bool
	}{
		{Health, true},
		{Info, true},
		{OpenAPI, true},
		{Metrics, true},
		{MetricsInternal, true},
		{"/catalog.json", false},
		{"/healthz", false},
		{"/openapi.jsonx", false},
	}
	for _, caseObj := range caseArr {
		if got := IsService(caseObj.pathText); got != caseObj.want {
			t.Fatalf("IsService(%q)=%v want %v", caseObj.pathText, got, caseObj.want)
		}
	}
}
