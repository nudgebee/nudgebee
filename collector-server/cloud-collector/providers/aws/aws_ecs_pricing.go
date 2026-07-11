package aws

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// fargateValidCombo describes a valid Fargate task CPU size (in vCPU) and the set
// of memory sizes (in GB) allowed for that CPU size. Source: AWS Fargate task
// CPU/memory configuration matrix (Linux/X86).
type fargateValidCombo struct {
	vcpu float64
	mems []float64
}

// fargateMemRange builds an inclusive memory option list from minGB to maxGB in
// stepGB increments.
func fargateMemRange(minGB, maxGB, stepGB float64) []float64 {
	mems := []float64{}
	for m := minGB; m <= maxGB+0.001; m += stepGB {
		mems = append(mems, m)
	}
	return mems
}

// fargateCombos holds the supported Fargate CPU/memory combinations in ascending
// CPU order. Static, so it is built once rather than per nearestValidFargate call.
var fargateCombos = []fargateValidCombo{
	{0.25, []float64{0.5, 1, 2}},
	{0.5, []float64{1, 2, 3, 4}},
	{1, fargateMemRange(2, 8, 1)},
	{2, fargateMemRange(4, 16, 1)},
	{4, fargateMemRange(8, 30, 1)},
	{8, fargateMemRange(16, 60, 4)},
	{16, fargateMemRange(32, 120, 8)},
}

// nearestValidFargate snaps a desired vCPU/memory pair to the smallest valid
// Fargate combination that satisfies both (cpu >= reqVCPU and mem >= reqMemGB).
// If the request exceeds the largest combination, the largest is returned.
func nearestValidFargate(reqVCPU, reqMemGB float64) (float64, float64) {
	combos := fargateCombos
	for _, combo := range combos {
		if combo.vcpu < reqVCPU {
			continue
		}
		for _, m := range combo.mems {
			if m >= reqMemGB {
				return combo.vcpu, m
			}
		}
	}
	last := combos[len(combos)-1]
	return last.vcpu, last.mems[len(last.mems)-1]
}

// getFargatePricing returns the on-demand hourly price per vCPU and per GB of
// memory for Fargate in the given region, via the AWS Pricing API. Fargate is
// billed as two separate dimensions (vCPU-hours and GB-hours), so both are
// returned. It reuses the shared getAvailableInstancesFromPricing/getPricingValue
// helpers used by EC2 and RDS.
func getFargatePricing(cfg aws.Config, region string) (vcpuHourly float64, gbHourly float64, err error) {
	// The AWS Pricing API is only served from us-east-1. Copy the config (rather
	// than mutating the shared one) and pin the region so the lookup works
	// regardless of the account's region.
	cfgCopy := cfg.Copy()
	cfgCopy.Region = "us-east-1"

	// The vCPU and GB SKUs do not both carry an operatingSystem attribute, so
	// suppress the default Linux filter (set by getAvailableInstancesFromPricing)
	// by passing an explicit empty value, and select the SKUs by usagetype below.
	products, err := getAvailableInstancesFromPricing(cfgCopy, "AmazonECS", map[string]string{
		"regionCode":      region,
		"operatingSystem": "",
	})
	if err != nil {
		return 0, 0, err
	}

	for _, p := range products {
		product, ok := p["product"].(map[string]any)
		if !ok {
			continue
		}
		attributes, ok := product["attributes"].(map[string]any)
		if !ok {
			continue
		}
		// Skip non-Linux SKUs (e.g. Windows, which carries license-included rates)
		// so they don't overwrite the Linux price we want for savings estimates.
		if os, ok := attributes["operatingSystem"].(string); ok && os != "" && !strings.EqualFold(os, "Linux") {
			continue
		}
		usagetype, _ := attributes["usagetype"].(string)
		switch {
		case strings.Contains(usagetype, "Fargate-vCPU-Hours"):
			if price, perr := getPricingValue(p); perr == nil && price > 0 && (vcpuHourly == 0 || price < vcpuHourly) {
				vcpuHourly = price
			}
		case strings.Contains(usagetype, "Fargate-GB-Hours"):
			if price, perr := getPricingValue(p); perr == nil && price > 0 && (gbHourly == 0 || price < gbHourly) {
				gbHourly = price
			}
		}
	}

	if vcpuHourly <= 0 || gbHourly <= 0 {
		return vcpuHourly, gbHourly, fmt.Errorf("fargate pricing not found for region %s (vcpu=%.5f, gb=%.5f)", region, vcpuHourly, gbHourly)
	}
	return vcpuHourly, gbHourly, nil
}

// fargateTaskMonthlyCost returns the estimated monthly cost (USD) for the given
// task size and desired count, using 24h * 30d.
func fargateTaskMonthlyCost(vcpu, memGB, vcpuHourly, gbHourly, desiredCount float64) float64 {
	return (vcpu*vcpuHourly + memGB*gbHourly) * 24 * 30 * desiredCount
}
