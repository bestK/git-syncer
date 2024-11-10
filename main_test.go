package main

import (
	"log"
	"os"
	"path/filepath"
	"testing"
)

// 测试配置文件
const testConfig = `
users:
  - username: "testuser"
    email: "test@example.com"
    jobs:
      - name: "test-job"
        schedule: "* * * * *"
        source_path: "./testdata/source"
        repo_path: "./testdata/repo"
        includes: ["*.txt"]
        excludes: ["*.tmp"]
`

func TestNewGitSync(t *testing.T) {
	// 创建临时配置文件
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(testConfig)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	// 测试创建 GitSync 实例
	sync, err := NewGitSync(tmpfile.Name())
	if err != nil {
		t.Fatalf("NewGitSync failed: %v", err)
	}

	if sync == nil {
		t.Fatal("Expected non-nil GitSync instance")
	}
}

func TestShouldSync(t *testing.T) {
	gs := &GitSync{logger: createTestLogger()}

	tests := []struct {
		name     string
		path     string
		includes []string
		excludes []string
		want     bool
	}{
		{
			name:     "Match include",
			path:     "test.txt",
			includes: []string{"*.txt"},
			excludes: []string{},
			want:     true,
		},
		{
			name:     "Match exclude",
			path:     "test.tmp",
			includes: []string{"*.txt"},
			excludes: []string{"*.tmp"},
			want:     false,
		},
		{
			name:     "No includes",
			path:     "test.doc",
			includes: []string{},
			excludes: []string{},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gs.shouldSync(tt.path, tt.includes, tt.excludes)
			if got != tt.want {
				t.Errorf("shouldSync() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyncFiles(t *testing.T) {
	// 创建测试目录结构
	testDir := setupTestDirectory(t)
	defer os.RemoveAll(testDir)

	gs := &GitSync{logger: createTestLogger()}
	job := &Job{
		SourcePath: filepath.Join(testDir, "source"),
		Includes:   []string{"*.txt"},
		Excludes:   []string{"*.tmp"},
	}

	// 测试文件同步
	err := gs.syncFiles(job)
	if err != nil {
		t.Fatalf("syncFiles failed: %v", err)
	}

	// 验证文件是否正确同步
	checkSyncedFiles(t, job)
}

// 辅助函数

func createTestLogger() *log.Logger {
	return log.New(os.Stdout, "TEST: ", log.LstdFlags)
}

func setupTestDirectory(t *testing.T) string {
	dir, err := os.MkdirTemp("", "gitsync-test-*")
	if err != nil {
		t.Fatal(err)
	}

	// 创建源目录和目标目录
	sourcePath := filepath.Join(dir, "source")
	repoPath := filepath.Join(dir, "repo")
	os.MkdirAll(sourcePath, 0755)
	os.MkdirAll(repoPath, 0755)

	// 创建测试文件
	createTestFile(t, filepath.Join(sourcePath, "test1.txt"), "test content 1")
	createTestFile(t, filepath.Join(sourcePath, "test2.txt"), "test content 2")
	createTestFile(t, filepath.Join(sourcePath, "ignore.tmp"), "should be ignored")

	return dir
}

func createTestFile(t *testing.T, path, content string) {
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}
}

func checkSyncedFiles(t *testing.T, job *Job) {
	// 检查同步的文件
	repoPath := job.GetRepoPath()
	files, err := filepath.Glob(filepath.Join(repoPath, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 synced files, got %d", len(files))
	}

	// 检查被排除的文件
	excluded, err := filepath.Glob(filepath.Join(repoPath, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}

	if len(excluded) != 0 {
		t.Errorf("Expected 0 excluded files, got %d", len(excluded))
	}
}
