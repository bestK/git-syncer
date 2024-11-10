# Git-Syncer in Golang

Git-Syncer is a tool that automatically syncs local files to Git repositories based on configurable schedules.

## Features

-   🔄 Automated file synchronization with Git repositories
-   ⏰ Configurable sync schedules using cron expressions
-   📁 Multiple sync jobs support
-   🔍 File filtering with include/exclude patterns
-   🪝 Webhook support for sync notifications
-   🔐 SSH and HTTPS authentication support
-   📝 Detailed logging
-   🌲 Custom branch support

## Installation

1. Clone the repository:

```bash
git clone https://github.com/bestk/git-syncer.git
```

2. Install dependencies:

```bash
go mod tidy
```

3. Build the project:

```bash
go build
```

4. Run the project:

```bash
./git-syncer
```

## Configuration

See [config.example.yaml](config.example.yaml) for configuration details.

## Usage

```bash
./git-syncer config.yaml
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Configuration Example (config.yaml)

```yaml
## 用户配置列表
users:
    # 第一个用户配置
    - username: 'Git Syncer' # Git提交时显示的用户名
      email: 'git-syncer@example.com' # Git提交时显示的邮箱
      ssh_key_path: '~/.ssh/git_syncer' # SSH密钥路径（可选）
      git_username: 'git-syncer' # Git仓库用户名（用于HTTPS认证，可选）
      git_password: 'ghp_xxxxxxxxxxxxxxxxxxxx' # Git仓库密码或token（用于HTTPS认证，可选）

      # 用户的同步任务列表
      jobs:
          # 第一个同步任务
          - name: 'docs-sync' # 任务名称
            schedule: '*/30 * * * *' # Cron表达式（每30分钟执行一次）
            source_path: './docs' # 源文件路径
            remote_url: 'https://github.com/user/docs.git' # 远程仓库地址
            branch: 'main' # Git分支（可选，默认main）
            includes: # 文件包含规则（可选）
                - '*.md'
                - '*.txt'
                - 'images/**'
            excludes: # 文件排除规则（可选）
                - '*.tmp'
                - '.git/**'
                - 'draft/**'
            webhooks: # 任务关联的webhook（可选）
                - 'notify-slack'
                - 'notify-discord'

          # 第二个同步任务
          - name: 'config-sync'
            schedule: '0 * * * *' # 每小时执行
            source_path: './configs'
            remote_url: 'https://github.com/user/configs.git'
            branch: 'develop'
            includes:
                - '*.yaml'
                - '*.json'
            webhooks:
                - 'notify-slack'

# Webhook配置列表
webhooks:
    - name: 'notify-slack' # Webhook名称
      url: 'https://hooks.slack.com/services/xxx/yyy/zzz' # Webhook URL
      method: 'POST' # HTTP方法
      headers: # 自定义HTTP头
          Content-Type: 'application/json'
      body: | # 请求体模板
          {
            "text": "Sync job '{{.Job.Name}}' completed with status: {{.Status}}",
            "channel": "#git-sync",
            "username": "Git Syncer Bot"
          }

    - name: 'notify-discord'
      url: 'https://discord.com/api/webhooks/xxx/yyy'
      method: 'POST'
      headers:
          Content-Type: 'application/json'
      body: |
          {
            "content": "Sync status for {{.Job.Name}}: {{.Status}}",
            "username": "{{.User.Username}}"
          }
```
