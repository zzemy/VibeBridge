# VibeBridge 需求与实现说明

## 一、核心概念：这个项目到底在干嘛？

你可以把这个项目理解为**“文字版的 TeamViewer”**。

普通的远程控制（如 TeamViewer）是把整个电脑屏幕录制成视频，通过网络发给手机，这在手机上极其卡顿且难操作。

而 VibeBridge 只传输文字和按键：

- 把电脑命令行里 AI 吐出来的文字、颜色和终端控制字符传给手机。
- 把你在手机上敲下的按键、快捷键和控制输入传回电脑。

这样，你在手机上就能低延迟地操作电脑上的 AI 编程工具（如 Claude Code 或 Codex CLI）。

项目边界：

- VibeBridge 不做完整屏幕共享。
- VibeBridge 不做远程文件浏览器。
- VibeBridge 不做多人协作。
- 第一阶段只做一件事：**安全地把一个本地终端会话映射到手机浏览器**。

## 二、这个项目由哪三部分组成？（核心角色）

项目运行起来后，只有三个角色在协同工作：

1. **手机浏览器**

   相当于“遥控器屏幕”，负责显示终端内容，并收集你的按键。前端使用 React + TypeScript 生态实现，第一版建议用 Vite 构建静态 SPA。

2. **电脑上的 Go 程序**

   相当于“传话筒/管道”，负责在中间拉线，把手机和 AI 工具连接起来。

3. **Claude Code/Codex（AI 工具）**

   相当于“打工人”，在电脑本地真正看代码、改代码、运行测试。

## 三、具体的项目实现逻辑（一步一步是怎么跑起来的？）

我们用一个具体的场景（比如你想在手机上命令 AI 写一个 Go 语言的限流器）来梳理整个项目的运行逻辑。

### 第一步：配对与连接（握手）

你在电脑上启动写好的 Go 程序。

Go 程序在电脑控制台上打印出一个二维码。

你用手机扫码，手机浏览器会自动打开一个网页，并与电脑上的 Go 程序建立一条实时双向通信通道（WebSocket）。

二维码里必须带一个一次性 `session token`。手机连上 WebSocket 后，Go 程序必须校验这个 token，不能让局域网里的其他设备随便连进来控制终端。

### 第二步：在电脑后台打开“虚拟终端”

一旦手机连接成功，Go 程序立刻在电脑后台启动指定命令。这个命令不要写死成 `claude`，而是做成可配置参数，例如：

```powershell
vibebridge --cmd "codex"
vibebridge --cmd "claude"
vibebridge --cmd "powershell"
```

Go 程序不是简单地启动它，而是把它启动在一个**“虚拟终端（PTY）”**里。

为什么要用虚拟终端？因为像 Claude Code 或 Codex CLI 这种工具，它需要知道自己是在一个“有屏幕”的地方运行，才能画出彩色的菜单、进度条和交互界面。虚拟终端能让这些工具按真实终端方式运行。

注意：Windows 上的 PTY 是一个关键技术风险点。Unix/macOS 通常使用 PTY，Windows 需要考虑 ConPTY。实现前要先验证 Go 依赖库是否能稳定支持目标系统。

### 第三步：核心控制循环（数据是怎么流动的？）

这是项目最核心的实现逻辑，分为“输出流”和“输入流”。

#### 输出流：电脑 -> 手机

电脑后台的 Claude Code 或 Codex 开始工作，修改了代码，并在终端输出了一行带颜色的字：

```text
我已经帮您写好了限流器，是否需要运行测试？(y/n)
```

Go 程序立刻从虚拟终端里把这行字抓取出来。这里抓到的不只是普通文字，还包括控制颜色、光标位置和界面布局的 ANSI 控制字符。

Go 程序顺着 WebSocket 通道，把这些终端输出字节发给手机。

手机网页上的终端渲染器（xterm.js）收到输出后，在手机屏幕上渲染出彩色交互界面。

#### 输入流：手机 -> 电脑

你在手机屏幕上看到了 AI 的提问，想同意它运行测试。

因为手机上敲键盘不方便，你可以直接点击网页下方自定义的 “Y” 快捷键。

手机网页通过 WebSocket 发送字符 `y` 给 Go 程序。

Go 程序收到后，把字符 `y` 写进后台虚拟终端的输入流里。

Claude Code 或 Codex 以为是坐在电脑前的真人敲下了 `y`，于是立刻在电脑本地开始运行测试命令。

### 第四步：安全退出

当你写完代码，在手机上关闭网页，长连接会断开。

但是手机锁屏、切后台、网络抖动也可能导致 WebSocket 临时断开，所以不建议一断开就立刻杀掉后台 AI 进程。

建议退出策略：

- 用户主动点击“结束会话”：立即向后台进程发送退出信号，并清理 PTY。
- WebSocket 意外断开：保留进程 30-120 秒，等待手机重连。
- 超过重连窗口仍未连接：向后台进程发送退出信号，防止 AI 进程在电脑后台无限挂机，消耗电脑 CPU 和大模型 Token。

