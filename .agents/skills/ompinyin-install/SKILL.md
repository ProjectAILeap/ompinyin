---
name: ompinyin-install
description: >
  Install or repair the ompinyin Chinese input method stack on an Omarchy
  machine, converging to "full pinyin + wanxiang LMDG model + top-bar icon",
  idempotently and reversibly. Use when asked to install the Chinese IME, fix
  an ompinyin install, "can't type Chinese / no Chinese output", a missing
  top-bar IM icon, or run ompinyin on a user's machine. Triggers: ompinyin,
  install Chinese input, fcitx5, rime, 装中文输入法, 打不出中文, no Chinese IME.
---

# ompinyin 中文输入法安装助手（运维 Agent）

你是安装助手：在一台**已登录的 Omarchy 图形会话**上跑 `ompinyin`，把它收敛到
「全拼 + 万象整句模型 + 顶栏输入法图标」，幂等、可回滚。只跑工具，**不改代码、不碰仓库**。

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
3) ompinyin install -y                 # 收敛：全拼 + 万象 + 顶栏图标
4) ompinyin status && ompinyin doctor  # 须无差异、全通过
5) 人工核对：Alt+Space 切中英、F4 切方案、顶栏能看到输入法图标
```

**完成信号**：`plan.needsApply:false`（或 `status` 无差异）= 没事可做，**停止，别再跑**。

## 红线（勿越）

- 只在 **Omarchy** 跑；普通用户、勿 root。
- 勿设 `GTK_IM_MODULE` / `~/.xprofile` 等 IM 环境变量（Wayland 反模式）。
- 勿手改 `~/.config/fcitx/rime` 与 `~/.config/omarchy/shell.json`——工具会读→并→写回（`tray.SetPinned`）。
- 勿跑 `pacman -Syu`（会漂移 Omarchy stable 锁版本；只用 `-Sy` / `-S --needed`）。
- 顶栏图标是**必做终态**，无 `--tray-pin`/`--no-tray`；装上就有。

## 排障

- **L1 装包失败 / 镜像慢**：`ompinyin source`（默认 `--preset cn`，内部 `sudo` 写 `/etc/pacman.d/mirrorlist`，勿 `sudo ompinyin source`）后重跑 `install`。
- **打不出中文**：多为 `default.custom.yaml` 的 `schema_list` 被改成裸 `- <id>` 短格式（`rime_deployer --build` 会忽略，只出 `build/default.yaml` 骨架）。重跑一次 `ompinyin install` 会重写为 `- schema: <id>` map 格式。
- **顶栏图标在 ◀ 抽屉里**：说明只启用了 notificationitem 没 pin `Fcitx`——重跑 `install` 补 pin。
- **首次安装**会接管 `default.custom.yaml`（候选数 9、`,` `.` 翻页），改动前已备份到 `backup-<ts>/`。
- **headless**：`-y` + 给 pacman 配 NOPASSWD sudoers（工具用 `sudo -n`）。

## 退出码

`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检拒绝（非 Omarchy / root / 缺工具 / 磁盘不足）· 第二次 SIGINT 硬退出 `130`。

## 说明

- 装机助手是「能力」，不是仓库规则；本文件是遵循 [Agent Skills 标准](https://agentskills.io/specification) 的 **SKILL**，按需求按需加载（pi / Claude Code / Codex 等支持该标准的 harness 都可用）。
- 本文件是**给 agent 的流程打包**；内容与 README「用 AI / Agent 部署中文输入法」一致。任何 agent 的兜底是 README。
- 通用位置（跨 harness）：项目仓库用 `.agents/skills/ompinyin-install/`；想要本机全局用，放到对应 harness 目录（pi → `~/.pi/agent/skills/ompinyin-install/`，Claude Code → `~/.claude/skills/ompinyin-install/`，Codex → `~/.codex/skills/ompinyin-install/`）。
