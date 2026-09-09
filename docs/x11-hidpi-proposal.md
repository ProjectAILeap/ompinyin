# 决策记录：X11 HiDPI（ClassicUI X11 候选框缩放）——默认只诊断，显式 opt-in

> 状态：已实现（v2 收敛行为修正后落地）。本文档先记录原始「无开关自动收敛」提案，再记录**为什么撤回**，最后是落地形态。
> 相关代码：`internal/hidpi`（纯函数 + seam）、`converge/install.go`（L4 writeHidpi / undoHidpi / ApplyX11HiDPI）、`observe` X11 事实、`plan` 的 `hidpi`/`hidpiUndo` 谓词、`verify` doctor 检查项。
> 背景实证：2026-09-09，ThinkBook（单屏，Hyprland scale 2.0），fcitx5 5.1.22，`wechat-universal-bwrap` 4.1.13.9。

## 1. 问题

Wayland 会话下 fcitx5 classicui 是**双 UI**（日志实证，同一进程输出两行）：

```text
classicui.cpp:89]  Created classicui for x11 display::0
classicui.cpp:110] Created classicui for wayland display:
```

X11 程序（微信，走 `fcitx4` 老协议，输入上下文挂在 `Group [x11::0]`）命中 **X11 候选框**，按 96 DPI 渲染 → 在 2x 屏上字小；原生 Wayland 应用走另一套 → 正常。`QT_SCALE_FACTOR`、`GDK_SCALE`、`ForceWaylandDPI` 都够不到 X11 这套。

X11 UI 字号公式（上游源码 `fcitx5/src/ui/classic/xcbui.cpp::XCBUI::scaledDPI`，已拉源码核对）：

- `PerScreenDPI=False`（默认）→ 取 X resources 的 `Xft.dpi`，缺省回退 96；
- `PerScreenDPI=True` → 取 RandR 上报值——但 **XWayland 恒上报 96**，此路不通（已实机验证无效）。

已验证有效的修复：根窗口 `RESOURCE_MANAGER` 写入 `Xft.dpi: 192` + 重启 fcitx5（XCBUI 只在启动时解析一次）。Qt（微信本体）不读 `Xft.dpi`，故本体不受影响——三处对照验证通过（微信候选框变大 / 微信本体不变 / 终端不变）。

## 2. 目标与非目标

目标：`ompinyin install --x11-hidpi` 后，X11 程序的 fcitx5 候选框字号跟随显示缩放；改缩放后由监听单元自动跟上；`doctor` 能检出漂移。**默认不做任何事**：`install` 裸跑只诊断。

非目标：

- 不写显示缩放本身（`monitors.lua`、`hyprctl` 输出永远只读，那是 Omarchy 官方显示层的领地）；
- 不管 `classicui.conf`（`Font`、`PerScreenDPI`、`ForceWaylandDPI` 实证均不需要动，不碰避免与用户手调冲突）；
- 不管 Wayland UI（合成器已处理正确）；
- 多屏混合 DPI 不做自动精确化（`Xft.dpi` 是 X11 全局单值，见 §6）。

## 3. 历史决策与撤回

### 3.1 撤回：原「L4 不变量，无开关」策略（不采用）

原始提案把 X11-HiDPI 学顶栏图标（`DESIGN.md` §6.4）定义为「输入法正确性」L4 不变量：**不设开关**，每次收敛自动达成——`install` 自动写 `~/.Xresources` 受管块、自动装 `xorg-xrdb`、自动装登录发布单元并监听 `monitors.lua`。

**撤回原因（评审定案）**：`Xft.dpi` 是**全局 XWayland 资源**，不是 ompinyin 或 fcitx5 的私有领地：

- 其它 X11/Electron 应用若已由合成器（scale 方式）或自身（Qt 的 `QT_SCALE_FACTOR` 等）缩放，read 到全局 `Xft.dpi` 后可能**二次放大**——这是对用户桌面的全局副作用，无权默认施加；
- 混合 DPI 多屏下，XWayland 与 `Xft.dpi` 都是单值，没有正确的自动解；焦点屏切换会同时影响所有 X11 应用并打断输入；
- 默认动作 = 默认装一个包 + 写两个 user 单元 + 监听文件变化并重启 fcitx5，对未被影响到的用户是负资产。

