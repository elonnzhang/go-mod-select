# 架构设计

## 项目结构

```
go-mod-select/
├── cmd/
│   ├── root.go              # Cobra 命令定义、参数校验、go.mod 路径解析
│   └── root_test.go
├── pkg/
│   ├── parser/              # go.mod 解析与写回
│   ├── finder/
│   │   ├── finder.go        # 版本查询（沙箱 + 批量 + 命中即停）
│   │   └── version.go       # Go 版本比较（go/version）
│   ├── ui/
│   │   ├── app_model.go     # Bubbletea 模型、消息、按键分发
│   │   ├── app_render.go    # 各屏幕渲染
│   │   ├── app_helpers.go   # 分析 worker、详情加载、派生状态
│   │   ├── app_keys.go      # 按键绑定表
│   │   └── banner.txt       # 嵌入的 ASCII banner
│   ├── updater/             # go get / go mod tidy / 回滚 / 版本校验
│   ├── controller/          # 模式分派（dry-run / auto / TUI）
│   ├── analyzer/            # AST 导入扫描、go mod why
│   ├── logger/              # slog JSON 日志
│   └── config/              # 颜色配置
├── docs/
└── main.go
```

## 模块说明

### 1. CLI Layer (`cmd/`)

**职责**：
- 命令行参数解析与互斥校验
- 目标版本格式校验（复用 finder 的解析逻辑，避免 CLI 接受的与实际能比较的漂移）
- go.mod 路径解析：`--gomod` > `--dir` > 向上查找模块根
- 日志初始化（失败只警告，不中止）

**主要文件**：
- `root.go` - Cobra 命令定义，`resolveGoModPath` / `findModuleRoot`

### 2. Parser Module (`pkg/parser/`)

**职责**：
- 解析 go.mod 文件（基于 `golang.org/x/mod/modfile`）
- 提取模块信息（require / replace / go version）
- 更新 go 指令
- 提供回滚所需的原始内容快照

**API**：
```go
ParseGoMod(path string) (*GoMod, error)
(*GoMod).UpdateGoVersion(version string) error
(*GoMod).Backup() []byte
(*GoMod).Restore(backup []byte) error
CountDirectDeps(requires []Require) int
CountIndirectDeps(requires []Require) int
```

### 3. Finder Module (`pkg/finder/`)

**职责**：
- 查询模块所有版本
- 检查版本兼容性
- 确定更新类型（patch/minor/major/prerelease）

**API**：
```go
New(projectDir string) *Finder            // 创建查询沙箱，用完 Close()
(*Finder).ListVersions(modulePath string) ([]string, error)
(*Finder).GetModuleInfo(modulePath, version string) (*ModuleMetadata, error)
(*Finder).FindCompatibleVersions(modulePath, currentVersion, targetGoVersion string) (*ModuleInfo, error)

GoVersionSatisfied(moduleGo, targetGo string) bool  // 基于 go/version 比较
IsValidGoVersion(v string) bool
SameGoLanguageVersion(a, b string) bool
```

**两个关键设计**：

*沙箱查询*：所有 `go list` 都在一个临时的最小模块里执行，而不是用户的项目目录。就地查询只有两个都不好的选项——`-mod=mod` 会在只读分析期间改写用户的 go.mod/go.sum；`-mod=readonly` 一旦问到不在 build list 里的版本就直接报 `updates to go.sum needed` 而失败。沙箱两者都能避开。

*合并查询 + 渐进批量 + 命中即停*：先用一次 `go list -m -e -json -versions <mod>@latest` 同时拿到全部已发布版本和 `@latest` 的 go 指令；最新版兼容（绝大多数情况）时，一个模块只花这一次子进程调用。否则从新到旧按 2、4、8 的递增批量继续用 `go list -m -e -json` 查询，找到第一个兼容版本就返回。`-e` 让单个失败版本以内联 Error 字段出现，不会中断整批。批量从小开始是因为 `go list` 对多个参数是**顺序**解析的（冷缓存实测：1 个 0.12s、2 个 0.26s、8 个 0.80s），为了用上第一个而一口气解析 8 个并不划算。若合并查询拿不到版本列表（罕见模块或旧工具链），回退到单独的 `-versions` 调用。

