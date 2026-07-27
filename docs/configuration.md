# 配置说明

## 配置文件位置

```
~/.go-mod-select/
├── app.log        # 应用日志（JSON 格式）
└── colors.json    # TUI 颜色配置
```

首次运行时自动创建配置目录和默认配置文件。

## 日志配置

日志始终写入 `~/.go-mod-select/app.log`，目前不可通过参数或环境变量改路径。

日志是**尽力而为**的：主目录不可写时会向 stderr 打印一行警告，然后照常运行，不会因为写不了日志而中止。

### 日志格式

采用 JSON 格式的结构化日志：

```json
{"time":"2026-07-27T10:30:00Z","level":"INFO","msg":"go-mod-select started"}
{"time":"2026-07-27T10:30:00Z","level":"INFO","msg":"resolved go.mod path","path":"/path/to/go.mod"}
{"time":"2026-07-27T10:30:01Z","level":"INFO","msg":"finding compatible versions","module":"github.com/spf13/cobra","current":"v1.7.0","target_go":"1.25"}
{"time":"2026-07-27T10:30:01Z","level":"DEBUG","msg":"found versions","module":"github.com/spf13/cobra","count":27}
{"time":"2026-07-27T10:30:02Z","level":"INFO","msg":"found compatible version","module":"github.com/spf13/cobra","latest":"v1.10.2","update_type":"minor","downgrade":false,"blocked_newer":0}
```

`blocked_newer` 是被目标 Go 版本挡掉的更新版本数量 —— 想知道"为什么没升到最新版"，看这个字段。

### 日志级别

全部级别都会写入文件（包括 DEBUG）：

- **DEBUG** - 版本列表长度、批查询失败等细节
- **INFO** - 一般操作信息
- **WARN** - 警告（不影响运行），如某模块查询失败被跳过
- **ERROR** - 错误，如 `go get` 失败、回滚未完成

### 查看日志

```bash
# 实时查看
tail -f ~/.go-mod-select/app.log | jq .

# 只看错误和警告
jq 'select(.level=="ERROR" or .level=="WARN")' ~/.go-mod-select/app.log

# 某个模块的完整过程
jq 'select(.module=="github.com/spf13/cobra")' ~/.go-mod-select/app.log

# 看哪些模块被目标 Go 版本挡住了新版本
jq 'select(.blocked_newer != null and .blocked_newer > 0) | {module, latest, blocked_newer}' ~/.go-mod-select/app.log

# 清空日志（文件是追加写入的，会一直增长）
> ~/.go-mod-select/app.log
```

## 颜色配置

### 配置文件

位置：`~/.go-mod-select/colors.json`，首次运行时以默认值创建。

**只需写想改的键**：缺失的键沿用默认值，不会变成"无颜色"。所以最小的配置文件可以只有一行：

```json
{ "cursor": "#ff87d7" }
```

完整默认配置（等同于 [colors.example.json](../colors.example.json)）：

```json
{
  "title": "#7d3de3ff",
  "header": "#7d3de3ff",
  "border": "#5f87af",
  "tab_active": "#7d3de3ff",
  "tab_inactive": "#626262",
  "tab_background": "#bdd4dfff",
  "help": "#a4cf64ff",
  "patch": "#00d700",
  "minor": "#ffd700",
  "major": "#00afff",
  "prerelease": "#ff0000",
  "selected": "#7d3de3ff",
  "cursor": "#7d3de3ff",
  "popup_border": "#ff5faf",
  "info": "#5fffd7",
  "success": "#a4cf64ff",
  "log": "#cfd4b7ff"
}
```

### 配置项说明

| 配置项 | 用在哪 | 默认值 |
|--------|--------|--------|
| `title` | 各屏幕标题、日志区标题、spinner | `#7d3de3ff` 紫 |
| `header` | 预留（当前渲染未使用） | `#7d3de3ff` 紫 |
| `border` | 头部边框、日志分隔线 | `#5f87af` 钢蓝 |
| `tab_active` | 当前标签页文字 | `#7d3de3ff` 紫 |
| `tab_inactive` | 其他标签页文字 | `#626262` 灰 |
| `tab_background` | 当前标签页背景 | `#bdd4dfff` 浅蓝 |
| `help` | 帮助行、滚动位置提示、空列表提示 | `#a4cf64ff` 橄榄绿 |
| `patch` | patch 更新的目标版本号 | `#00d700` 绿 |
| `minor` | minor 更新的目标版本号 | `#ffd700` 黄 |
| `major` | major 更新的目标版本号 | `#00afff` 青 |
| `prerelease` | prerelease 版本号、错误状态、失败摘要边框 | `#ff0000` 红 |
| `selected` | 勾选标记 `✓` | `#7d3de3ff` 紫 |
| `cursor` | 光标 `▶` | `#7d3de3ff` 紫 |
| `popup_border` | 详情弹窗边框 | `#ff5faf` 粉 |
| `info` | 欢迎页信息标题、分析中状态 | `#5fffd7` 青 |
| `success` | 已更新标记、成功摘要边框、进度条 | `#a4cf64ff` 橄榄绿 |
| `log` | 日志正文 | `#cfd4b7ff` 浅卡其 |

