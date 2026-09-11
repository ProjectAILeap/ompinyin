---
name: ompinyin-install
description: >
  Install, repair, or diagnose the ompinyin Chinese input method stack on an
  Omarchy machine, converging to "full pinyin + wanxiang LMDG model + top-bar
  icon + candidate window following the Omarchy theme", idempotently and
  reversibly. Use when asked to install the Chinese IME, fix an ompinyin
  install, "can't type Chinese / no Chinese output", a missing top-bar IM
  icon, or to diagnose input-method problems — e.g. a candidate window that is
  too small in X11/XWayland apps (WeChat), or WeChat / other non-Wayland apps
  whose UI and fonts are too large from HiDPI double-scaling. Triggers:
  ompinyin, install Chinese input, fcitx5, rime, 装中文输入法, 打不出中文,
  no Chinese IME, 候选框太小, 微信字太大, HiDPI, XWayland.
---

# ompinyin 中文输入法助手（安装 / 排障）

你是运维助手：在一台**已登录的 Omarchy 图形会话**上跑 `ompinyin`，把它收敛到
「全拼 + 万象整句模型 + 顶栏输入法图标 + 候选框跟随 Omarchy 主题」，幂等、可回滚。
只跑工具，**不改代码、不碰仓库**。诊断是只读的：`status` / `doctor` 永远安全，
不会落盘或收敛。

## 前置

- 主机必须 **Omarchy**（`ID=omarchy`）；其它发行版会被预检拒绝（exit 3）。
- 用**普通用户**跑，**勿 root**——只有 `pacman` / `ompinyin source` 会 `sudo` 提权。
- 万象模型 ~420MB，预检要求空闲 ≥2GB。
- 一台机器只需跑一次；重跑是安全的（幂等）。

## 流程

```text
0) 没有就装：Releases 直链 / gh-proxy 前缀 / 源码 make build（见 README「安装」）
1) ompinyin status --json
2) ompinyin install --dry-run --json   → 读 plan，确认 plan.needsApply
3) ompinyin install -y                 # 收敛：全拼 + 万象 + 顶栏图标 + 候选框跟随主题
4) ompinyin status && ompinyin doctor  # 须无差异、全通过
5) 人工核对：Alt+Space 切中英、F4 切方案、顶栏能看到输入法图标、候选框是当前主题配色
```

**完成信号**：`plan.needsApply:false`（或 `status` 无差异）= 没事可做，**停止，别再跑**。

## X11 HiDPI / 候选框大小（默认只诊断）

Wayland 原生应用的候选框正常，但 **X11/XWayland 应用（微信；走 fcitx4 旧协议、
输入上下文挂在 `x11::0`）里的候选框偏小**：fcitx5 classicui 的 X11 UI 只读 X 资源
`Xft.dpi`（XWayland 的 RandR 恒上报 96，`PerScreenDPI` 在 XWayland 上被强制忽略）。
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
候选框与所有 X11 应用会二次放大到 4×。

**`Xft.dpi` 是全局 X11 缩放权，不是候选框私有开关。** 凡把它当逻辑 DPI 的 X11 客户端
都会受影响；若某个应用又自带显式缩放因子，就会**二次放大**（典型：微信
`QT_SCALE_FACTOR=2` + `Xft.dpi=192` → 4×，图标与文字整体变大）。

- 规则：**一个框架只能有一个缩放权，禁止叠加。**
- Qt：删掉显式 `QT_SCALE_FACTOR`，改用 `QT_AUTO_SCREEN_SCALE_FACTOR=1`（自动跟随
  `Xft.dpi`）或 `QT_SCREEN_SCALE_FACTORS=<scale>`（显式覆盖 DPI 派生因子）——二者
  不要并用。
- GTK：必要时用 `GDK_DPI_SCALE` 抵消（`GDK_SCALE` 在管控件缩放）。
- **不要用「删掉全局 `Xft.dpi`」来修某个应用**——那会让候选框又变小。

**冲突与所有权**：ompinyin 只拥有 `~/.Xresources` 的 marker 块，**绝不删除块外的
`Xft.dpi` 行**。`xrdb -merge` 按文件顺序、后者胜；块外存在冲突时 `x11ForeignDpi=true`
（`status`/`doctor`/`--json` 会提示），若它排在块之后，`install` 会在 L5 报
「期望≠实际」并 exit 1（确定性失败，不是静默错值）。`xrdb` 不可用时回退
`xprop -root RESOURCE_MANAGER` 读取。`Xft.dpi` 属**每个 X 会话**（不是每 app、
也不跨 OS 用户），运行中第三方改动无实时守护，下次 `install`/`doctor`/登录/缩放变化纠正。

