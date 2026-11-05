---
name: ompinyin-install
description: >
  Install, repair, or diagnose the ompinyin Chinese input method stack on an
  Omarchy machine, converging to "full pinyin + wanxiang LMDG model + Omarchy's
  own top-bar IM indicator (on the bar) + candidate window following the
  Omarchy theme", idempotently and reversibly. Use when asked to install the
  Chinese IME, fix an ompinyin install, "can't type Chinese / no Chinese
  output", a missing (or duplicated) top-bar IM icon, an IM group containing
  the wrong engines, or to diagnose input-method problems — a candidate window that is too
  small in X11/XWayland apps (WeChat, Baidu Netdisk), WeChat / other
  non-Wayland apps whose UI and fonts are too large from HiDPI double-scaling,
  a lock screen that rejects the password after full-width (全角) input, or a
  stuck 中/英 switch. Triggers: ompinyin, install Chinese input, fcitx5, rime,
  装中文输入法, 打不出中文, no Chinese IME, 候选框太小, 微信字太大, HiDPI,
  XWayland, 全角, 锁屏密码, 切不了中英文.
---

# ompinyin 中文输入法助手（安装 / 排障）

你是运维助手：在一台**已登录的 Omarchy 图形会话**上跑 `ompinyin`，把它收敛到
「全拼 + 万象整句模型 + 顶栏输入法指示器在栏上 + 候选框跟随 Omarchy 主题」，幂等、可回滚。
**Omarchy 4.5+**：热键、顶栏指示器、候选框配色都由 Omarchy 提供，ompinyin 只保证它们到位并清理老版本
自己装过的产物（notificationitem drop-in / `Fcitx` SNI pin / theme-set 钩子）。
只跑工具，**不改代码、不碰仓库**。诊断是只读的：`status` / `doctor` 永远安全，
不会落盘或收敛。

## 前置

- 主机必须 **Omarchy**（`ID=omarchy`）；其它发行版会被预检拒绝（exit 3）。
- 用**普通用户**跑，**勿 root**——只有 `pacman` / `ompinyin source` 会 `sudo` 提权。
- 万象模型 ~420MB，预检要求空闲 ≥2GB。
- 一台机器只需跑一次；重跑是安全的（幂等）。
- 二进制旧了先升级：`ompinyin update --self`（v1.3 前的版本有两个已知缺陷：登录时
  `Xft.dpi` 从不发布、右侧 Shift 可能失效，见「排障」第一条）。

## 流程

```text
0) 没有就装：Releases 直链 / gh-proxy 前缀 / 源码 make build（见 README「安装」）
1) ompinyin status --json
2) ompinyin install --dry-run --json   → 读 plan，确认 plan.needsApply
3) ompinyin install -y                 # 收敛：全拼 + 万象 + 顶栏图标 + 候选框跟随主题
4) ompinyin status && ompinyin doctor  # 须无差异、全通过
5) 人工核对：
     · 左 Shift       Omarchy 4.5+ 的「开/关输入法」：左 Shift 是 fcitx5 的 AltTriggerKeys（上游默认），
                   只翻 active（不改组内 IM）；rime 侧被我们显式写成 `Shift_L: noop`，不会与引擎内切换打架
     · Super+I        Omarchy 4.5+ 的「换 IM」：rime ↔ keyboard-us 循环
     Super+I 是 `omarchy-input-method cycle`。判据是按键前后 `fcitx5-remote` 的 IM 名是否
     `rime` → `keyboard-us`；组内只有 rime 时**都不动作**（fcitx5 的 `canTrigger()` 要求条目数 > 1）。
     4.0（4.5 之前）上对应的是 `Ctrl+Space`（那时热键由 ompinyin 写；herdr 前缀应为 `ctrl+b`，dotfiles 侧）
     · 右 Shift    在 rime 内切中英（受管手感项 `commit_code`；不依赖输入法开关）。
                   左 Shift 在 4.5+ 不再属于 rime——写 `Shift_L: commit_code` 也永不触发
     · F4          切 Rime 方案（全拼 ↔ 双拼；fcitx5 的输入法列表里只有一项 "Rime"）
     · 顶栏有一个（且只有一个）输入法指示器：Omarchy 自绘的 `omarchy.keyboard-layout`；
                   候选框是当前主题配色
```

**完成信号**：`plan.needsApply:false`（或 `status` 无差异）= 没事可做，**停止，别再跑**。