*go 指令的读取*：`go list` 对 go 指令高于本机工具链的版本不返回 `GoVersion`，只给出缓存 `.mod` 的路径。`fillGoVersion` 在字段缺失时解析该文件，否则这类版本会被当成"没有 go 指令"而判为兼容。

*版本比较*：Go 版本用 `go/version` 而非 `semver` 比较。`semver` 无法正确解析 `1.21rc1` 这类 Go 特有写法，且对非法输入返回 0（会被误判为"相等即兼容"）。比较前把 `1.21` 补成 `1.21.0`，因为 `go/version` 把裸的 `go1.21`（语言版本）排在 `go1.21.0` 之前。

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
- 执行模块更新（`go get`）
- 运行 `go mod tidy`
- 错误处理和回滚
- 校验最终落地的 go 指令

**API**：
```go
NewUpdater(goMod *parser.GoMod) *Updater   // 构造时快照 go.mod + go.sum
(*Updater).UpdateAllWithProgress(targetGoVersion string, modules []*finder.ModuleInfo, verbose bool, cb ProgressCallback) error
(*Updater).VerifyGoVersion(requested string) (actual string, matched bool, err error)
(*Updater).Rollback() error
```

`VerifyGoVersion` 存在的原因：`go mod tidy` 会在剩余依赖需要更新工具链时主动抬高 go 指令，所以"请求的版本"不等于"磁盘上的版本"。不校验就会把"降级到 1.17"悄悄变成"仍然是 1.25"。

### 6. Controller Module (`pkg/controller/`)

**职责**：
- 主流程编排
- 模式切换（dry-run, auto, interactive）
- 错误处理

**工作流程**：
1. 解析 go.mod
2. 按模式分派：dry-run 只分析并打印；auto 分析后直接应用；交互模式交给 TUI
3. TUI 自己完成更新（不再把选择结果回传给 controller 重复执行一遍）

### 7. Analyzer Module (`pkg/analyzer/`)

**职责**：
- 用 AST（`parser.ImportsOnly`）扫描哪些文件导入了某个模块
- 通过 `go mod why -m` 解释间接依赖的来源

**API**：
```go
FindFilesUsingPackage(projectRoot, packagePath string) ([]string, error)
FindWhyPackageNeeded(projectRoot, packagePath string) []string
```

扫描时跳过 `vendor` / `.git` / `node_modules` / `testdata` 及隐藏目录；**包含** `_test.go`（测试文件也是真实使用点）；不可读的目录跳过而非中断整个遍历——详情弹窗里一个部分结果比一条错误更有用。匹配按路径边界判断，`github.com/x/lib` 不会命中 `github.com/x/libextra`。

### 8. Logger Module (`pkg/logger/`)

**职责**：
- 初始化写入 `~/.go-mod-select/app.log` 的 JSON slog handler
- 提供全局 `Log`

**API**：
```go
InitDefault() error   // 失败不致命，调用方打印警告即可
Init(logPath string) error
```

`Log` 的零值是一个 discard logger，因此库代码和没调用 `Init` 的测试不会因为日志调用而 nil panic。

### 9. Config Module (`pkg/config/`)

**职责**：
- 加载/创建颜色配置

**API**：
```go
DefaultColorConfig() *ColorConfig
LoadColorConfig(path string) (*ColorConfig, error)   // 缺失的键沿用默认值
LoadOrCreateDefault() (*ColorConfig, error)          // 返回可用配置 + 可选警告
SaveColorConfig(config *ColorConfig, path string) error
```

`LoadColorConfig` 把 JSON 反序列化到**已填好默认值**的结构体上，而不是零值结构体。否则只写了几个键的配置文件会把其余颜色变成 `""`，lipgloss 渲染为无样式——界面大面积失色，而这恰恰是最常见的用法。

