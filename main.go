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

	"runtime"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-co-op/gocron"
	"github.com/sevlyar/go-daemon"
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
	Banner       = `
	______ _ _      _____                            
   / ____/(_) /_   / ___/__  ______  ________  _____
  / / __/ / __/   \__ \/ / / / __ \/ ___/ _ \/ ___/
 / /_/ / / /_    ___/ / /_/ / / / / /__/  __/ /    
 \____/_/\__/   /____/\__, /_/ /_/\___/\___/_/     
                     /____/                          
`
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
	Name          string   `yaml:"name"`
	Schedule      string   `yaml:"schedule"`
	SourcePath    string   `yaml:"source_path"`
	RemoteURL     string   `yaml:"remote_url"`
	Includes      []string `yaml:"includes"`
	Excludes      []string `yaml:"excludes"`
	Webhooks      []string `yaml:"webhooks"`
	Branch        string   `yaml:"branch"`
	MergeStrategy string   `yaml:"merge_strategy"` // 新增：合并策略配置
	RemotePath    string   `yaml:"remote_path"`    // 远程仓库中的目标路径
	KeepStructure bool     `yaml:"keep_structure"` // 是否保持原目录结构
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

// initRepo 初始化检查Git仓库
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
		gs.logger.Printf("DEBUG: Initializing new repository at %s\n", repoDir)

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
	} else {
		gs.logger.Printf("DEBUG: Using existing repository at %s\n", repoDir)
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
			// 设置分支
			cmd := exec.Command("git", "branch", "-M", job.Branch)
			cmd.Dir = repoDir
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to set branch %s: %v", job.Branch, err)
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

// validateJob 验证任务配置
func (gs *GitSync) validateJob(job *Job) error {
	if job.RemotePath != "" && job.KeepStructure {
		return fmt.Errorf("remote_path and keep_structure cannot be used together")
	}
	return nil
}

// syncFiles 同步文件
func (gs *GitSync) syncFiles(job *Job) error {
	if err := gs.validateJob(job); err != nil {
		return err
	}

	// Normalize source and repo paths
	sourcePath := filepath.ToSlash(job.SourcePath)
	repoPath := job.GetRepoPath()

	// Get absolute path of working directory
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %v", err)
	}

	// Normalize matching pattern
	pattern := normalizeSourcePath(sourcePath)
	gs.logger.Printf("DEBUG: Using pattern: %s\n", pattern)

	// Use filepath.Walk to traverse directory
	var matches []string
	err = filepath.Walk(filepath.Join(workDir, filepath.Dir(pattern)), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			gs.logger.Printf("WARNING: Failed to access path %s: %v\n", path, err)
			return nil
		}

		// Convert to relative path
		relPath, err := filepath.Rel(workDir, path)
		if err != nil {
			gs.logger.Printf("WARNING: Cannot get relative path for %s: %v\n", path, err)
			return nil
		}

		// Convert to forward slash path for matching
		relPath = filepath.ToSlash(relPath)

		// Check if matches pattern
		matched, err := doublestar.Match(pattern, relPath)
		if err != nil {
			gs.logger.Printf("WARNING: Pattern matching failed for %s: %v\n", relPath, err)
			return nil
		}

		if matched && !info.IsDir() {
			gs.logger.Printf("DEBUG: Found matching file: %s\n", relPath)
			matches = append(matches, path)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to traverse directory: %v", err)
	}

	if len(matches) == 0 {
		gs.logger.Printf("WARNING: No matching files found for pattern: %s\n", pattern)
		return nil
	}

	// Process matching files
	for _, path := range matches {
		relPath, _ := filepath.Rel(workDir, path)
		relPath = filepath.ToSlash(relPath)

		// Determine destination path
		var destPath string
		switch {
		case job.KeepStructure:
			destPath = filepath.Join(repoPath, relPath)
		case job.RemotePath != "":
			destPath = filepath.Join(repoPath, job.RemotePath, filepath.Base(path))
		default:
			destPath = filepath.Join(repoPath, filepath.Base(path))
		}

		// Create destination directory
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			gs.logger.Printf("WARNING: Failed to create directory %s: %v\n", filepath.Dir(destPath), err)
			continue
		}

		if err := gs.copyFile(path, destPath); err != nil {
			gs.logger.Printf("WARNING: Failed to copy file %s: %v\n", path, err)
			continue
		}

		gs.logger.Printf("Successfully synced file: %s to %s\n", relPath, destPath)
	}

	return nil
}

// shouldSync 检查文件是否应该被同步
func (gs *GitSync) shouldSync(path string, includes, excludes []string) bool {
	// 首先检查排除规则
	for _, exclude := range excludes {
		matched, err := doublestar.Match(exclude, path)
		if err == nil && matched {
			gs.logger.Printf("DEBUG: file %s excluded by rule %s\n", path, exclude)
			return false
		}
	}

	// 如果没有包含规则，则包含所有文件
	if len(includes) == 0 {
		return true
	}

	// 检查包含规则
	for _, include := range includes {
		matched, err := doublestar.Match(include, path)
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

		// 根据合策略处理
		switch strings.ToLower(job.MergeStrategy) {
		case "rebase":
			// 使用 rebase 策略
			if err := gs.rebaseAndPush(repoPath, job); err != nil {
				return err
			}
		case "force":
			// 使用强制推送策略
			if err := gs.forcePush(repoPath, job); err != nil {
				return err
			}
		default:
			// 默认使用普通推送
			if err := gs.normalPush(repoPath, job); err != nil {
				return err
			}
		}
		gs.logger.Printf("DEBUG: Successfully pushed to remote repository on branch %s\n", job.Branch)
	}

	return nil
}

// 添加以下辅助方法