## 受管手感项（v1.3 起）

这些由 ompinyin 生成，**不要手改**；改了下次收敛会被覆盖（有备份）：

| 项 | 值 | 效果 |
|---|---|---|
| `menu/page_size` | 9 | 每页 9 个候选 |
| `,` `.` | `Page_Up/Page_Down` | 翻页 |
| `ascii_composer/switch_key/Shift_R` | `commit_code` | 右 Shift 在 rime 内切中英（先上屏已打编码） |
| `ascii_composer/switch_key/Shift_L` | `noop` | 左 Shift 让给 fcitx5 的 AltTriggerKeys。不写会回落到 rime 出厂的 `commit_code`，两边抢同一个键 → 同一按键有时进 rime、有时进英文键盘 |
| `Control_L/R` | `noop` | Ctrl 尾随不误切 |
| `good_old_caps_lock` | `true` | CapsLock 只切大写 |
| `switches/@4/reset` | `0` | **全半角永远从半角起步**（写进每个方案的 custom.yaml） |
| `switcher/save_options` | 4 项（**不含** `full_shape`） | 全角不跨会话记忆；与上一条成对 |
| `[Hotkey/TriggerKeys]` | 4.0（4.5 之前）：只有 `Control+space`；4.5+：**不写** | 4.5 起热键归 Omarchy（`Super+I` / 左 Shift）；ompinyin 只删旧版残留的 `Control+space`/`Alt+space`，且绝不碰 `[Hotkey/AltTriggerKeys]` |
| `switcher/hotkeys` | `[F4]` | 上游一次绑了 F4 + `Ctrl+\`` 家族；`Ctrl+\`` 与 VS Code 终端、JetBrains quick-switch 冲突，只留 F4 |

仍归用户：`ascii_punct`（中英标点）/ 简繁 / emoji / 单字 的**默认值**，以及其它修饰键。

## X11 HiDPI / 候选框大小（默认只诊断）

Wayland 原生应用的候选框正常，但 **X11/XWayland 应用（微信、百度网盘；输入上下文挂在
`x11::0`）里的候选框偏小**：fcitx5 classicui 的 X11 UI 只读 X 资源 `Xft.dpi`
（XWayland 的 RandR 恒上报 96，`PerScreenDPI` 在 XWayland 上被强制忽略）。
所以需要全局 `Xft.dpi = round(96 × 显示器缩放)`。

- **默认只读诊断**：`ompinyin status` / `doctor` 展示 scale、期望/实际 `Xft.dpi`、
  残留产物；不写文件、不装包。先确认候选框确实过小再启用。
- **启用**：`ompinyin install --x11-hidpi`（持久化进 `Desired.X11HiDPI`）。它会装
  `xorg-xrdb`、在 `~/.Xresources` 写 marker 块（**只拥有块内 `Xft.dpi`**，保留其余
  用户行）、装 `ompinyin-x11-hidpi.{service,path}`（登录发布 + 监听 `monitors.lua`），
  并重启 fcitx5 一次。之后改显示缩放会自动跟随，无需手动。
- **撤销**：`ompinyin install --no-x11-hidpi`（移除受管块 + 两个单元；已发布到当前
  会话的值保留到注销）。
- `--dry-run --json` 的 `plan.need.hidpi` 同时覆盖 opt-in 收敛与 opt-out 撤销。

**前置是 Hyprland `xwayland:force_zero_scaling`**（Omarchy 默认 `true` = X11 窗口
**不由合成器缩放**，各 toolkit 自己缩）。若为 `false`（合成器已把 X11 整体放大），
本模式会**自动变为 no-op** 并在 `doctor`/plan 说明原因——**不要试图强行发布**，否则
候选框与所有 X11 应用会二次放大到 4×。若 `doctor` 说读不到该选项（按默认 true 继续），
说明 `hyprctl` 不可用，结论是"未验证"。

**`Xft.dpi` 是全局 X11 缩放权，不是候选框私有开关。** 凡把它当逻辑 DPI 的 X11 客户端
都会受影响；若某个框架又自带显式缩放因子，就会**二次放大**。

