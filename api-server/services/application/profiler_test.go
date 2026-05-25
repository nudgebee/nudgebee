package application

import (
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProfiler(t *testing.T) {
	// Create a new request context
	ctx := security.NewRequestContextForUserTenant("30b9833e-f667-4b0b-b2c1-065169968e24", "890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)

	// Create a new application profile request
	request := ApplicationProfileRequest{
		AccountId:       "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		ProfileDuration: 30,
		ProfileType:     "cpu",
		PodName:         "services-server-68555f876f-6cnhr",
		Namespace:       "nudgebee",
	}

	// Call the AppplicationProfile function
	response, err := AppplicationProfile(ctx, request)

	// Check for errors
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check the response
	if response == nil {
		t.Fatal("expected response, got nil")
		return
	}

	GetApplicationProfileRequest := &GetApplicationProfileRequest{
		AccountId:     request.AccountId,
		ProfileTaskId: response.ProfileTaskId,
	}

	status := "TODO"

	// loop till status is completed
	var profileStatusResponse *ApplicationProfileResponse
	for status != "completed" {
		profileStatusResponse, err = GetProfileStatus(ctx, GetApplicationProfileRequest)
		assert.Nil(t, err)
		assert.NotNil(t, response)
		// sleep for 30 seconds
		status = string(profileStatusResponse.Status)
		if status == "completed" {
			break
		}
		time.Sleep(30 * time.Second)
	}
	time.Sleep(30 * time.Second)
	if profileStatusResponse.Profile == nil {
		t.Fatal("expected profile, got nil")
	}
}

func TestGetProfileStatus(t *testing.T) {
	// Create a new request context
	ctx := security.NewRequestContextForUserTenant("30b9833e-f667-4b0b-b2c1-065169968e24", "890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)

	// Create a new application profile request
	request := &GetApplicationProfileRequest{
		AccountId:     "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		ProfileTaskId: "3cd07e13-4c74-4fed-8c92-ac036291e410",
	}

	// Call the GetProfileStatus function
	response, err := GetProfileStatus(ctx, request)

	// Check for errors
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check the response
	if response == nil {
		t.Fatal("expected response, got nil")
	}
}

func TestFailedGetProfileDStatus(t *testing.T) {
	// Create a new request context
	ctx := security.NewRequestContextForUserTenant("30b9833e-f667-4b0b-b2c1-065169968e24", "890cad87-c452-4aa7-b84a-742cee0454a1", nil, nil, nil)

	// Create a new application profile request
	request := &GetApplicationProfileRequest{
		AccountId:     "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		ProfileTaskId: "96a05ebd-d7ba-476a-b773-c6ae768f5187",
	}

	// Call the GetProfileStatus function
	response, err := GetProfileStatus(ctx, request)

	// Check for errors
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check the response
	if response == nil {
		t.Fatal("expected response, got nil")
	}
}
