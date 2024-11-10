package main

import (
	"testing"
)

func TestWebhookManager(t *testing.T) {
	wm := NewWebhookManager(createTestLogger())

	// 测试注册 webhook
	webhook := WebhookConfig{
		Name: "test-hook",
		URL:  "http://example.com/webhook",
	}

	err := wm.RegisterWebhook(webhook)
	if err != nil {
		t.Fatalf("Failed to register webhook: %v", err)
	}

	// 测试获取 webhook
	configs := wm.GetWebhooksByNames([]string{"test-hook"})
	if len(configs) != 1 {
		t.Errorf("Expected 1 webhook config, got %d", len(configs))
	}

	if configs[0].Name != "test-hook" {
		t.Errorf("Expected webhook name 'test-hook', got '%s'", configs[0].Name)
	}
}