- 规则：**一个框架只能有一个缩放权，禁止叠加。**
- **GTK3/X11 是当前最容易被坑的组合**：Omarchy 自带的 `~/.config/hypr/monitors.lua`
  默认就写 `hl.env("GDK_SCALE", "2")`，而 GTK3 的 X11 后端会**同时**应用 `GDK_SCALE`
  （控件）与 `Xft.dpi`（字体）⇒ 约 4×。`doctor` 会直接点出来：
  `⚠ 会话里已有显式缩放因子 GDK_SCALE=2，读 Xft.dpi 的 X11 客户端（GTK3/Qt5）会与它叠加（约 4×）`。
  处理方式二选一，**别两个都留**：把 `monitors.lua` 的 `omarchy_gdk_scale` 降到 1，
  或干脆不发布 `Xft.dpi`。
- Qt：删掉显式 `QT_SCALE_FACTOR`，改用 `QT_AUTO_SCREEN_SCALE_FACTOR=1`（跟随
  `Xft.dpi`）或 `QT_SCREEN_SCALE_FACTORS=<scale>`（显式覆盖 DPI 派生因子）——二者不并用。
- Chromium/Electron（百度网盘等）：**不读** `Xft.dpi`（二进制里没有该字面量），
  所以要放大本体得用 `--force-device-scale-factor=<scale>`（留在 XWayland）或
  `--ozone-platform-hint=wayland`（换原生 Wayland）。**这两条路线互斥**，同时加 = 4×。
- **不要用「删掉全局 `Xft.dpi`」来修某个应用**——那会让候选框又变小。

**冲突与所有权**：ompinyin 只拥有 `~/.Xresources` 的 marker 块，**绝不删除块外的
`Xft.dpi` 行**。`xrdb -merge` 按文件顺序、后者胜；块外存在冲突时 `x11ForeignDpi=true`
（`status`/`doctor`/`--json` 会提示），若它排在块之后，`install` 会在 L5 报
「期望≠实际」并 exit 1（确定性失败，不是静默错值）。`xrdb` 不可用时回退
`xprop -root RESOURCE_MANAGER` 读取。`Xft.dpi` 属**每个 X 会话**（不是每 app、
也不跨 OS 用户），运行中第三方改动无实时守护，下次 `install`/`doctor`/登录/缩放变化纠正。

**若 `~/.Xresources`、`~/.config/fcitx5`、两个 `ompinyin-x11-hidpi.*` unit 或
`rime/*.custom.yaml` 同时被 dotfiles 工具（chezmoi 等）纳管**：先让 dotfiles 退出，
否则那边一 `apply` 就把收敛结果回退（v1.3 起本机已清理；判据
`chezmoi managed | rg 'Xresources|fcitx5|ompinyin|rime/.*custom'` 应为空）。

`ompinyin x11-hidpi-apply` 是 systemd 私有入口，**勿手动跑**——它不解析参数，
加 `--help` 也会真的发布并重启 fcitx5。

## 红线（勿越）

- 只在 **Omarchy** 跑；普通用户、勿 root。
- 勿设 `GTK_IM_MODULE` / `~/.xprofile` 等 IM 环境变量（Wayland 反模式）。
- 勿手改 `~/.config/fcitx/rime` 与 `~/.config/omarchy/shell.json`——4.0 上工具会读→并→写回（`tray.SetPinned`），
  4.5+ 只做「去掉 `Fcitx`」（`tray.RemovePinned`）。
- 勿跑 `pacman -Syu`（会漂移 Omarchy stable 锁版本；只用 `-Sy` / `-S --needed`）。
- 顶栏输入法指示器是**必做终态**，无 `--tray-pin`/`--no-tray`。4.5+ 上由 Omarchy 自绘：工具跑
  `omarchy bar put omarchy.keyboard-layout`（需外壳在跑）并**删掉**老版本的 drop-in/pin；**别为了让它变绿去手改 `shell.json`**。
- 候选框跟随 Omarchy 主题也是**默认终态**（无 flag）：ompinyin 只拥有 `classicui.conf` 的 6 个键（其余键保留）；
  配色由 Omarchy 的 `omarchy-theme-set-fcitx5` 生成 `~/.local/share/fcitx5/themes/omarchy/`（**不入账、卸载不删**）。
  旧版 ompinyin 装的 `theme-set.d/fcitx5-theme` 钩子与它并发写同一目录，收敛会删掉——**别把它装回去**。