## 数据流

```
CLI 解析参数 / 校验版本 / 定位 go.mod
   ↓
Parser 解析 go.mod
   ↓
┌── --dry-run ──→ Finder 分析 ──→ 打印计划，结束
├── --auto ─────→ Finder 分析 ──→ 过滤降级 ──→ Updater 应用 ──→ 校验 go 指令
└── 交互 ───────→ TUI
                   ↓
                 欢迎页 → 分析页（并发 Finder，实时日志）
                   ↓
                 选择页（四标签页 / 过滤 / 详情弹窗）
                   ↓
                 更新页（Updater + 进度回调）
                   ↓
                 完成页（成功摘要 或 失败+已回滚）
                   ↓
                 Enter 回选择页做下一轮，q 退出
```

## 并发模型

### 版本查询

每个直接依赖一个 goroutine，用信号量限制同时运行的子进程数：

```go
const analyzeConcurrency = 12

semaphore := make(chan struct{}, analyzeConcurrency)
for _, req := range requires {
    go func(r parser.Require) {
        semaphore <- struct{}{}
        defer func() { <-semaphore }()
        info, err := f.FindCompatibleVersions(r.Path, r.Version, targetGoVersion)
        // 结果在 mutex 下收集，避免 worker 阻塞在无人读取的 channel 上
    }(req)
}
```

并发数刻意保持较小：每个 worker 会 fork 一个做网络 IO 的 `go list` 子进程。之前的 200 会为大 go.mod 拉起数百个进程，既被 proxy 限流，也让操作系统在上下文切换上白花开销。

### TUI 中的慢操作

`go mod why` 和源码目录遍历都慢到肉眼可感。它们通过 `tea.Cmd` 在事件循环外执行，弹窗先立刻渲染模块信息，结果到达后再填充；期间的按键和 spinner 不受影响。晚到的结果会用模块名比对当前弹窗，不匹配就丢弃。

## 错误处理

### 分层错误处理

1. **Finder** - 跳过失败的模块/版本，记录日志；无法解析的 go 版本按"兼容"处理（漏报一次更新比误把模块排除更可接受）
2. **Updater** - 更新失败时回滚 go.mod 和 go.sum
3. **Controller / TUI** - 展示失败原因并说明已回滚

### 回滚机制

```go
// NewUpdater 时快照两个文件
u := updater.NewUpdater(goMod)   // 内部保存 go.mod、go.sum 内容及 go.sum 是否原本存在

// 任一步失败都回滚
if err := u.UpdateAllWithProgress(...); err != nil {
    // 内部已 Rollback：go.mod 恢复、go.sum 恢复；
    // 原本不存在 go.sum 时，删除新生成的那个
}
```

只恢复 go.mod 是不够的：`go get` 同时改写 go.sum，留下不一致的 go.sum 会让模块无法构建。

## TUI 架构

### 五个屏幕

`screenWelcome` → `screenAnalyzing` → `screenSelection` → `screenUpdating` → `screenComplete`，完成后可回到 `screenSelection` 做下一轮。

### Bubbletea 模型

```go
type AppModel struct {
    // 状态机
    currentScreen   screen
    currentTab      tabType

    // 数据
    updateModules   []*SelectableModule   // 可选，带 Selected / Updated 标志
    requireModules  []parser.Require      // 只读
    indirectModules []parser.Require      // 只读
    replaceModules  []ReplaceInfo         // 只读

    // 选择页交互
    cursor          int
    filterMode      bool                  // 正在输入过滤词
    filterInput     string                // 已输入/已应用的过滤词
    showingDetail   bool
    detailModule    string                // 用于丢弃过期的异步结果

    // 进度
    analyzingLogOffset int                // 分析日志滚动位置
    analyzingLogFollow bool               // 是否跟随最新
    tidySucceeded      bool
    updateFailed       bool
    updateError        error
    actualGoVersion    string             // tidy 之后磁盘上的实际版本
    goVersionMatched   bool

    // UI 组件
    detailViewport viewport.Model         // 仅详情弹窗使用；模块列表自己做窗口化
    listTop int                           // 列表可见窗口的首行；只渲染窗口内的行
    pathCol int                           // 路径列宽，随标签页与终端宽度调整
    styles  *styleSet                     // 启动时按主题一次性构建的样式
    spinner  spinner.Model
    progress progress.Model

    colors *config.ColorConfig
}
```

