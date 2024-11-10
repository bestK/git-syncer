// webhook.go
package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"text/template"
	"time"
)

// WebhookConfig 定义webhook配置
type WebhookConfig struct {
	Name       string            `yaml:"name"`
	URL        string            `yaml:"url"`
	Method     string            `yaml:"method"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	Body       string            `yaml:"body,omitempty"`
	Trigger    string            `yaml:"trigger"` // success, failure, always
	RetryCount int               `yaml:"retry_count,omitempty"`
	RetryDelay int               `yaml:"retry_delay,omitempty"` // seconds
	References []string          `yaml:"references,omitempty"`  // 引用其他webhook的名称
}

// WebhookContext 定义webhook上下文
type WebhookContext struct {
	User         User
	Job          Job
	Status       string // success, failure
	Error        error  // 如果失败，错误信息
	StartTime    string
	EndTime      string
	Duration     string
	ChangedFiles []string
}

// WebhookManager webhook管理器
type WebhookManager struct {
	webhooks map[string]*WebhookConfig
	logger   *log.Logger
}

// NewWebhookManager 创建webhook管理器
func NewWebhookManager(logger *log.Logger) *WebhookManager {
	return &WebhookManager{
		webhooks: make(map[string]*WebhookConfig),
		logger:   logger,
	}
}

// RegisterWebhook 注册webhook
func (wm *WebhookManager) RegisterWebhook(webhook WebhookConfig) error {
	if webhook.Name == "" {
		return fmt.Errorf("webhook name cannot be empty")
	}
	if webhook.URL == "" {
		return fmt.Errorf("webhook URL cannot be empty")
	}
	if webhook.Method == "" {
		webhook.Method = "POST"
	}
	if webhook.Trigger == "" {
		webhook.Trigger = "always"
	}
	if webhook.RetryCount == 0 {
		webhook.RetryCount = 3
	}
	if webhook.RetryDelay == 0 {
		webhook.RetryDelay = 5
	}

	wm.webhooks[webhook.Name] = &webhook
	return nil
}

// ExecuteWebhooks 执行webhook
func (wm *WebhookManager) ExecuteWebhooks(webhooks []WebhookConfig, ctx WebhookContext) error {
	for _, webhook := range webhooks {
		if err := wm.executeWebhookWithReferences(&webhook, ctx, make(map[string]bool)); err != nil {
			wm.logger.Printf("Failed to execute webhook %s: %v", webhook.Name, err)
		}
	}
	return nil
}

// executeWebhookWithReferences 执行webhook及其引用
func (wm *WebhookManager) executeWebhookWithReferences(webhook *WebhookConfig, ctx WebhookContext, executed map[string]bool) error {
	// 检查是否已执行过，防止循环引用
	if executed[webhook.Name] {
		return nil
	}
	executed[webhook.Name] = true

	// 首先执行引用的webhook
	for _, refName := range webhook.References {
		if refWebhook, exists := wm.webhooks[refName]; exists {
			if err := wm.executeWebhookWithReferences(refWebhook, ctx, executed); err != nil {
				return fmt.Errorf("failed to execute referenced webhook %s: %v", refName, err)
			}
		}
	}

	// 根据trigger确定是否执行
	shouldExecute := webhook.Trigger == "always" ||
		(webhook.Trigger == "success" && ctx.Status == "success") ||
		(webhook.Trigger == "failure" && ctx.Status == "failure")

	if !shouldExecute {
		return nil
	}

	return wm.executeWebhook(webhook, ctx)
}

// executeWebhook 执行单个webhook
func (wm *WebhookManager) executeWebhook(webhook *WebhookConfig, ctx WebhookContext) error {
	// 解析body模板
	bodyTemplate, err := template.New("webhook_body").Parse(webhook.Body)
	if err != nil {
		return fmt.Errorf("failed to parse webhook body template: %v", err)
	}

	// 执行模板
	var bodyBuffer bytes.Buffer
	if err := bodyTemplate.Execute(&bodyBuffer, ctx); err != nil {
		return fmt.Errorf("failed to execute webhook body template: %v", err)
	}

	// 重试逻辑
	var lastErr error
	for i := 0; i < webhook.RetryCount; i++ {
		if err := wm.sendWebhookRequest(webhook, bodyBuffer.String()); err != nil {
			lastErr = err
			wm.logger.Printf("Webhook attempt %d failed: %v", i+1, err)
			time.Sleep(time.Duration(webhook.RetryDelay) * time.Second)
			continue
		}
		return nil
	}

	return fmt.Errorf("webhook execution failed after %d attempts: %v", webhook.RetryCount, lastErr)
}

// sendWebhookRequest 发送webhook请求
func (wm *WebhookManager) sendWebhookRequest(webhook *WebhookConfig, body string) error {
	req, err := http.NewRequest(webhook.Method, webhook.URL, bytes.NewBufferString(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	// 设置headers
	for key, value := range webhook.Headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{
		Timeout: time.Second * 30,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook request failed with status code: %d", resp.StatusCode)
	}

	return nil
}

// GetWebhooksByNames 根据名称获取webhook配置
func (wm *WebhookManager) GetWebhooksByNames(names []string) []WebhookConfig {
	var configs []WebhookConfig
	for _, name := range names {
		if config, exists := wm.webhooks[name]; exists {
			configs = append(configs, *config)
		}
	}
	return configs
}