## 四、安全模型

VibeBridge 的本质是“让手机控制电脑上的一个本地终端”，权限非常大，所以安全模型必须在 MVP 阶段就做。

最低要求：

- 二维码 URL 必须带一次性 `session token`。
- WebSocket 连接必须校验 `session token`。
- token 只能使用一次，或者只在当前会话内有效。
- Go 程序默认只服务当前局域网或指定网卡，不暴露到公网。
- 控制台显示当前访问地址和已连接设备信息。
- 手机页面提供明确的“结束会话”按钮。
- Go 程序提供空闲超时，避免无人操作时长时间挂起。
- 不在日志里记录完整 token、敏感路径、命令输出中的私密信息。

可选增强：

- 启动时支持 `--bind 127.0.0.1`、`--bind 0.0.0.0` 或指定局域网 IP。
- 支持 `--idle-timeout` 和 `--reconnect-timeout`。
- 支持只允许一个手机连接。
- 后续如需公网访问，必须增加 HTTPS/WSS、认证和更严格的访问控制。

## 五、WebSocket 消息协议

不要只把协议描述成“传文字/传按键”，因为终端交互本质上是字节流，还会涉及 resize、心跳和退出状态。

建议最小协议：

```text
output  Go -> Browser  PTY 输出字节，包含文字、颜色和 ANSI 控制字符
input   Browser -> Go  手机输入字节，如普通字符、Enter、Ctrl+C
resize  Browser -> Go  终端尺寸变化，如 cols/rows
ping    双向           心跳保活
exit    Go -> Browser  后台进程退出状态
error   Go -> Browser  稳定错误信息
```

实现方式可以先简单处理：

- 终端输出可以走 WebSocket binary frame，尽量保持原始字节。
- 如果前端处理二进制不方便，可以临时使用 base64 包装。
- JSON 适合传 `resize`、`exit`、`error` 这类结构化消息。
- 不要假设所有 PTY 输出都是干净 UTF-8。

## 六、移动端输入体验

手机上操作终端最大的问题不是显示，而是输入。手机端输入应该分成两层：

1. **自然语言输入框（composer）**

   用来输入较长的任务描述、修改要求、报错信息和普通聊天内容。

2. **快捷键栏**

   用来输入终端控制键、确认键和高频操作。

自然语言输入框是手机端的核心输入方式。用户应该能先在输入框里完整编辑一句话，再点击“发送”一次性写入 PTY，而不是把手机软键盘的每个按键都实时塞进终端。这样可以减少误触，也更适合输入长提示词。

建议行为：

- 输入框支持多行文本。
- 点击“发送”后，把文本写入 PTY，并默认追加一次 `Enter`。
- 支持“仅插入不发送”的高级模式，方便用户先把内容放进终端再手动确认。
- 发送前处理手机输入法的 composition 状态，避免中文、日文等输入法还没确认就提前发送。
- 输入框可以保留历史草稿，避免手机切后台后内容丢失。
- 对超长文本做长度提示或二次确认，避免误把大段内容粘进终端。

MVP 阶段就应该提供快捷键栏：

- `Enter`
- `Esc`
- `Ctrl+C`
- `Tab`
- 上、下、左、右方向键
- `Y`
- `N`
- 粘贴按钮
- 可选：常用命令片段

输入注意事项：

- 区分自然语言输入、普通终端文本输入和控制键输入。
- 粘贴内容要限制长度，避免误粘贴大段文本。
- 处理换行差异，例如 `\r`、`\n`、`\r\n`。
- 兼容手机输入法和软键盘弹起后的布局变化。

## 七、前端技术栈、终端尺寸与渲染

前端使用 React + TypeScript 生态，不需要 Next.js。这个项目的手机端页面是一个由 Go 程序托管的本地控制台，第一版用 Vite React SPA 更轻、更容易嵌入 Go 二进制。

样式体系建议使用 Tailwind CSS + shadcn/ui：

- Tailwind CSS 负责布局、间距、颜色、响应式和主题 token。
- shadcn/ui 负责按钮、输入框、弹窗、抽屉、提示、状态徽标等可访问组件。
- xterm.js 仍然负责终端主体渲染，不要把终端输出拆成 React 节点。
- 视觉风格应该是移动端优先、深色终端优先、信息密度高但不拥挤。
- shadcn/ui 只选择性安装需要的组件，不一次性引入大量无关组件。

建议前端结构：

```text
web/
  src/
    App.tsx
    components/
      ui/
        button.tsx
        textarea.tsx
        alert-dialog.tsx
        drawer.tsx
        badge.tsx
      TerminalView.tsx
      PromptComposer.tsx
      ShortcutBar.tsx
      ConnectionStatus.tsx
    styles/
      globals.css
    lib/
      protocol.ts
      terminalKeys.ts
```