**唯一保留的默认行为是诊断**：`status`/`doctor`/`--dry-run` 永远把 X11 事实（scale、期望/实际 DPI、配置与单元状态）读出来展示，但**不写文件、不发 ressource、不装包**。用户确认候选框确实过小后，才用 `install --x11-hidpi` 明确 opt-in（`Desired.X11HiDPI` 持久化，后续收敛持续维护）；`install --no-x11-hidpi` 撤销，移除受管块与两个单元。

### 3.2 推导公式（常量 96 是约定，不是测量）

```text
dpi = round(96 × scale)，取整（X 资源只接受整数）
```

96 的三重出处（均为 1x 基准的同一约定）：CSS 参考像素（96px=1in）、X11 历史缺省 DPI、Pango/fontconfig 缺省分辨率。面板真实 DPI（如 280+）在逻辑缩放体系里用不上。映射表示例：1.0→96、1.25→120、1.5→144、1.6→154、1.75→168、2.0→192。

`scale` 来源优先级（只读）：

1. `hyprctl monitors -j` 聚焦显示器的实时 `scale`（能处理 `monitors.lua` 为 `auto` 的情况；多屏取聚焦屏——用户正在看的那块）；
2. `~/.config/hypr/monitors.lua` 的 `omarchy_monitor_scale` 数字预设；
3. 沿用 `~/.Xresources` 现有 `Xft.dpi`（首次且无源时按 96，不放大）。

### 3.3 收敛内容（opt-in 后的 L4 工作，类比 tray drop-in）

| # | 终态 | 说明 |
|---|---|---|
| 1 | L1 新增包（**仅 opt-in 时**） | `xorg-xrdb`（`xrdb -merge` 发布 RESOURCE_MANAGER；`pkgs.Needed` 不含它，`requiredPackages()` 按 `Desired.X11HiDPI` 追加） |
| 2 | `~/.Xresources` 受管块 | **只拥有 `Xft.dpi` 一行**（markers `# >>> ompinyin-managed >>>` 包裹），其余用户内容原样保留；幂等：行在且值对 → 无操作（`MergeXresources`） |
| 3 | 登录自启 hook | systemd user service `ompinyin-x11-hidpi.service`（`WantedBy=graphical-session.target`），`ExecStart=ompinyin x11-hidpi-apply`（内部命令，不进 CLI 契约） |
| 4 | 缩放跟随 | systemd user **path 单元 `ompinyin-x11-hidpi.path` 监听 `~/.config/hypr/monitors.lua`**，变化即 `ompinyin x11-hidpi-apply` 重算重推。显示缩放试用/回退会各触发一次 fcitx5 重启——正在拼写的拼音串会被打断，已写入 README/输出 |
| 5 | fcitx5 重启策略 | 发布后必须重启（XCBUI 启动时一次性解析）。只在「值确实变了」时重启，避免无意义打断 |
| 6 | **撤销（`--no-x11-hidpi`）** | 先 `disable --now` path 单元（防监视器变化再发布），删两个 unit 文件，再移除 `~/.Xresources` 受管块（空则删文件）；已发布到当前会话 RESOURCE_MANAGER 的值保留到注销（`xrdb -load` 会清空整库，破坏性过大，不做）。`ApplyX11HiDPI` 在未 opt-in 时是 no-op，防残留 enable 链接继续发布 |

### 3.4 `doctor` 检查项（默认也展示，只读）

- 根窗口 `RESOURCE_MANAGER` 是否含 `Xft.dpi` 且 `== round(96×当前scale)`（未 opt-in 时报告「未启用」及风险一句话）；
- 受管块、登录发布单元、path 单元是否存在且内容正确（未 opt-in 时仍读，便于发现残留）；
- `~/.Xresources` 行级拥有校验：用户内容是否被破坏（只告警、不收敛）。

## 4. 与现有分层的衔接

- `facts`/`observe`：新增只读事实 `x11Scale/x11DpiDesired/x11DpiActual/x11Available/x11ConfigOK/x11UnitsOK/x11ManagedPresent`（`observe.Collect` 每次调用都采集——默认只诊断的载体；`status --json` 的 `host` 字段暴露）；
- `plan`：`NeedHidpi`（opt-in 且未达终态）与 `NeedHidpiUndo`（已 opt-in 过、现在要撤）两个谓词；`--dry-run --json` 的 `need.hidpi` 同时覆盖两者，steps 里有对应揭示行；
- `converge`：`writeHidpi` 在 stop 窗口内完成（最后一件事是重启 fcitx5 服务，顺序见 AGENTS「关键陷阱」）；`undoHidpi` **在 stop 窗口外**执行——撤销不碰 fcitx5，不许 churn 输入法；`ApplyX11HiDPI` 是 systemd 调用的私有入口；
- `verify`（L5）：未 opt-in → 恒 OK 的「可选」项 + 风险说明；opt-in → 发布后读回根窗口值比对；
- `uninstall`：移除受管块、两个单元、`~/.Xresources` 若因此变空则删文件（`RemoveXresources` 的 `empty` 分支）。

