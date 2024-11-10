package main

import (
	"flag"
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

var (
	// 这些变量会在构建时通过 -ldflags 注入
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "none"
)

// 在 package main 声明后添加常量定义
const (
	GitSyncerDir = ".git-syncer" // 同步仓库的基础目录
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
	RemoteURL  string   `yaml:"remote_url"`
	Includes   []string `yaml:"includes"`
	Excludes   []string `yaml:"excludes"`
	Webhooks   []string `yaml:"webhooks"`
	Branch     string   `yaml:"branch"`
}

// 添加一个获取仓库路径的辅助方法
func (j *Job) GetRepoPath() string {
	return filepath.Join(".git-syncer", j.Name)
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

	// 初始化所有用户的仓库
	for _, user := range config.Users {
		// 设置用户的Git配置
		if err := gs.setupUserGitConfig(&user); err != nil {
			logger.Printf("Failed to setup git config for user %s: %v\n", user.Username, err)
			continue
		}

		// 初始化每个任务的仓库
		for _, job := range user.Jobs {
			if err := gs.initRepo(&user, &job); err != nil {
				logger.Printf("Failed to initialize repository for job %s: %v\n", job.Name, err)
				continue
			}
			logger.Printf("Successfully initialized repository for job: %s\n", job.Name)
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
	// 使用当前目录作为基础目录
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %v", err)
	}

	// 在当前目录下创建 .git-syncer/job-name 目录
	repoDir := filepath.Join(currentDir, GitSyncerDir, sanitizePath(job.Name))

	gs.logger.Printf("DEBUG: Initializing repo - Path: %s, RemoteURL: %s\n", repoDir, job.RemoteURL)

	// 如果未指定分支，使用默认分支
	if job.Branch == "" {
		job.Branch = "main"
	}
	gs.logger.Printf("DEBUG: Using branch: %s\n", job.Branch)

	isNewRepo := false

	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		gs.logger.Println("DEBUG: .git directory not found, initializing new repository")

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

		isNewRepo = true
	}

	// 如果配置了远程库
	if job.RemoteURL != "" {
		remoteURL := job.RemoteURL
		if user.GitUsername != "" && user.GitPassword != "" {
			remoteURL = addCredentialsToURL(job.RemoteURL, user.GitUsername, user.GitPassword)
		}

		// 检查是否已有 origin
		checkRemote := exec.Command("git", "remote", "get-url", "origin")
		checkRemote.Dir = repoDir
		if err := checkRemote.Run(); err != nil {
			// 如果没有 origin，添加它
			cmd := exec.Command("git", "remote", "add", "origin", remoteURL)
			cmd.Dir = repoDir
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to add remote: %v", err)
			}
		}

		if isNewRepo {
			// 创建 README.md
			readmePath := filepath.Join(repoDir, "README.md")
			if err := os.WriteFile(readmePath, []byte("# Git Sync Repository\n https://github.com/bestk/git-syncer.git"), 0644); err != nil {
				return fmt.Errorf("failed to create README: %v", err)
			}

			// 添加并提交 README，设置指定分支
			cmds := [][]string{
				{"git", "add", "README.md"},
				{"git", "commit", "-m", "Initial commit"},
				{"git", "branch", "-M", job.Branch}, // 使用配置的分支名
			}

			for _, cmdArgs := range cmds {
				cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
				cmd.Dir = repoDir
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("failed to execute %v: %v", cmdArgs, err)
				}
			}
		} else {
			// 如果不是新仓库，确保在正确的分支上
			// 先检查分支是否存在
			checkBranch := exec.Command("git", "rev-parse", "--verify", job.Branch)
			checkBranch.Dir = repoDir
			if err := checkBranch.Run(); err != nil {
				// 分支不存在，创建新分支
				createBranch := exec.Command("git", "checkout", "-b", job.Branch)
				createBranch.Dir = repoDir
				if err := createBranch.Run(); err != nil {
					return fmt.Errorf("failed to create branch %s: %v", job.Branch, err)
				}
			} else {
				// 分支存在，切换到该分支
				checkout := exec.Command("git", "checkout", job.Branch)
				checkout.Dir = repoDir
				if err := checkout.Run(); err != nil {
					return fmt.Errorf("failed to checkout branch %s: %v", job.Branch, err)
				}
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
	repoPath := job.GetRepoPath()
	gs.logger.Printf("DEBUG: Syncing files from %s to %s\n", job.SourcePath, repoPath)

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

		// 复制文件到仓库目录
		destPath := filepath.Join(repoPath, relPath)
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
	repoPath := job.GetRepoPath()
	gs.logger.Printf("DEBUG: Starting commit process for job: %s in directory: %s\n", job.Name, repoPath)

	// 确保我们在正确的目录中操作
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return fmt.Errorf("repository directory does not exist: %s", repoPath)
	}

	// 检查 git 状态
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		gs.logger.Printf("ERROR: Git status failed in %s: %v\n", repoPath, err)
		return fmt.Errorf("failed to check git status: %v", err)
	}

	gs.logger.Printf("DEBUG: Git status output in %s:\n%s", repoPath, string(output))

	if len(output) == 0 {
		gs.logger.Println("DEBUG: No changes to commit")
		return nil
	}

	// 添加所有更改
	gs.logger.Println("DEBUG: Adding changes to git")
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = repoPath
	if output, err := addCmd.CombinedOutput(); err != nil {
		gs.logger.Printf("ERROR: Git add failed: %s\n", string(output))
		return fmt.Errorf("git add failed: %v", err)
	}

	// 设置 Git 配置
	gs.logger.Printf("DEBUG: Setting git config for user: %s\n", user.Username)
	configCmds := []struct {
		args []string
		msg  string
	}{
		{[]string{"config", "user.name", user.Username}, "set user.name"},
		{[]string{"config", "user.email", user.Email}, "set user.email"},
	}

	for _, cfg := range configCmds {
		cmd := exec.Command("git", cfg.args...)
		cmd.Dir = repoPath
		if output, err := cmd.CombinedOutput(); err != nil {
			gs.logger.Printf("ERROR: Failed to %s: %s\n", cfg.msg, string(output))
			return fmt.Errorf("failed to %s: %v", cfg.msg, err)
		}
	}

	// 提交更改
	commitMsg := fmt.Sprintf("Sync update by %s: %s", user.Username, time.Now().Format("2006-01-02 15:04:05"))
	gs.logger.Printf("DEBUG: Committing with message: %s\n", commitMsg)
	commitCmd := exec.Command("git", "commit", "-m", commitMsg)
	commitCmd.Dir = repoPath
	if output, err := commitCmd.CombinedOutput(); err != nil {
		gs.logger.Printf("ERROR: Git commit failed: %s\n", string(output))
		return fmt.Errorf("git commit failed: %v", err)
	}

	if job.RemoteURL != "" {
		// 先获取远程更新
		gs.logger.Printf("DEBUG: Fetching from remote\n")
		fetchCmd := exec.Command("git", "fetch", "origin", job.Branch)
		fetchCmd.Dir = repoPath
		if output, err := fetchCmd.CombinedOutput(); err != nil {
			gs.logger.Printf("WARNING: Git fetch failed: %s\n", string(output))
		}

		// 尝试 rebase
		gs.logger.Printf("DEBUG: Rebasing with remote branch: %s\n", job.Branch)
		rebaseCmd := exec.Command("git", "rebase", fmt.Sprintf("origin/%s", job.Branch))
		rebaseCmd.Dir = repoPath
		if _, err := rebaseCmd.CombinedOutput(); err != nil {
			// 如果 rebase 失败，中止它并尝试强制推送
			abortCmd := exec.Command("git", "rebase", "--abort")
			abortCmd.Dir = repoPath
			abortCmd.Run()

			// 使用强制推送
			gs.logger.Printf("DEBUG: Force pushing to remote branch: %s\n", job.Branch)

			pushCmd := exec.Command("git", "push", "-f", "origin", job.Branch)
			pushCmd.Dir = repoPath
			if output, err := pushCmd.CombinedOutput(); err != nil {
				gs.logger.Printf("ERROR: Force push failed: %s\n", string(output))
				return fmt.Errorf("git force push failed: %s, %v", string(output), err)
			}
		} else {
			// 正常推送
			pushCmd := exec.Command("git", "push", "origin", job.Branch)
			pushCmd.Dir = repoPath
			if output, err := pushCmd.CombinedOutput(); err != nil {
				gs.logger.Printf("ERROR: Git push failed: %s\n", string(output))
				return fmt.Errorf("git push failed: %s, %v", string(output), err)
			}
		}
		gs.logger.Printf("DEBUG: Successfully pushed to remote repository on branch %s\n", job.Branch)
	}

	return nil
}

