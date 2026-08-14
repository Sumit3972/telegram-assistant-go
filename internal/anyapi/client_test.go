package anyapi

import (
	"context"
	"testing"
	"time"
)

func TestAnyAPISubscribe(t *testing.T) {
	email := "sumitmehta396@gmail.com"
	password := "Sumit3972@gmail"

	client := NewClient(email, password)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.Subscribe(ctx, "developer")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if !result.Success {
		t.Fatalf("Expected success=true, got false")
	}

	if result.Subscription == nil {
		t.Fatalf("Expected subscription details, got nil")
	}

	if result.Subscription.PlanCode != "developer" {
		t.Errorf("Expected planCode developer, got %s", result.Subscription.PlanCode)
	}

	t.Logf("Subscription successfully created/renewed: %+v", result.Subscription)
	t.Logf("User: %+v", result.User)
	t.Logf("AccessToken: %s", result.AccessToken)
}