`ompinyin x11-hidpi-apply` 是 systemd 私有入口，**勿手动跑**。

## 红线（勿越）

- 只在 **Omarchy** 跑；普通用户、勿 root。
- 勿设 `GTK_IM_MODULE` / `~/.xprofile` 等 IM 环境变量（Wayland 反模式）。
- 勿手改 `~/.config/fcitx/rime` 与 `~/.config/omarchy/shell.json`——工具会读→并→写回（`tray.SetPinned`）。
- 勿跑 `pacman -Syu`（会漂移 Omarchy stable 锁版本；只用 `-Sy` / `-S --needed`）。
- 顶栏图标是**必做终态**，无 `--tray-pin`/`--no-tray`；装上就有。
- 候选框跟随 Omarchy 主题也是**默认终态**（无 flag）：`classicui.conf` + `theme-set.d/fcitx5-theme` 钩子入账、按所有权协议覆盖；`~/.local/share/fcitx5/themes/omarchy/` 为钩子生成目录（不入账，但卸载会删）。换主题由钩子自动刷新，勿手改这三个文件。
- `--x11-hidpi` 是**显式 opt-in**：未显式给出时绝不写 `~/.Xresources`、不装 `xorg-xrdb`、不装单元；启用后**勿再给应用叠加显式缩放因子**（见上）。
- 勿手改 `~/.Xresources` 里 marker 块之外的内容，勿删别人的 `Xft.dpi`（行级拥有）。

## 排障

- **X11/微信里候选框太小**：见上「X11 HiDPI」——`doctor` 确认后 `install --x11-hidpi`。
- **微信等非 Wayland 应用整体变大（图标+文字）**：`Xft.dpi` 与显式缩放因子叠加。先看该应用 desktop/环境里的 `QT_SCALE_FACTOR`；按「一框架一缩放权」改成 `QT_SCREEN_SCALE_FACTORS=<scale>` 或去掉显式因子。**不要删 `Xft.dpi`**。
- **`doctor` 报 X11 HiDPI 未达标**：`Xft.dpi 实际≠期望`（可能第三方改了根属性，或块外 `Xft.dpi` 获胜）——重跑 `install`；若提示 `force_zero_scaling=false`，则本模式不适用。
- **L1 装包失败 / 镜像慢**：`ompinyin source`（默认 `--preset cn`，内部 `sudo` 写 `/etc/pacman.d/mirrorlist`，勿 `sudo ompinyin source`）后重跑 `install`。
- **打不出中文**：多为 `default.custom.yaml` 的 `schema_list` 被改成裸 `- <id>` 短格式（`rime_deployer --build` 会忽略，只出 `build/default.yaml` 骨架）。重跑一次 `ompinyin install` 会重写为 `- schema: <id>` map 格式。
- **顶栏图标在 ◀ 抽屉里**：说明只启用了 notificationitem 没 pin `Fcitx`——重跑 `install` 补 pin。
- **doctor 报「候选框主题」未达标**：`classicui.conf` 被手改/缺失、钩子缺失，或当前 Omarchy 主题颜色（`~/.local/state/omarchy/current/theme/colors.toml`）不可用（极少见）——重跑 `ompinyin install` 会按所有权协议修复；主题颜色缺失时候选框保持 fcitx5 默认外观，属预期。
- **首次安装**会接管 `default.custom.yaml`（候选数 9、`,` `.` 翻页），改动前已备份到 `backup-<ts>/`。
- **headless**：`-y` + 给 pacman 配 NOPASSWD sudoers（工具用 `sudo -n`）。

## 退出码

`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检拒绝（非 Omarchy / root / 缺工具 / 磁盘不足）· 第二次 SIGINT 硬退出 `130`。

## 说明

- 本文件遵循 [Agent Skills 标准](https://agentskills.io/specification)，按需加载；事实与 README「AI / Agent 装机」一致，冲突时以 README / `ompinyin --help` 为准。
- 跨 harness 位置：仓库内 `.agents/skills/ompinyin-install/`；装到本机全局则放入 `~/.pi/agent/skills/`、`~/.claude/skills/`、`~/.codex/skills/` 等对应目录。
