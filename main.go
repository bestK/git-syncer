// main.go
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-co-op/gocron"
	"gopkg.in/yaml.v2"
)

// Config 定义配置文件结构
type Config struct {
	Users    []User          `yaml:"users"`
	Webhooks []WebhookConfig `yaml:"webhooks"`
}

// User 定义用户配置
type User struct {
	Username    string `yaml:"username"`
	Email       string `yaml:"email"`
	SshKeyPath  string `yaml:"ssh_key_path,omitempty"`
	Jobs        []Job  `yaml:"jobs"`
	GitUsername string `yaml:"git_username,omitempty"`
	GitPassword string `yaml:"git_password,omitempty"`
}

// Job 定义单个同步任务的配置
type Job struct {
	Name       string   `yaml:"name"`
	Schedule   string   `yaml:"schedule"`
	SourcePath string   `yaml:"source_path"`
	RepoPath   string   `yaml:"repo_path"`
	RemoteURL  string   `yaml:"remote_url,omitempty"`
	Includes   []string `yaml:"includes,omitempty"`
	Excludes   []string `yaml:"excludes,omitempty"`
	Webhooks   []string `yaml:"webhooks,omitempty"`
}

// GitSync 同步器结构
type GitSync struct {
	config         *Config
	scheduler      *gocron.Scheduler
	logger         *log.Logger
	webhookManager *WebhookManager
}

// NewGitSync 创建新的同步器实例
func NewGitSync(configPath string) (*GitSync, error) {
	// 创建日志记录器
	logFile, err := os.OpenFile("git_sync.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	multiWriter := io.MultiWriter(os.Stdout, logFile)
	logger := log.New(multiWriter, "", log.LstdFlags)

	// 加载配置
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// 创建调度器
	scheduler := gocron.NewScheduler(time.Local)

	webhookManager := NewWebhookManager(logger)

	gs := &GitSync{
		config:         config,
		scheduler:      scheduler,
		logger:         logger,
		webhookManager: webhookManager,
	}

	// 注册全局 webhook
	for _, webhook := range config.Webhooks {
		if err := gs.webhookManager.RegisterWebhook(webhook); err != nil {
			return nil, fmt.Errorf("failed to register webhook %s: %v", webhook.Name, err)
		}
	}

	return gs, nil
}

// loadConfig 从YAML文件加载配置
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// Run 启动同步服务
func (gs *GitSync) Run() error {
	gs.logger.Println("Starting Git sync service...")

	// 为每个用户设置任务
	for _, user := range gs.config.Users {
		// 设置用户的Git配置
		if err := gs.setupUserGitConfig(&user); err != nil {
			gs.logger.Printf("Failed to setup git config for user %s: %v\n", user.Username, err)
			continue
		}

		// 设置用户的所有任务
		for _, job := range user.Jobs {
			// 创建任务的闭包以保留user和job变量
			userCopy := user
			jobCopy := job
			_, err := gs.scheduler.Cron(job.Schedule).Do(func() {
				gs.syncJob(&userCopy, &jobCopy)
			})
			if err != nil {
				gs.logger.Printf("Failed to schedule job %s for user %s: %v\n", job.Name, user.Username, err)
				continue
			}
			gs.logger.Printf("Scheduled job: %s for user: %s with schedule: %s\n", job.Name, user.Username, job.Schedule)
		}
	}

	// 启动调度器
	gs.scheduler.StartBlocking()
	return nil
}

// setupUserGitConfig 设置用户的Git配置
func (gs *GitSync) setupUserGitConfig(user *User) error {
	// 设置全局Git配置
	commands := []struct {
		name  string
		args  []string
		fatal bool
	}{
		{"git", []string{"config", "--global", "user.name", user.Username}, true},
		{"git", []string{"config", "--global", "user.email", user.Email}, true},
	}

	// 如果提供了SSH密钥，确保其权限正确
	if user.SshKeyPath != "" {
		if err := os.Chmod(user.SshKeyPath, 0600); err != nil {
			return fmt.Errorf("failed to set SSH key permissions: %v", err)
		}
	}

	// 执行Git配置命令
	for _, cmd := range commands {
		command := exec.Command(cmd.name, cmd.args...)
		if err := command.Run(); err != nil && cmd.fatal {
			return fmt.Errorf("failed to execute git command %v: %v", cmd.args, err)
		}
	}

	return nil
}

// syncJob 执行单个同步任务
func (gs *GitSync) syncJob(user *User, job *Job) {
	startTime := time.Now()
	ctx := WebhookContext{
		User:      *user,
		Job:       *job,
		StartTime: startTime.Format(time.RFC3339),
	}

	var syncErr error
	defer func() {
		endTime := time.Now()
		ctx.EndTime = endTime.Format(time.RFC3339)
		ctx.Duration = endTime.Sub(startTime).String()

		if syncErr != nil {
			ctx.Status = "failure"
			ctx.Error = syncErr
		} else {
			ctx.Status = "success"
		}

		// 执行任务的 webhook
		if len(job.Webhooks) > 0 {
			webhookConfigs := gs.webhookManager.GetWebhooksByNames(job.Webhooks)
			if err := gs.webhookManager.ExecuteWebhooks(webhookConfigs, ctx); err != nil {
				gs.logger.Printf("Failed to execute job webhooks: %v", err)
			}
		}
	}()

	gs.logger.Printf("Starting sync job: %s for user: %s\n", job.Name, user.Username)

	// 确保目标仓库存在并配置
	if err := gs.initRepo(user, job); err != nil {
		gs.logger.Printf("Failed to init repository for job %s: %v\n", job.Name, err)
		return
	}

	// 同步文件
	if err := gs.syncFiles(job); err != nil {
		gs.logger.Printf("Failed to sync files for job %s: %v\n", job.Name, err)
		return
	}

	// 提交更改
	if err := gs.commitChanges(user, job); err != nil {
		gs.logger.Printf("Failed to commit changes for job %s: %v\n", job.Name, err)
		return
	}

	gs.logger.Printf("Completed sync job: %s for user: %s\n", job.Name, user.Username)
}

// initRepo 初始化或检查Git仓库
func (gs *GitSync) initRepo(user *User, job *Job) error {
	repoDir := filepath.Join(job.RepoPath)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		// 创建目录
		if err := os.MkdirAll(repoDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}

		// 初始化新仓库
		cmd := exec.Command("git", "init")
		cmd.Dir = repoDir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git init failed: %v", err)
		}

		// 如果配置了远程仓库，添加远程源
		if job.RemoteURL != "" {
			remoteURL := job.RemoteURL
			// 如果提供了用户名和密码，将其添加到URL中
			if user.GitUsername != "" && user.GitPassword != "" {
				remoteURL = addCredentialsToURL(job.RemoteURL, user.GitUsername, user.GitPassword)
			}

			cmd = exec.Command("git", "remote", "add", "origin", remoteURL)
			cmd.Dir = repoDir
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to add remote: %v", err)
			}
		}
	}
	return nil
}