- **勿手改受管文件**：`rime/*.custom.yaml`、`classicui.conf`、`~/.config/fcitx5/profile`（组形状由 `desired.ims` 声明）、两个 `ompinyin-x11-hidpi.*` unit、`~/.Xresources` 的 marker 块。要改行为就改 `ompinyin install` 的参数。4.5+ 上 `~/.config/fcitx5/config` 归 Omarchy，ompinyin 只删自己写过的触发键。
- `--x11-hidpi` 是**显式 opt-in**：未显式给出时绝不写 `~/.Xresources`、不装 `xorg-xrdb`、不装单元；启用后**勿再给应用叠加显式缩放因子**（见上）。
- 勿手改 `~/.Xresources` 里 marker 块之外的内容，勿删别人的 `Xft.dpi`（行级拥有）。
- **勿在 fcitx5 服务停止时用 `fcitx5-remote`/总线命令探测存活**（`fcitx5-remote -n`/`-q` 也算）：`org.fcitx.Fcitx5` 是唯一总线名，这一步会 D-Bus 激活一个游离 fcitx5 占住它，单元的实例从此启动即退出。存活只用 `pgrep -x fcitx5`；`ompinyin` 的 `status`/`doctor`/`install` 都是安全的（内部只用进程探针）。

## 排障

- **X11 应用（微信 / 百度网盘）里候选框太小**：`ompinyin doctor` 看 X11 行。
  - `期望=192 实际=192` → 已发布；候选框仍小则是该应用的缩放问题（见下一条 4×）。
  - `实际=0` 且日志里有 `[跳过] … xrdb -query 失败`/`未检测到可用的 X 会话` → **旧版本的登录发布缺陷**（单元启动时 `DISPLAY` 还没被导入 systemd user manager）。升级到 v1.3+；应急可先在自己终端的图形会话里跑一次 `ompinyin install --x11-hidpi -y`（当天有效），再升级。
  - 根窗口判据：`xprop -root RESOURCE_MANAGER` 应含 `Xft.dpi: 192`。
- **某个 X11 应用整体变大（图标+文字，约 4×）**：`Xft.dpi` 与该框架的显式缩放因子叠加。
  看 `doctor` 的 `⚠ 会话里已有显式缩放因子 …`；GTK3-on-X11 的话去 `~/.config/hypr/monitors.lua`
  把 `omarchy_gdk_scale` 降到 1（或撤销 `--x11-hidpi`）。Qt 应用改用
  `QT_SCREEN_SCALE_FACTORS=<scale>`。**不要删 `Xft.dpi`**。
- **锁屏后密码输不对、进不去系统（曾经在全角状态下中招）**：rime 的 `full_shape`
  会把**字母数字也变全角**（`ａｂｃ`），而锁屏密码框接受 IME 输入、PAM 只做字符比对。
  v1.3 起已受管（全角永远从半角起步、不跨会话记忆）。若仍发生：先在另一个 TTY
  （`Ctrl+Alt+F3`）改密码为纯字母数字、或临时用 `KEYMAP` 布局登录；再确认
  `ompinyin doctor` 的「手感项（全半角）」是 `[完成]`。
- **`doctor` 报「手感项（全半角）」未达标**：上游 rime-ice 重排了 `switches`，补丁的
  下标（`switches/@4`）打到了别的开关。**不要手改**，升级 ompinyin（改
  `catalog.FullShapeSwitchIndex`）。
- **`doctor` 报 X11 HiDPI 未达标**：`Xft.dpi 实际≠期望`（可能第三方改了根属性，或块外
  `Xft.dpi` 获胜）——重跑 `install`；若提示 `force_zero_scaling=false`，则本模式不适用。
- **`Ctrl+Space` 和 herdr 抢键（4.0）**：那里输入法用 `Ctrl+Space`（传统键），herdr 的 prefix 应为 `ctrl+b`
  （`~/.config/herdr/config.toml` 的 `[keys] prefix`，改完 `herdr server reload-config`）。
  **新装 Omarchy 出厂就是 `ctrl+space`** ⇒ 一定会撞；`ompinyin doctor` 会告警（判据：
  `observe.HerdrPrefix` 读 `$HERDR_CONFIG_PATH` → `~/.config/herdr/config.toml` → 内置 `ctrl+b`）。
  ompinyin **只告警**，不改她的配置、也不偷偷换触发键。方案切换是 `F4`（`Ctrl+\`` 家族被移除，会与编辑器冲突）。
- **键按了没反应**：判据是按键前后 `fcitx5-remote` 是否 `2 → 1`。不影响打字（右 Shift 在 rime 内切中英），
  需要关输入法时可用顶栏指示器菜单或 `fcitx5-remote -t`。