核心组件边界：

- `TerminalView`：封装 xterm.js，负责渲染 PTY 输出、处理 resize。
- `PromptComposer`：自然语言输入框，负责多行编辑、composition 状态和发送行为。可以基于 shadcn/ui `Textarea` 和 `Button` 实现。
- `ShortcutBar`：快捷键栏，负责 `Enter`、`Esc`、`Ctrl+C`、方向键等控制输入。可以基于 shadcn/ui `Button` 实现。
- `ConnectionStatus`：显示连接状态、重连状态和结束会话按钮。可以基于 shadcn/ui `Badge`、`AlertDialog` 或 `Drawer` 实现。
- `protocol.ts`：定义 WebSocket 消息类型，避免前后端协议散落在组件里。

首批建议安装的 shadcn/ui 组件：

```powershell
npx shadcn@latest add button textarea alert-dialog drawer badge separator tooltip
```

样式原则：

- 主界面应该像一个精致的移动端终端控制台，而不是后台管理面板。
- 终端区域全屏优先，不要放进厚重卡片里。
- 底部固定自然语言输入框和快捷键栏，适配手机软键盘。
- 状态信息用轻量 badge/toast/顶部栏，不要打断终端操作。
- 结束会话、断线重连、超长粘贴这类风险操作用确认弹窗。
- 默认深色主题，保留清晰的焦点态、按压态和禁用态。

WebSocket 消息类型应该在 TypeScript 中建模，例如用 discriminated union 表达 `output`、`input`、`resize`、`exit`、`error` 等消息。

构建与托管方式：

- 开发阶段：Vite dev server 可单独运行，代理 WebSocket 到 Go server。
- 交付阶段：`vite build` 生成静态文件，由 Go HTTP server 托管。
- 后续打包：可以用 Go `embed.FS` 把前端构建产物打进 `vibebridge` 单个二进制。

终端渲染使用 xterm.js。

必须处理终端 resize：

1. 手机页面加载后计算当前可用宽高。
2. xterm.js 得到 `cols` 和 `rows`。
3. 浏览器把 `cols/rows` 通过 WebSocket 发给 Go。
4. Go 调整 PTY 的窗口大小。
5. 手机旋转、软键盘弹起、浏览器尺寸变化时重复上述流程。

如果不处理 resize，Claude Code 或 Codex 的 TUI 很容易出现换行错乱、菜单错位、进度条显示异常。

## 八、MVP 实现路线

建议按下面顺序推进，每一步都保持可运行：

1. Go 启动本地 HTTP server，手机能打开页面。
2. Go 生成二维码，二维码指向手机页面 URL。
3. 手机页面与 Go 建立 WebSocket，先完成 echo 测试。
4. Go 启动一个普通 shell，桥接 WebSocket 输入输出。
5. 把普通 shell 换成 PTY，验证彩色输出和交互工具是否正常。
6. 初始化 Vite React + TypeScript 前端，接入 Tailwind CSS、shadcn/ui 和 xterm.js，渲染终端输出。
7. 增加 `session token` 校验，防止未授权连接。
8. 增加移动端自然语言输入框，支持编辑后一次性发送到 PTY。
9. 增加移动端快捷键栏。
10. 增加 terminal resize。
11. 增加断线重连窗口和空闲超时。
12. 增加进程退出、错误状态和日志清理。
13. 最后再接入 `codex`、`claude` 等真实 AI 编程工具。

## 九、主要风险与提前验证项

需要提前验证的点：

- Windows ConPTY 是否稳定支持目标 AI CLI。
- xterm.js 在手机浏览器上的性能和布局是否可接受。
- WebSocket 断线重连时，PTY 和后台进程是否能正确保留。
- Claude Code/Codex 在 PTY 中是否能正常识别终端尺寸、颜色和交互输入。
- Ctrl+C、Esc、方向键、Tab 等控制输入是否能被正确传入。
- 手机切后台、锁屏、网络切换后是否会误杀会话。
- 局域网访问时 Windows 防火墙是否会阻止连接。
- 二维码里的本机 IP 是否选择正确，例如多网卡、VPN、虚拟网卡环境。

## 十、第一版验收标准

第一版完成后，至少应该能做到：

- 电脑运行 `vibebridge --cmd "codex"` 或类似命令。
- 控制台打印二维码。
- 手机扫码后打开终端页面。
- 手机能看到电脑 PTY 里的彩色终端输出。
- 手机能输入自然语言任务描述，并一次性发送给后台 AI CLI。
- 手机能输入普通文字、Enter、Esc、Ctrl+C、方向键。
- 手机旋转或尺寸变化后终端布局仍然正常。
- 断线短时间内可以重连。
- 用户主动结束会话后，后台进程被清理。
- 未携带正确 token 的 WebSocket 连接会被拒绝。
