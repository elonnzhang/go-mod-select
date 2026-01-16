# go-mod-select

一个交互式 Go 模块版本管理工具，帮助你升/降级 Go 版本的同时确保所有依赖兼容。

灵感来自 [go-mod-upgrade](https://github.com/oligot/go-mod-upgrade)

## ✨ 特性

- 🎯 **智能兼容性检查** - 自动检测依赖与目标 Go 版本的兼容性
- 🎨 **四标签页 TUI** - Update（更新）/ Require（依赖）/ Indirect（间接依赖）/ Replace（替换）
- 🔍 **详情弹窗** - 查看包使用情况和依赖链（支持滚动）
- ⌨️ **快捷键操作** - 空格选择、`/`过滤、Tab 切换标签页
- 🔄 **并发查询** - 快速检查多个模块版本
- 🎨 **可配置主题** - 自定义 TUI 颜色
- 📝 **结构化日志** - JSON 格式日志便于分析
- 🛡️ **安全回滚** - 失败时自动恢复

## 📦 安装

```bash
# 从源码构建
git clone https://github.com/elonnzhang/go-mod-select.git
cd go-mod-select
go build -o go-mod-select

# 或使用 go install
go install github.com/elonnzhang/go-mod-select@latest
```

## 🚀 快速开始

```bash
# 基本用法 - 升级到 Go 1.25
go-mod-select 1.25

# 指定工作目录
go-mod-select 1.25 --dir /path/to/project

# 指定 go.mod 文件
go-mod-select 1.25 --gomod /path/to/go.mod

# 仅查看可更新模块（不执行更新）
go-mod-select 1.25 --dry-run

# 自动选择所有兼容更新
go-mod-select 1.25 --auto

# 查看详细日志
go-mod-select 1.25 --verbose
```

## 📖 文档

- [使用指南](./docs/usage.md) - 详细使用说明
- [配置说明](./docs/configuration.md) - 日志和颜色配置
- [架构设计](./docs/architecture.md) - 项目架构说明

## ⌨️ 快捷键

| 按键                   | 功能                                               |
| ---------------------- | -------------------------------------------------- |
| `↑` / `↓` 或 `k` / `j` | 上下移动                                           |
| `Space`                | 选择/取消（Update 标签页）或查看详情（其他标签页） |
| `Tab`                  | 切换标签页                                         |
| `/`                    | 过滤模式                                           |
| `Enter`                | 确认更新（Update 标签页）                          |
| `ESC`                  | 取消过滤/关闭弹窗                                  |
| `q` 或 `Ctrl+C`        | 退出                                               |

## 🎨 颜色说明
- 🔵 **青色** - Major 更新（主版本）
- 🟡 **黄色** - Minor 更新（次版本）
- 🟢 **绿色** - Patch 更新（补丁版本）
- 🔴 **红色** - Prerelease 更新（预发布版本）

## 📁 配置文件

```
~/.go-mod-select/
├── app.log        # JSON 格式日志
└── colors.json    # TUI 颜色配置
```

查看日志：

```bash
tail -f ~/.go-mod-select/app.log | jq .
```

自定义颜色：

```bash
vim ~/.go-mod-select/colors.json
```

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

MIT License