注意 `prerelease` 一色两用：既是 prerelease 版本号的颜色，也是错误/失败的颜色。

### 颜色值格式

Hex 字符串，交给 lipgloss 解析：

- `#RRGGBB` —— 推荐，如 `#5f87af`
- `#RGB` —— 简写，如 `#f5a` 等价于 `#ff55aa`
- `#RRGGBBAA` —— 8 位形式可以接受，**alpha 会被忽略**（终端没有 alpha 通道）。默认配置里的 `ff` 后缀就是这种，纯属历史遗留，去掉效果完全一样

**无法解析的值不会报错，而是渲染成无样式的普通文本。** 所以颜色"丢了"通常意味着值写错了（比如漏了 `#`、用了 `rgb(...)` 或颜色名），不是配置没加载。

### 主题示例

只列需要覆盖的键即可。

#### 冷色低饱和
```json
{
  "title": "#7aa2f7",
  "tab_active": "#7aa2f7",
  "tab_background": "#1f2335",
  "cursor": "#7dcfff",
  "selected": "#9ece6a",
  "help": "#565f89",
  "log": "#a9b1d6"
}
```

#### 高对比度（弱视/强光环境）
```json
{
  "title": "#ffff00",
  "tab_active": "#000000",
  "tab_background": "#ffff00",
  "patch": "#00ff00",
  "minor": "#ffff00",
  "major": "#00ffff",
  "prerelease": "#ff00ff",
  "cursor": "#ffffff",
  "log": "#ffffff"
}
```

#### 单色（灰度终端）
```json
{
  "title": "#ffffff",
  "tab_active": "#000000",
  "tab_background": "#ffffff",
  "patch": "#e0e0e0",
  "minor": "#c0c0c0",
  "major": "#a0a0a0",
  "prerelease": "#808080",
  "cursor": "#ffffff",
  "help": "#a0a0a0",
  "log": "#c0c0c0"
}
```

#### 色觉友好（不靠红/绿区分更新类型）
```json
{
  "patch": "#0173b2",
  "minor": "#de8f05",
  "major": "#cc78bc",
  "prerelease": "#ca9161"
}
```

更新类型除了颜色外，在标题栏还有 `■ major / ■ minor / ■ patch / ■ pre_release` 图例可对照。

### 自定义颜色

1. 编辑配置文件：
   ```bash
   vim ~/.go-mod-select/colors.json
   ```
2. 保存 —— 配置在**启动时**读取，需要重新运行程序才生效
3. 改坏了直接删掉，下次运行会重建默认配置

### 重置配置

```bash
rm ~/.go-mod-select/colors.json
```

## 故障排查

### 日志文件未创建

日志失败不会中止程序，只会向 stderr 打印一行 `warning: file logging disabled: ...`。检查主目录权限：

```bash
ls -ld ~/.go-mod-select
mkdir -p ~/.go-mod-select && chmod 755 ~/.go-mod-select
```

### 颜色配置不生效

1. 先确认 JSON 合法 —— 格式错误会让程序回退到默认配置，并在 stderr/日志留下警告：
   ```bash
   jq . ~/.go-mod-select/colors.json
   ```
2. 确认颜色值本身合法。**无法解析的值渲染成无样式文本，不会报错**，所以某处颜色消失通常是值写错了：
   ```bash
   # 找出不符合 #RGB / #RRGGBB / #RRGGBBAA 的值
   jq 'to_entries[] | select(.value | test("^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$") | not)' ~/.go-mod-select/colors.json
   ```
3. 确认改完重启了程序（配置只在启动时读一次）
4. 重置：
   ```bash
   rm ~/.go-mod-select/colors.json
   ```

### 颜色显示不对 / 全是近似色

lipgloss 会按终端能力降级：真彩终端显示精确的 hex，256 色终端取最近似色，单色终端则完全不上色。

```bash
echo $TERM          # 期望 xterm-256color 之类
echo $COLORTERM     # truecolor / 24bit 表示支持真彩

# 临时启用 256 色
export TERM=xterm-256color
```

在 CI 或管道中输出会被自动去色，这是刻意行为。

### 弹窗/列表排版错乱

界面按终端宽高渲染，过窄或过矮的窗口会挤在一起。建议至少 100×30。放大窗口后界面会自动重排（TUI 会响应尺寸变化）。
