package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"sort"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"golang.org/x/exp/maps"
)

func getPricingValue(currentInstance map[string]interface{}) (float64, error) {
	currentInsatnceCost := 0.0
	terms, ok := currentInstance["terms"].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: terms not found or wrong type")
	}
	onDemand, ok := terms["OnDemand"].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: OnDemand not found or wrong type")
	}
	if len(onDemand) == 0 {
		return 0.0, errors.New("no on demand pricing found")
	}
	firstOnDemandEntry, ok := onDemand[maps.Keys(onDemand)[0]].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: OnDemand entry is wrong type")
	}
	onDemandPriceDimensions, ok := firstOnDemandEntry["priceDimensions"].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: priceDimensions not found or wrong type")
	}
	firstPriceDimension, ok := onDemandPriceDimensions[maps.Keys(onDemandPriceDimensions)[0]].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: price dimension entry is wrong type")
	}
	onDemandPricePerUnit, ok := firstPriceDimension["pricePerUnit"].(map[string]any)
	if !ok {
		return 0.0, fmt.Errorf("missing pricing data: pricePerUnit not found or wrong type")
	}
	usdPrice, ok := onDemandPricePerUnit["USD"].(string)
	if !ok {
		return 0.0, errors.New("no USD pricing found")
	}
	pricePerHour, err := strconv.ParseFloat(usdPrice, 64)
	if err != nil {
		return 0.0, err
	}
	currentInsatnceCost = pricePerHour
	return currentInsatnceCost, nil
}

func alternateInstancesBasedOnPricing(instances []map[string]interface{}, currentInstance map[string]interface{}) ([]map[string]interface{}, error) {
	recommendedInstances := []map[string]interface{}{}
	currentInsatnceCost, err := getPricingValue(currentInstance)
	if err != nil {
		return recommendedInstances, err
	}

	for _, instance := range instances {
		//skip if not current generation
		product, ok := instance["product"].(map[string]any)
		if !ok {
			continue
		}
		attributes, ok := product["attributes"].(map[string]any)
		if !ok {
			continue
		}
		currentGen, ok := attributes["currentGeneration"].(string)
		if !ok || currentGen != "Yes" {
			continue
		}
		//check if cost is low
		p, err := getPricingValue(instance)
		if err != nil {
			continue
		}
		if p < currentInsatnceCost {
			recommendedInstances = append(recommendedInstances, instance)
		}
	}

	sort.Slice(recommendedInstances, func(i, j int) bool {
		p1, _ := getPricingValue(recommendedInstances[i])
		p2, _ := getPricingValue(recommendedInstances[j])
		return p1 < p2
	})

	return recommendedInstances, nil
}

func getAvailableInstancesFromPricing(cfg aws.Config, serviceName string, filtersMap map[string]string) ([]map[string]interface{}, error) {

	svc := pricing.NewFromConfig(cfg)
	filters := []types.Filter{}
	for key, value := range filtersMap {
		// Skip filters with empty values (allows services to explicitly exclude filters)
		if value == "" {
			continue
		}
		filters = append(filters, types.Filter{
			Field: aws.String(key),
			Type:  types.FilterTypeTermMatch,
			Value: aws.String(value),
		})
	}
	// Add a default filter for Linux OS if not provided, as it's common for many services.
	// Services can explicitly exclude this by setting "operatingSystem": ""
	if _, ok := filtersMap["operatingSystem"]; !ok {
		filters = append(filters, types.Filter{
			Field: aws.String("operatingSystem"),
			Type:  types.FilterTypeTermMatch,
			Value: aws.String("Linux"),
		})
	}

	priceList := []map[string]interface{}{}
	paginator := pricing.NewGetProductsPaginator(svc, &pricing.GetProductsInput{
		ServiceCode: aws.String(serviceName),
		Filters:     filters,
	})

	for paginator.HasMorePages() {
		result, err := paginator.NextPage(context.TODO())
		if err != nil {
			return nil, err
		}
		for _, price := range result.PriceList {
			var priceData map[string]interface{}
			err := common.UnmarshalJson([]byte(price), &priceData)
			if err != nil {
				slog.Error("aws: unable to unmarshal price data", "error", err, "serviceName", serviceName)
				continue
			}
			priceList = append(priceList, priceData)
		}
	}
	return priceList, nil
}