## 5. 所有权与红线（对应 DESIGN §5/§16）

- `~/.Xresources`：**行级拥有**（仅 marker 块），整文件不归 ompinyin；`RemoveXresources` 保用户内容；
- `monitors.lua`、`hyprland` 配置：只读，永不写入；
- `classicui.conf`：永不写入（§1 已证无需）；
- 受管文件形状无变化 → **不递增 `catalog.ManagedFormat`**（AGENTS.md 幂等性说明：只在生成内容形状变化时递增，避免全量 L3 重写）。

## 6. 风险与已知局限

| 风险 | 评估 | 缓解 |
|---|---|---|
| `Xft.dpi` 是 X11 全局的，未来装 Electron/QQ 等 X11 应用可能被二次放大 | 中（当前无此类应用；Qt 已证免疫） | **默认不启用**；README 明示；`doctor` 每次展示「未启用」及风险 |
| 多屏混合 DPI 下单值只能满足聚焦屏 | 低-中（单屏用户为主；Wayland 侧天然按屏） | 取聚焦屏；**不做焦点切换自动改写**（会影响所有 X11 应用并打断输入）；文档写清 |
| 无 XWayland 的纯 Wayland 会话 | 低 | 探测不到 RESOURCE_MANAGER 时安装仍收敛文件与单元，发布跳过并注记 |
| fcitx5 重启打断正在输入 | 低 | 仅值变化时重启；输出里提示 |
| `hyprctl`/`jq` 缺失时的 scale 推导 | 低 | 按 §3.2 逐级降级；XML 解析残留 `grep -o` 是备选 |
| opt-out 后已发布值残留到注销 | 低 | README/DESIGN 明示；新登录即干净 |

## 7. 落地顺序（已完成）

1. **默认只诊断**：`observe` X11 事实 + `status --json`/`doctor` 只读展示（零风险，先让漂移可见）；
2. **opt-in 收敛**：`install --x11-hidpi`（L1 包 + L4 块 + 单元 + 发布 + fcitx5 重启策略）；
3. **撤销**：`install --no-x11-hidpi`（stop 窗口外移除产物 + `ApplyX11HiDPI` no-op 护栏）；
4. `uninstall` 清理 + T0 假主机测试（`setupFakeHost` 的 `hidpi.Run` seam）。

## 8. 测试（对应 DESIGN §9/§15/§16）

- 单测（纯函数，无需假主机）：推导表（§3.2 全映射，重点 1.6→154 的取整）、Xresources 行级合并幂等（已存在/缺失/值错三种输入）、`ManagedBlockPresent`；
- 假主机测试（`setupFakeHost` fixture 模式，CI）：默认 install 零产物；opt-in 收敛 + 幂等重跑；opt-out 撤销三件套；`ApplyX11HiDPI` 未 opt-in no-op；
- 不变量：收敛后复跑 diff 为空；`classicui.conf` mtime/content 不变；
- 实机 T 级：2.0 缩放三对照（§1）为验收门槛；1.6 小数缩放抽查一次。

## 9. 验收标准

- [ ] 全新 Omarchy（2x 缩放）`ompinyin install` 后：**不写** `~/.Xresources`、不装 `xorg-xrdb`、无两个 user 单元；`doctor` 显示「X11 HiDPI（可选）未启用」；
- [ ] `ompinyin install --x11-hidpi` 后，微信候选框与终端候选框视觉一致；`~/.Xresources` 出现 marker 块且其它用户行原样；
- [ ] 显示缩放改为 1.6（顶栏菜单或 `omarchy hyprland monitor scaling 1.6`）后无需人工干预，`Xft.dpi` 变为 154（重启 fcitx5 一次）；
- [ ] 二次 `install` 无操作（幂等），`--dry-run` 只打印不落盘；
- [ ] `ompinyin install --no-x11-hidpi` 后受管块与两个单元消失，用户自有 Xresources 内容保留；
- [ ] `doctor` 在 opt-in 后手动删除 `Xft.dpi` 能报出漂移；未 opt-in 时一律 OK（只诊断）。