// 添加辅助函数来清理路径名
func sanitizePath(name string) string {
	// 移除或替换不安全的字符
	unsafe := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", " "}
	safe := name
	for _, char := range unsafe {
		safe = strings.ReplaceAll(safe, char, "-")
	}
	return safe
}

func main() {
	// 首先显示版本信息
	fmt.Printf("Git-Syncer %s (Build: %s, Commit: %s)\n", Version, BuildTime, GitCommit)

	// 解析命令行参数
	var daemon bool
	flag.BoolVar(&daemon, "d", false, "Run as daemon in background")
	flag.Parse()

	args := flag.Args()

	// 检查版本标志
	if len(args) == 1 && (args[0] == "-v" || args[0] == "--version") {
		return
	}

	if len(args) != 1 {
		fmt.Println("Usage: git-syncer [-d] <config_file>")
		fmt.Println("       git-syncer --version")
		os.Exit(1)
	}

	if daemon {
		// 创建子进程
		cmd := exec.Command(os.Args[0], args[0])
		cmd.Start()
		fmt.Printf("Git-Syncer is running in background with PID: %d\n", cmd.Process.Pid)
		os.Exit(0)
	}

	sync, err := NewGitSync(args[0])
	if err != nil {
		log.Fatalf("Failed to create GitSync: %v", err)
	}

	if err := sync.Run(); err != nil {
		log.Fatalf("Failed to run GitSync: %v", err)
	}
}