// addCredentialsToURL 向Git URL添加认证信息
func addCredentialsToURL(url, username, password string) string {
	if strings.HasPrefix(url, "https://") {
		return fmt.Sprintf("https://%s:%s@%s", username, password, strings.TrimPrefix(url, "https://"))
	}
	return url
}

// syncFiles 同步文件
func (gs *GitSync) syncFiles(job *Job) error {
	return filepath.Walk(job.SourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		// 获取相对路径
		relPath, err := filepath.Rel(job.SourcePath, path)
		if err != nil {
			return err
		}

		// 检查文件是否应该被同步
		if !gs.shouldSync(relPath, job.Includes, job.Excludes) {
			return nil
		}

		// 复制文件
		destPath := filepath.Join(job.RepoPath, relPath)
		if err := gs.copyFile(path, destPath); err != nil {
			return err
		}

		gs.logger.Printf("Synced file: %s\n", relPath)
		return nil
	})
}

// shouldSync 检查文件是否应该被同步
func (gs *GitSync) shouldSync(path string, includes, excludes []string) bool {
	// 首先检查排除规则
	for _, exclude := range excludes {
		matched, err := filepath.Match(exclude, path)
		if err == nil && matched {
			return false
		}
	}

	// 如果没有包含规则，则包含所有文件
	if len(includes) == 0 {
		return true
	}

	// 检查包含规则
	for _, include := range includes {
		matched, err := filepath.Match(include, path)
		if err == nil && matched {
			return true
		}
	}

	return false
}

// copyFile 复制文件
func (gs *GitSync) copyFile(src, dst string) error {
	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// 复制文件
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// commitChanges 提交更改并推送到远程
func (gs *GitSync) commitChanges(user *User, job *Job) error {
	// 检查是否有更改
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = job.RepoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check git status: %v", err)
	}

	if len(strings.TrimSpace(string(output))) == 0 {
		gs.logger.Println("No changes to commit")
		return nil
	}

	// 添加所有更改
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = job.RepoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add failed: %v", err)
	}

	// 提交更改
	commitMsg := fmt.Sprintf("Sync update by %s: %s", user.Username, time.Now().Format("2006-01-02 15:04:05"))
	cmd = exec.Command("git", "commit", "-m", commitMsg)
	cmd.Dir = job.RepoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit failed: %v", err)
	}

	// 如果配置了远程仓库，推送更改
	if job.RemoteURL != "" {
		cmd = exec.Command("git", "push", "origin", "master")
		cmd.Dir = job.RepoPath
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git push failed: %v", err)
		}
		gs.logger.Println("Changes pushed to remote repository")
	}

	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: git-sync <config_file>")
		os.Exit(1)
	}

	sync, err := NewGitSync(os.Args[1])
	if err != nil {
		log.Fatalf("Failed to create GitSync: %v", err)
	}

	if err := sync.Run(); err != nil {
		log.Fatalf("Failed to run GitSync: %v", err)
	}
}