派生状态刻意不缓存：`countSelected()`、`visibleIndices()`、`updatedModules()` 每次渲染重算。列表规模是几十到几百，重算的成本远低于缓存失效带来的 bug。

*窗口化渲染*：模块列表只渲染屏幕上可见的那几行（`renderModuleList`），而不是把整张列表交给 viewport 再滚动。按一次方向键的代价因此与模块总数无关（300 个模块：1.0 ms → 0.22 ms，内存分配 1.19 MB → 0.22 MB；没有过滤时耗时与列表长度无关，生效的过滤每次按键多一遍 O(n) 路径匹配，300 个模块约 +40µs）。过滤对四个标签页统一生效，光标是**过滤后列表**里的位置，经 `selectedIndex()` 映射回各标签页的底层条目。

*样式缓存*：lipgloss 样式在 `styleSet` 里按主题构建一次。此前列表和日志面板每次重绘都会创建数百个临时样式，分析日志还会每行重建一张 map。

### 消息

| 消息 | 来源 | 作用 |
|------|------|------|
| `startAnalysisMsg` | 欢迎页按 Enter | 启动分析 worker |
| `analyzeProgressMsg` | 分析 worker | 单个模块的 checking/found/skipped/error |
| `analyzeCompleteMsg` | 分析 worker | 全部结果，切到选择页 |
| `startUpdateMsg` | 选择页按 Enter | 启动更新 worker |
| `updateProgressMsg` | 更新 worker | 每个模块 + tidy 的进度 |
| `updateFailedMsg` | 更新 worker | 失败原因（此时已回滚完成） |
| `updateCompleteMsg` | 更新 worker | 结束，附带实际 go 版本 |
| `detailLoadedMsg` | 详情加载 Cmd | 弹窗的异步正文 |

### 事件处理

```
KeyMsg → handleKeyPress() → 按 currentScreen 分派 → 更新 Model → View() → 渲染
```

按键判定顺序很关键：`Ctrl+C`（全局）→ 弹窗 → **过滤输入** → 命令绑定。过滤输入必须排在命令绑定之前，否则输入模块名里的 `q` 会退出程序。

`Ctrl+C` 在更新页是**取消**而不是退出：直接退出会让 `go get` 在无人监管下继续改写 go.mod，且没有回滚。第一次按下通过 context 中断子进程（先 SIGINT，5 秒后才 SIGKILL），updater 照常回滚并返回 `ErrCancelled`；第二次才真正退出。

退出时 `TUI()` 在关闭 `quit` 之后会等待后台 worker 收尾（最多 10 秒）：删除沙箱目录、终止 `go list` 子进程、完成回滚。此前它关闭信号后立即返回，进程在 worker 的 `defer` 跑完之前就退出了。

日志条目以 `[STATUS]pkg` 的形式存储，而不是预渲染好的带色文本。这样窗口尺寸变化时能重新上色，也让完成状态可以就地替换自己的 `[CHECKING]` 占位行。

## 依赖关系

```
main
 └── cmd/root ── finder（版本格式校验）
      └── controller
           ├── parser
           ├── finder
           ├── ui
           │    ├── analyzer
           │    ├── config
           │    ├── finder
           │    └── updater
           └── updater
```

`logger` 被几乎所有包引用，未在图中展开。

## 性能优化

