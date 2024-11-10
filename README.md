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
