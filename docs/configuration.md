# 配置说明

## 配置文件位置

```
~/.go-mod-select/
├── app.log        # 应用日志（JSON 格式）
└── colors.json    # TUI 颜色配置
```

首次运行时自动创建配置目录和默认配置文件。

## 日志配置

### 日志格式

采用 JSON 格式的结构化日志：

```json
{"time":"2026-01-15T10:30:00Z","level":"INFO","msg":"go-mod-select started"}
{"time":"2026-01-15T10:30:01Z","level":"INFO","msg":"target version","version":"1.25"}
{"time":"2026-01-15T10:30:02Z","level":"ERROR","msg":"module not found","module":"example.com/foo"}
```

### 日志级别

- **DEBUG** - 详细调试信息
- **INFO** - 一般操作信息
- **WARN** - 警告信息（不影响运行）
- **ERROR** - 错误信息

### 查看日志

```bash
# 实时查看日志
tail -f ~/.go-mod-select/app.log

# 使用 jq 格式化
tail -f ~/.go-mod-select/app.log | jq .

# 只看错误
grep '"level":"ERROR"' ~/.go-mod-select/app.log | jq .

# 查看特定模块的日志
grep 'github.com/spf13/cobra' ~/.go-mod-select/app.log | jq .

# 清空日志
> ~/.go-mod-select/app.log
```

## 颜色配置

### 配置文件

位置：`~/.go-mod-select/colors.json`

```json
{
  "title": "#ff5faf",
  "header": "#5f87af",
  "border": "#5f87af",
  "tab_active": "#ff5faf",
  "tab_inactive": "#626262",
  "tab_background": "#303030",
  "help": "#626262",
  "patch": "#00d700",
  "minor": "#ffd700",
  "major": "#00afff",
  "prerelease": "#ff0000",
  "selected": "#ff5faf",
  "cursor": "#ff87d7",
  "popup_border": "#ff5faf",
  "info": "#5fffd7",
  "success": "#00d700",
  "log": "#949494"
}
```

### 配置项说明

| 配置项 | 说明 | 默认颜色 |
|--------|------|---------|
| `title` | 标题颜色 | #ff5faf（粉色） |
| `header` | 头部边框 | #5f87af（紫色） |
| `border` | 边框 | #5f87af（紫色） |
| `tab_active` | 活动标签页 | #ff5faf（粉色） |
| `tab_inactive` | 非活动标签页 | #626262（灰色） |
| `tab_background` | 标签页背景 | #303030（深灰） |
| `help` | 帮助文本 | #626262（灰色） |
| `patch` | Patch 更新 | #00d700（绿色） |
| `minor` | Minor 更新 | #ffd700（黄色） |
| `major` | Major 更新 | #00afff（青色） |
| `prerelease` | Prerelease 更新 | #ff0000（红色） |
| `selected` | 选中项 | #ff5faf（粉色） |
| `cursor` | 光标 | #ff87d7（淡粉色） |
| `popup_border` | 弹窗边框 | #ff5faf（粉色） |
| `info` | 信息文本 | #5fffd7（青色） |
| `success` | 成功提示 | #00d700（绿色） |
| `log` | 日志文本 | #949494（灰色） |

### 颜色值格式

使用 RGB 16 进制格式（Hex Color）：

- **完整格式**：`#RRGGBB`（如 `#ff5faf`）
- **简写格式**：`#RGB`（如 `#f5a` 等价于 `#ff55aa`）

**示例**：
- `#ff0000` - 纯红色
- `#00ff00` - 纯绿色
- `#0000ff` - 纯蓝色
- `#ffffff` - 白色
- `#000000` - 黑色
- `#808080` - 灰色

### 主题示例

#### 深色主题（默认）
```json
{
  "title": "#ff5faf",
  "patch": "#00d700",
  "minor": "#ffd700",
  "major": "#00afff",
  "prerelease": "#ff0000"
}
```

#### 浅色主题
```json
{
  "title": "#8a2be2",
  "patch": "#008000",
  "minor": "#b8860b",
  "major": "#00008b",
  "prerelease": "#8b0000"
}
```

#### 单色主题（灰度）
```json
{
  "title": "#ffffff",
  "patch": "#e0e0e0",
  "minor": "#c0c0c0",
  "major": "#a0a0a0",
  "prerelease": "#808080"
}
```

#### 夜间护眼主题
```json
{
  "title": "#87ceeb",
  "patch": "#90ee90",
  "minor": "#ffffe0",
  "major": "#add8e6",
  "prerelease": "#ffb6c1"
}
```

#### 高对比度主题
```json
{
  "title": "#ffff00",
  "patch": "#00ff00",
  "minor": "#ffff00",
  "major": "#00ffff",
  "prerelease": "#ff00ff"
}
```

### 配色工具推荐

- [Coolors](https://coolors.co/) - 在线配色方案生成器
- [Color Hunt](https://colorhunt.co/) - 配色方案灵感
- [Adobe Color](https://color.adobe.com/) - 专业配色工具
- [HTML Color Picker](https://htmlcolorcodes.com/color-picker/) - 在线取色器

### 自定义颜色

1. 编辑配置文件：
   ```bash
   vim ~/.go-mod-select/colors.json
   ```

2. 修改颜色值（0-255）

3. 保存并重新运行程序，颜色立即生效

### 重置配置

删除配置文件，下次运行时自动创建默认配置：
```bash
rm ~/.go-mod-select/colors.json
```

## 故障排查

### 日志文件未创建

检查主目录权限：
```bash
ls -la ~ | grep .go-mod-select
mkdir -p ~/.go-mod-select
chmod 755 ~/.go-mod-select
```

### 颜色配置不生效

1. 检查 JSON 格式：
   ```bash
   cat ~/.go-mod-select/colors.json | jq .
   ```

2. 查看错误日志：
   ```bash
   grep '"level":"ERROR"' ~/.go-mod-select/app.log | tail -1 | jq .
   ```

3. 重置配置：
   ```bash
   rm ~/.go-mod-select/colors.json
   ```

### 终端不支持 256 色

检查终端支持：
```bash
echo $TERM
# 应该显示 xterm-256color 或类似

# 设置终端为 256 色模式
export TERM=xterm-256color
```
