---
title: Installation
description: Installation methods for Task
outline: deep
---

# Installation

## Binary

You can download the binary from the
[releases page on GitHub](https://github.com/wallix/task/releases) and add to
your `$PATH`.

The `task_checksums.txt` file contains the SHA-256 checksum for each file.

## Build From Source

Ensure that you have a supported version of [Go](https://golang.org) properly
installed and setup. You can find the minimum required version of Go in the
[go.mod](https://github.com/wallix/task/blob/main/go.mod#L3) file.

You can then install the latest release globally by running:

```shell
go install github.com/wallix/task/v3/cmd/task@latest
```

Or you can install into another directory:

```shell
env GOBIN=/bin go install github.com/wallix/task/v3/cmd/task@latest
```

## Setup completions

You can run `task --completion <shell>` to output a completion script for any
supported shell. There are a couple of ways these completions can be added to
your shell config:

### Option 1. Load the completions in your shell's startup config (Recommended)

This method loads the completion script from the currently installed version of
task every time you create a new shell. This ensures that your completions are
always up-to-date.

::: code-group

```shell [bash]
# ~/.bashrc
eval "$(task --completion bash)"
```

```shell [zsh]
# ~/.zshrc
eval "$(task --completion zsh)"
```

```shell [fish]
# ~/.config/fish/config.fish
task --completion fish | source
```

```powershell [powershell]
# $PROFILE\Microsoft.PowerShell_profile.ps1
Invoke-Expression  (&task --completion powershell | Out-String)
```

:::

### Option 2. Copy the script to your shell's completions directory

This method requires you to manually update the completions whenever Task is
updated. However, it is useful if you want to modify the completions yourself.

::: code-group

```shell [bash]
task --completion bash > /etc/bash_completion.d/task
```

```shell [zsh]
task --completion zsh  > /usr/local/share/zsh/site-functions/_task
```

```shell [fish]
task --completion fish > ~/.config/fish/completions/task.fish
```

:::
