# 架构设计

## 项目结构

```
go-mod-select/
├── cmd/                    # 命令行入口
│   └── root.go            # Cobra 命令定义
├── pkg/                   # 核心包
│   ├── parser/            # go.mod 解析
│   ├── finder/            # 版本查询和兼容性检查
│   ├── ui/                # TUI 界面
│   ├── updater/           # 模块更新执行
│   ├── controller/        # 主流程控制
│   ├── analyzer/          # 依赖分析
│   ├── logger/            # 日志管理
│   └── config/            # 配置管理
├── docs/                  # 文档
└── main.go               # 程序入口
```

## 模块说明

### 1. CLI Layer (`cmd/`)

**职责**：
- 命令行参数解析
- 参数验证
- 日志初始化
- 调用控制器

**主要文件**：
- `root.go` - Cobra 命令定义，参数处理

### 2. Parser Module (`pkg/parser/`)

**职责**：
- 解析 go.mod 文件
- 提取模块信息（require, replace, go version）
- 更新 go.mod 文件
- 备份和回滚

**API**：
```go
ParseGoMod(path string) (*GoMod, error)
UpdateGoVersion(version string) error
UpdateRequire(path, version string) error
```

### 3. Finder Module (`pkg/finder/`)

**职责**：
- 查询模块所有版本
- 检查版本兼容性
- 确定更新类型（patch/minor/major/prerelease）

**API**：
```go
ListVersions(modulePath string) ([]string, error)
GetModuleInfo(modulePath, version string) (*ModuleMetadata, error)
FindCompatibleVersions(modulePath, currentVersion, targetGoVersion string) (*ModuleInfo, error)
```

### 4. UI Module (`pkg/ui/`)

**职责**：
- TUI 界面渲染
- 用户交互处理
- 四标签页展示
- 详情弹窗

**主要组件**：
- `app_model.go` - Bubbletea 模型和状态管理
- `app_render.go` - 界面渲染逻辑
- `app_helpers.go` - 辅助函数（分析、查询等）

### 5. Updater Module (`pkg/updater/`)

**职责**：
- 执行模块更新
- 运行 go mod tidy
- 错误处理和回滚

### 6. Controller Module (`pkg/controller/`)

**职责**：
- 主流程编排
- 模式切换（dry-run, auto, interactive）
- 错误处理

**工作流程**：
1. 解析 go.mod
2. 分析依赖兼容性
3. 启动 TUI（或自动模式）
4. 执行更新
5. 清理和回滚（如需）

### 7. Analyzer Module (`pkg/analyzer/`)

**职责**：
- 分析项目文件使用情况
- 查找依赖链
- AST 解析

**API**：
```go
FindFilesUsingPackage(projectRoot, packagePath string) ([]string, error)
```

### 8. Logger Module (`pkg/logger/`)

**职责**：
- 日志初始化
- 统一日志接口

**API**：
```go
InitDefault() error
Log.Info(msg, ...args)
Log.Error(msg, ...args)
```

### 9. Config Module (`pkg/config/`)

**职责**：
- 颜色配置加载
- 配置文件管理

**API**：
```go
LoadOrCreateDefault() (*ColorConfig, error)
SaveColorConfig(config, path string) error
```

## 数据流

```
1. CLI 解析参数
   ↓
2. Parser 解析 go.mod
   ↓
3. Finder 并发查询版本
   ↓
4. UI 展示可更新模块
   ↓
5. 用户选择模块
   ↓
6. Updater 执行更新
   ↓
7. 成功或回滚
```

## 并发模型

### 版本查询（Finder）

```go
// 使用 goroutine + channel 并发查询
// 信号量控制并发数（最多 10 个）
semaphore := make(chan struct{}, 10)

for _, req := range requires {
    go func(r Require) {
        semaphore <- struct{}{}
        defer func() { <-semaphore }()

        info, err := finder.FindCompatibleVersions(...)
        if err == nil {
            results <- info
        }
    }(req)
}
```

## 错误处理

### 分层错误处理

1. **Finder** - 跳过失败的模块，记录日志
2. **Updater** - 更新失败时自动回滚
3. **Controller** - 捕获所有错误，友好提示

### 回滚机制

```go
// 1. 备份原始文件
backup := goMod.Backup()

// 2. 执行更新
err := updater.Update(modules)

// 3. 失败时回滚
if err != nil {
    goMod.Restore(backup)
}
```

## TUI 架构

### Bubbletea 模型

```go
type AppModel struct {
    // 状态
    currentScreen   screen
    currentTab      tabType

    // 数据
    updateModules   []*SelectableModule
    requireModules  []parser.Require
    indirectModules []parser.Require
    replaceModules  []ReplaceInfo

    // UI 组件
    viewport        viewport.Model
    detailViewport  viewport.Model
    spinner         spinner.Model
    progress        progress.Model

    // 配置
    colors          *config.ColorConfig
}
```

### 事件处理

```
KeyMsg → Update() → 更新 Model → View() → 渲染
```

## 依赖关系

```
main
 └── cmd/root
      └── controller
           ├── parser
           ├── finder
           ├── ui
           │    ├── analyzer
           │    └── config
           ├── updater
           └── logger
```

## 性能优化

1. **并发查询** - 同时查询多个模块版本（限制 10 个并发）
2. **缓存** - Bubbletea 的 viewport 自动缓存渲染结果
3. **增量更新** - 只更新选中的模块
4. **懒加载** - 详情弹窗按需生成内容

## 测试策略

- **单元测试** - 各模块独立测试
- **集成测试** - 完整流程测试
- **手动测试** - TUI 交互测试

## 扩展性

### 添加新标签页

1. 在 `app_model.go` 添加 `tabType` 常量
2. 在 `renderModuleList()` 添加渲染逻辑
3. 在 `getDetailContent()` 添加详情逻辑

### 添加新命令

1. 在 `cmd/root.go` 添加新的 Cobra 命令
2. 实现命令处理函数
3. 调用相应的控制器方法

### 自定义颜色配置

修改 `~/.go-mod-select/colors.json` 即可。
