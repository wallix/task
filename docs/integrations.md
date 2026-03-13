---
title: Integrations
description: Integrations for Task
outline: deep
---

# Integrations

## Schema

A JSON Schema is available for Taskfiles. This schema can be used to validate
Taskfiles and provide autocompletion in many code editors.

### Visual Studio Code

To integrate the schema into VS Code, you need to install the
[YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
by Red Hat. You can configure it by adding the following to your
`settings.json`:

```json
// settings.json
{
  "yaml.schemas": {
    "https://taskfile.dev/schema.json": [
      "**/Taskfile.yml",
      "./path/to/any/other/taskfile.yml"
    ]
  }
}
```

You can also configure the schema directly inside of a Taskfile by adding the
following comment to the top of the file:

```yaml
# yaml-language-server: $schema=https://taskfile.dev/schema.json
version: '3'
```

## Community Integrations

- [Sublime Text Plugin](https://packagecontrol.io/packages/Taskfile)
  [[source](https://github.com/biozz/sublime-taskfile)] by @biozz
- [IntelliJ Plugin](https://plugins.jetbrains.com/plugin/17058-taskfile)
  [[source](https://github.com/lechuckroh/task-intellij-plugin)] by @lechuckroh
- [mk](https://github.com/pycontribs/mk) command line tool recognizes Taskfiles
  natively.
- [fzf-make](https://github.com/kyu08/fzf-make) fuzzy finder with preview window
  for make, pnpm, yarn, just & task.
