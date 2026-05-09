package application

import (
	"log/slog"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeploymentCompare(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)

	application := ApplicationRequest{
		Name:      "app-dev",
		Namespace: "nudgebee",
		Kind:      "deployment",
	}
	request := ApplicationDeploymentCompareRequest{
		AccountId:    "0053b816-4b45-4dcd-a612-19545110f8aa",
		Applications: []ApplicationRequest{application},
	}
	insights, err := CompareApplicationDeployment(ctxt, request)

	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
	if len(insights) != 1 {
		t.Errorf("Test case 1 failed")
	}
	insight := insights[0]
	if insight.Name != "app-dev" {
		t.Errorf("Test case 1 failed")
	}
}

func TestApplicationMetrics(t *testing.T) {

	// test case 1
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	metrics, err := GetApplicationMetrics(ctxt, ApplicationMetricsRequest{
		Applications: []ApplicationRequest{
			{
				Name:      "app-dev",
				Namespace: "nudgebee",
			},
			{
				Name:      "auto-pilot-server",
				Namespace: "nudgebee",
			},
		},
		AccountId: "0053b816-4b45-4dcd-a612-19545110f8aa",
	})
	if err != nil {
		println(err)
		t.Errorf("Test case 1 failed")
	}
	if len(metrics) != 2 {
		t.Errorf("Test case 1 failed")
	}
}

func TestProfile(t *testing.T) {

	base64Profile := "H4sIAAAAAAAA/5Lv5mAAAZb/DFcZtP573N0/49nbLw/mSHCxcDAKMHGxcDALsAS8m3vpg3kUmJ3QsPYRi5QAB6NAQ0MDk0TDggecGqwGbFIiHEwCDQveHHj3X16i4QGY1mCXEuFgFmg48P//f7AomNbgMGIwYi9OzC3ISS02Yk3OL80rMWJOLig14s5LzMsvTk3Oz0spNuLVTywo0C9OLSrLTE4tNtKwNEsxSEkzTTQ1TDI3Tk40sEg0NjExTrNMMUgyMzM3Tks2MTc3NDFMM2KLLkspzo814owuLSjKT0otjmVkYPj/v3vpY/OrDAwMAAAAAP//AQAA///tuhdV8QAAAA=="

	svgFile, err := Base64PprofToSVGGz(*slog.Default(), base64Profile)
	assert.Nil(t, err)
	assert.NotNil(t, svgFile)
}