1. **合并查询 + 渐进批量 + 命中即停** - 最新版兼容时每模块只需一次子进程调用，而不是"每个已发布版本一次"。本项目上 `--dry-run` 从 16.0s 降到 0.21s；24 个直接依赖、冷模块缓存的合成 go.mod 上，子进程 46 → 29，元数据解析 167 → 6 个版本，`--dry-run` 中位耗时约 2.4s → 1.1s
2. **有界并发** - 12 个 worker，避免进程爆炸和 proxy 限流
3. **异步详情** - `go mod why` 和源码遍历不阻塞事件循环
4. **增量更新** - 只更新选中的模块；已应用的模块不会在第二轮重复 `go get`
5. **窗口化列表 + 样式缓存** - 按键代价只与可见行数有关
6. **spinner 只在分析页转** - 其他页面不再每秒重绘十次

## 已知限制

- **不提议 v2+ 大版本升级**。Go 把 v2 及以上放在带 `/vN` 后缀的独立模块路径下，`go list -m -versions` 不会把 v2 报告为 v1 的可用版本。这类升级需要改 import 路径，属于人工决策
- **只分析直接依赖**。间接依赖仅展示，不参与更新——它们应由 `go mod tidy` 根据直接依赖推导
- **不检查 replace 指令的兼容性**。被 replace 的模块按原路径查询，本地 replace 的实际内容不会被检查
- **`go mod tidy` 可能抬高 go 指令**至目标版本之上。这不是 bug 而是 go 工具的行为，工具会检测并报告，但不会阻止
- **配置只在启动时读取**，修改颜色需要重启
- **需要网络**（或已预热的模块缓存）；离线时所有版本查询都会失败

## 测试策略

- **单元测试** - 版本比较边界、回滚语义、按键处理、渲染分支、配置合并、模块根查找、`go list -json` 流式解析
- **端到端测试（`e2e/`）** - 在真实伪终端里运行构建出的二进制，对着进程内的假 GOPROXY（`httptest`）和临时的 HOME / TMPDIR / 模块缓存，完全离线、互不干扰。覆盖：dry-run 输出、过滤→选择→更新→返回后 Require 页刷新、分析中途退出不残留沙箱目录、更新中途 `Ctrl+C` 与 SIGHUP 后 go.mod / go.sum 逐字节恢复、go 指令高于本机工具链的版本不被提议。这类问题（过滤框里的 `q`、中途退出泄漏、取消不回滚）单元测试发现不了。`-short` 时跳过；用 `GMS_E2E_BIN=/path/to/binary go test ./e2e` 可以对另一个构建（比如修复前的版本）跑同一套测试，确认测试确实能在旧行为上失败
- **手动测试** - 视觉细节、配色

测试都不访问外网：单元测试用 `t.TempDir()` 和 `t.Setenv("GOPROXY", "off")`，端到端测试用本地假代理，因此可以离线跑。

## 扩展性

### 添加新标签页

1. 在 `app_model.go` 的 `tabType` 中新增常量（放在 `tabCount` 之前，循环逻辑依赖它）
2. 在 `renderModuleList()` 添加渲染逻辑
3. 在 `detailHeader()` / `loadDetailBody()` 添加详情逻辑
4. 在 `getMaxCursor()` 和 `renderTabs()` 补上对应分支

### 添加新命令

1. 在 `cmd/root.go` 添加新的 Cobra 命令
2. 实现命令处理函数
3. 调用相应的控制器方法

### 添加新的颜色配置项

1. 在 `pkg/config/colors.go` 的 `ColorConfig` 加字段（带 `json` tag）
2. **在 `DefaultColorConfig()` 里给默认值** —— 漏掉会让该项渲染为无样式文本，`TestDefaultConfigIsFullyPopulated` 会捕获这个疏漏
3. 同步更新 `colors.example.json` 和 [配置说明](./configuration.md#配置项说明)

### 自定义颜色配置

修改 `~/.go-mod-select/colors.json` 即可，只写想改的键。详见 [配置说明](./configuration.md#颜色配置)。