// rebaseAndPush 执行 rebase 并推送
func (gs *GitSync) rebaseAndPush(repoPath string, job *Job) error {
	gs.logger.Printf("DEBUG: Rebasing with remote branch: %s\n", job.Branch)
	rebaseCmd := exec.Command("git", "rebase", fmt.Sprintf("origin/%s", job.Branch))
	rebaseCmd.Dir = repoPath
	if output, err := rebaseCmd.CombinedOutput(); err != nil {
		gs.logger.Printf("ERROR: Rebase failed: %s\n", string(output))
		// 中止 rebase
		abortCmd := exec.Command("git", "rebase", "--abort")
		abortCmd.Dir = repoPath
		abortCmd.Run()
		return fmt.Errorf("git rebase failed: %v", err)
	}

	return gs.normalPush(repoPath, job)
}

// forcePush 执行强制推送
func (gs *GitSync) forcePush(repoPath string, job *Job) error {
	gs.logger.Printf("DEBUG: Force pushing to remote branch: %s\n", job.Branch)
	pushCmd := exec.Command("git", "push", "-f", "origin", job.Branch)
	pushCmd.Dir = repoPath
	if output, err := pushCmd.CombinedOutput(); err != nil {
		gs.logger.Printf("ERROR: Force push failed: %s\n", string(output))
		return fmt.Errorf("git force push failed: %v", err)
	}
	return nil
}

// normalPush 执行普通推送
func (gs *GitSync) normalPush(repoPath string, job *Job) error {
	gs.logger.Printf("DEBUG: Pushing to remote branch: %s\n", job.Branch)
	pushCmd := exec.Command("git", "push", "origin", job.Branch)
	pushCmd.Dir = repoPath
	if output, err := pushCmd.CombinedOutput(); err != nil {
		gs.logger.Printf("ERROR: Push failed: %s\n", string(output))
		return fmt.Errorf("git push failed: %v", err)
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

func startDaemon() error {
	if runtime.GOOS == "windows" {
		// Windows: 使用简单的后台运行方案
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Args = append(cmd.Args, "-nodaemon") // 防止递归
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start daemon: %v", err)
		}
		fmt.Println("Git-Syncer is running in background with PID:", cmd.Process.Pid)
		return nil
	}

	// POSIX systems: 使用 go-daemon
	cntxt := &daemon.Context{
		PidFileName: "git-syncer.pid",
		PidFilePerm: 0644,
		LogFileName: "git-syncer-daemon.log",
		LogFilePerm: 0640,
		WorkDir:     "./",
		Umask:       027,
	}

	d, err := cntxt.Reborn()
	if err != nil {
		return fmt.Errorf("unable to run: %v", err)
	}
	if d != nil {
		fmt.Println("Git-Syncer daemon started. Check git-syncer-daemon.log for details")
		return nil
	}

	defer cntxt.Release()
	fmt.Print(Banner)
	fmt.Printf("Git-Syncer %s daemon started\n", Version)
	return nil
}

var (
	// 版本相关标志
	showVersion bool
	// daemon 模式标志
	daemonFlag bool
	// 配置文件路径
	configFile string
	// 帮助标志
	help bool
	// 防止递归的标志
	noDaemon bool
)

func main() {

	flag.BoolVar(&daemonFlag, "d", false, "Run as daemon")
	flag.BoolVar(&noDaemon, "nodaemon", false, "Internal flag to prevent recursive daemon")
	flag.BoolVar(&showVersion, "v", false, "Show version information")
	flag.BoolVar(&showVersion, "version", false, "Show version information (same as -v)")
	flag.StringVar(&configFile, "c", "config.yml", "Path to config file")
	flag.BoolVar(&help, "h", false, "Show help information")

	flag.Parse()

	// 检查帮助标志
	if len(os.Args) == 1 || help {
		fmt.Print(Banner)
		fmt.Printf("Git-Syncer %s\n\n", Version)
		fmt.Println("Usage:")
		fmt.Printf("  %s [options]\n\n", os.Args[0])
		fmt.Println("Options:")
		flag.PrintDefaults()
		return
	}

	// 检查版本标志
	if showVersion {
		// 显示 banner
		fmt.Print(Banner)
		// 显示版本信息
		fmt.Printf("Git-Syncer %s (Build: %s, Commit: %s)\n", Version, BuildTime, GitCommit)
		return
	}

	if daemonFlag && !noDaemon {
		if err := startDaemon(); err != nil {
			log.Fatal("Failed to start daemon: ", err)
		}
		if runtime.GOOS == "windows" {
			return // Windows 后台进程已启动，退出当前进程
		}
	}

	if configFile == "" {
		fmt.Println("No config file provided, using default: config.yml")
		// 检查 config.yml 是否存在
		if _, err := os.Stat("config.yml"); os.IsNotExist(err) {
			fmt.Println("Default config file not found, please provide a valid config file with -c option")
			os.Exit(1)
		}

	}

	// 使用配置文件标志
	sync, err := NewGitSync(configFile)
	if err != nil {
		log.Fatalf("Failed to create GitSync: %v", err)
	}

	if err := sync.Run(); err != nil {
		log.Fatalf("Failed to run GitSync: %v", err)
	}
}

// 更新辅助函数来处理路径
func normalizeSourcePath(path string) string {
	// Convert backslashes to forward slashes
	path = filepath.ToSlash(path)

	// Remove leading ./
	if strings.HasPrefix(path, "./") {
		path = path[2:]
	}

	// Ensure pattern is correct
	if !strings.Contains(path, "*") {
		// If no wildcard, add /** to match all files
		if strings.HasSuffix(path, "/") {
			path = path + "**"
		} else {
			path = path + "/**"
		}
	}

	return path
}