- **切不到「纯英文键盘」IM**：profile 里只有 `rime`（v1.3 起不再注册 `keyboard-us`）。
  `Ctrl+Space` 停用 fcitx5 即等于用键盘布局直接输英文；要独立英文 IM 得自己加。
- **L1 装包失败 / 镜像慢**：`ompinyin source`（默认 `--preset cn`，内部 `sudo` 写 `/etc/pacman.d/mirrorlist`，勿 `sudo ompinyin source`）后重跑 `install`。
- **打不出中文**：多为 `default.custom.yaml` 的 `schema_list` 被改成裸 `- <id>` 短格式（`rime_deployer --build` 会忽略，只出 `build/default.yaml` 骨架）。重跑一次 `ompinyin install` 会重写为 `- schema: <id>` map 格式。
- **顶栏没有输入法指示器**：4.5+ 上跑 `omarchy bar put omarchy.keyboard-layout`（或 `ompinyin install`）；
  `ompinyin doctor` 的「顶栏输入法指示器」行给出精确修复。外壳没在跑时只能靠 Omarchy 默认配置。
- **顶栏有两个输入法图标**：老版本 ompinyin 的 notificationitem drop-in / `Fcitx` pin 还在（`doctor` 的
  `staleTrayArtifacts` / 「检测到旧产物」）——跑一次 `ompinyin install` 就会清掉。
- **顶栏图标在 ◀ 抽屉里（4.0）**：只启用了 notificationitem 没 pin `Fcitx`——重跑 `install` 补 pin。
- **`doctor`/`status` 报「IM 三态：fcitx5 服务未运行 + 非单元 fcitx5 占着 org.fcitx.Fcitx5」**（常伴顶栏图标消失、打不出中文）：服务停止时被 D-Bus 激活的游离实例占住了总线名，单元自己 spawn 的实例启动即退出 0，`Restart=always` 把它拖成死循环。重跑 `ompinyin install` 会自动清理再启动（v1.2.2 起；旧版只提示你手跑 `pkill -x fcitx5` 再 `systemctl --user reset-failed … && start …`）。见「红线」——以后别在服务停止时手跑 `fcitx5-remote`。
- **doctor 报「候选框主题」未达标**：`classicui.conf` 被手改/缺失，或**旧 theme-set 钩子还在**（与 Omarchy 的生成器并发写）——重跑 `ompinyin install` 会按所有权协议修复并删掉钩子；4.5 之前的 Omarchy 没有 `omarchy-theme-set-fcitx5`，此时候选框保持 fcitx5 默认外观，属预期。
- **升级到 Omarchy 4.5 后行为变化**：左 Shift 从「rime 内切中英」变成「开/关输入法」（上游 `AltTriggerKeys=Shift_L`），rime 内中英改右 Shift，换 IM 用 `Super+I`；`Ctrl+Space` 不再切输入法（留给终端）。这是 4.5 的既定键位，不要试图改回。
- **左 Shift 「时灵时不灵」**：那是 `Shift_L: noop` 缺失的老状态（rime 出厂 `commit_code` 与 fcitx 的 AltTrigger 抢同一个键）。跑一次 `ompinyin install -y`，然后确认 `default.custom.yaml` 里有 `Shift_L: noop`。
- **首次安装**会接管 `default.custom.yaml`（候选数 9、`,` `.` 翻页、手感项），改动前已备份到 `backup-<ts>/`。
- **headless**：`-y` + 给 pacman 配 NOPASSWD sudoers（工具用 `sudo -n`）。

## 退出码

`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检拒绝（非 Omarchy / root / 缺工具 / 磁盘不足）· 第二次 SIGINT 硬退出 `130`。

## 说明

- 本文件遵循 [Agent Skills 标准](https://agentskills.io/specification)，按需加载；事实与 README「AI / Agent 装机」一致，冲突时以 README / `ompinyin --help` 为准。
- 仓库内的详细文档：`docs/design.md`（设计方案）、`docs/qa.md`（用户问答，可直接转述给用户）、`docs/dev-notes.md`（开发问题与决策）。
- 跨 harness 位置：仓库内 `.agents/skills/ompinyin-install/`；装到本机全局则放入 `~/.pi/agent/skills/`、`~/.claude/skills/`、`~/.codex/skills/` 等对应目录。
