# 开发记录：重要决策与开发问题

给**维护者**的。三类文档分工：方案写 [`design.md`](./design.md)（§N 指它），用户问答写 [`qa.md`](./qa.md)，
**这里只放开发问题与决策记录**（含理由、证据、修法与回归）。

格式：决策 `D<编号>`（结论 / 理由 / 影响面）；问题 `P<编号>`（现象 / 根因 / 证据 / 修法 / 回归）。

---

## 决策记录

### D1 一个停止窗口
所有"必须停 fcitx5 才能改"的操作（profile / hotkey / drop-in / rime 部署）在同一个 `runStopWindow` 内完成，
`defer` 保证重启，且只在真有活时开窗。
**理由**：分开 `stop→write→start` 会产生竞态窗口；窗口越长，用户越可能撞上"输入法没了"。
**影响**：新功能要么能在窗口里做完，要么必须证明它能热生效。见 `design.md §3 §6.0`。

### D2 受管文件整文件生成，粒度例外明确
受管文件一律整文件生成 + `catalog.ManagedHeader()`，字节相同即跳过；**例外**只有三个：
`~/.Xresources`（行级 marker 块）、`classicui.conf`（键级：只拥有 6 个键）、`hotkey`/`profile`（最小合并）。
**理由**：整文件生成可预测、可复盘；但 `~/.Xresources`/`classicui.conf` 是用户/他人的文件，整文件覆盖会吞掉别人的配置。
**影响**：新增受管文件默认走整文件；要行级/键级必须说明理由并加"不吞并"测试。见 `design.md §5.1`、`P6`。

### D3 ledger 先于 desired
每层落盘后 `SaveLedger()`；`Save()`（推进 `Desired`）只在成功时执行。
**理由**：否则一次在 L3 之后失败的收敛，会让下一次运行把自家文件判成 `StatusForeign`。见 `design.md §5.1`。

### D4 `ManagedFormat` 是内容形状的版本
生成内容形状变化时递增它；构建版本（`catalog.Version`）绝不进 header。
**理由**：header 带构建戳 ⇒ 每次编译都重写所有受管文件 ⇒ 每跑一次就强制 L3 重写 + 完整 rime 重建，破坏幂等。

### D5 X11 HiDPI 默认只诊断，显式 opt-in
`--x11-hidpi` 才写 `~/.Xresources` + 装单元；`force_zero_scaling=false` 时恒为 no-op。
**理由**：`Xft.dpi` 是全局 X11 缩放权，会让同时设了显式因子的客户端二次放大（实测微信 4×），混合 DPI 无单值解。
**影响**：`Desired.X11HiDPI` 只由命令行显式置位；意图在收敛开始时落盘（双向），L5 失败也不会被下次裸 `install` 反向解释。见 `design.md §6.7 §16-27`、`P1`、`P2`。

### D6 profile 只注册 rime，不再注册 `keyboard-us`
`DefaultProfile()` 只写 `rime`（`EnsureRime` 只增不删）。
**理由**：中英切换是 rime 引擎的 `ascii_composer`，与 fcitx5 的 IM 列表无关；旧版新机器两个 IM、老机器一个，谁都不一致。
**影响**：`Ctrl+Space` 的语义 = 在 IM 组内切到 `keyboard-us`（走 XKB 布局）；单 IM 组里它**不动作**（`canTrigger()` 要求条目数 > 1，见 P9）。`AGENTS`/skill/qa 的措辞都要跟着。见 `design.md §2.3` / §6.1。

### D7 手感项受管，取一组"有默认答案"的集合
受管：`menu/page_size: 9`、`,`/`.` 翻页、`ascii_composer/switch_key/Shift_L|R = commit_code`、
`Control_L|R = noop`、`good_old_caps_lock = true`、`switches/@4/reset: 0`、`switcher/save_options`（不含 `full_shape`）。
不受管：`ascii_punct`/简繁/emoji/单字的**默认值**、其它修饰键。
**理由**：这些项都有公认默认值且踩过坑（全角锁屏、右 Shift 消失）；偏好项（中文标点还是半角）仍归用户。
**影响**：`save_options` 与 `reset` 必须成对（rime 规定「要记忆的开关不能设 reset」）；`switches/@4` 是**下标**选择器，
故 `verify.checkFullShapeIndex` 读编译产物断言下标 4 仍是 `full_shape`。见 `design.md §5.4`、`P3`。

### D8 三个键各管一件事（键位定案）
| 轴 | 键 | 层 |
|---|---|---|
| 中英 | 左右 `Shift`（`commit_code`） | rime 引擎内 |
| 方案（全拼↔双拼） | `F4` | rime `switcher`（`catalog.SwitcherHotkeys`） |
| 切到英文键盘（组内 IM > 1 时） | `Ctrl+Space` | fcitx5 `[Hotkey/TriggerKeys]`（`hotkey.DefaultKeys`） |

`hotkey.PruneKeys = ["Alt+space"]`（旧版本写过的值，且是"窗口菜单"通用键）；键名按 `Ctrl`/`Control` 与大小写归一。

**理由**：
- `Ctrl+Space` 是中文输入法的传统键；它唯一的冲突（herdr 前缀）**在张略层消掉**：Omarchy 出厂给 herdr
  `prefix = "ctrl+space"`（`/usr/share/omarchy/config/herdr/config.toml:18`），把它改成 `ctrl+b`（herdr 内置默认、
  tmux 习惯）即可。**herdr 的前缀不由本仓库管理**，本仓库只负责 fcitx5 侧的触发键。
- 曾被考虑并否决的：`Super+space` 家族（Omarchy 菜单/背景；且 Hyprland 先接 `SUPER+*`，输入法看不到）、
  `Alt+space`（"窗口菜单"通用键，GTK/Electron 会先吃）、`Ctrl+Shift+space`（低频硬开关三键过重）、
  `Ctrl+\``（VS Code 终端 / JetBrains quick-switch）。
- rime 方案选单只留 `F4`：上游的 `Control+grave`/`Control+Shift+grave` 与 VS Code 终端开关（`Ctrl+\``）、
  JetBrains Quick switch scheme 冲突，误触代价高（多开一个终端）。

**影响**：`Ctrl+Space` **只在组内 IM 数 > 1 时动作**（`Instance::trigger` 先判 `canTrigger() == inputMethodList().size() > 1`）；默认组是 `[rime keyboard-us]`，它切到 `keyboard-us`。单 IM 组里它是**纯 no-op**——v1.3.1 曾写「单 IM 时 = 开/停用」，2026-11-03 由源码证伪，见 P9；组形状因此是 `Desired.IMs`（`design.md` §6.1）。
**另一侧（同一前提）**：出厂 `[Hotkey/AltTriggerKeys] = Shift_L`，在 >1 个 IM 时与触发键**走同一个 `toggle()`**，于是左 Shift 变成「第二个 Ctrl+Space」，rime 的 `Shift_L: commit_code` 永远收不到键 → 收敛把该段清空，且只能用「空 section」编码（缺段=回落默认），见 `design.md` §6.2 / P9。
**新装 Omarchy 是冲突现场**（出厂 herdr 也是 `ctrl+space`），所以加了跨工具检测：`observe.HerdrPrefix` 按 herdr 的顺序读
`$HERDR_CONFIG_PATH` → `~/.config/herdr/config.toml` → 内置 `ctrl+b`；`HerdrConflict` 归一比较后进 `doctor`/`status` 的
告警行，写触发键时也打 `[警告]`。**只告警**：不改她的配置，也不偷偷换键（`Desired` 必须来自命令行）。
`HasTrigger` 在被 prune 的键仍绑定时返回 false（plan 才算不收敛）。

### D9 `profile`/`hotkey`/`classicui.conf` 采用最小合并而不是整文件覆盖
见 D2 的例外清单。**理由**：这三个文件被 fcitx5 自己在运行期重写，整文件覆盖会抖动或吞设置。见 `P6`。

### D10 打 tag 前必须 `make smoke`
**理由**：T0 在结构上看不见真机（跨挂载点 `rename(2)`、游离 fcitx5、信号时序），加测试补不了。
**影响**：`make smoke` 必须能在收敛好的机器上直接跑通；它自身也只能在真机跑。见 `design.md §9 §15`、`P4`。

### D11 待决
- Omarchy 插件薄壳（在 shell 里给输入法一个入口）：未做。
- `uninstall --purge-packages`（连包一起卸）：倾向不加（卸载系统包的风险大于收益）。
- 锁屏时让 IME 不参与（`fcitx5-remote -c` 前置或上游 `ImhSensitiveData`）：见 `P3` 的后两道。

---

## 开发问题档案

### P1 登录时 `Xft.dpi` 从不发布，且日志把原因说错
**现象**：`~/.Xresources` 有 `Xft.dpi: 192`、`.path` enabled+active，但每次登录日志都是
`[跳过] L4 X11 HiDPI：未检测到可用的 X 会话（无 XWayland 或 DISPLAY 不可达）`；`xprop -root RESOURCE_MANAGER` 为空；
`doctor` 报 `期望=192 实际=0`。

**根因**：单元被 `graphical-session.target` 拉起时，systemd **冻结环境快照**，而 uwsm 之后才把 `DISPLAY` 导入 manager
⇒ 单元里 `DISPLAY` 为空 ⇒ `xrdb` 报 `Can't open display ''`。`.path` 只听 `monitors.lua`，这一跳失败即整会话不重试。
在 `ExecStartPre` 里等 socket 无效（快照不会更新；`$DISPLAY` 为空时 `ls` 兜底还立刻放行）。

**证据**

```bash
systemctl --user show ompinyin-x11-hidpi.service -p Environment          # 空
journalctl --user -b -o short-precise | rg 'Started ompinyin X11 HiDPI|DISPLAY \(already set\)|\[跳过\] L4 X11'
#   10.640 Started → 11.936 [跳过] → 13.549 DISPLAY (already set)
env -i DISPLAY=:0 xrdb -query     # rc=0（会话可达）
env -i xrdb -query                # rc=1 Can't open display ''
```

**修法**：`internal/hidpi/hidpi.go` 的 `ImportSessionEnv`（`ParseShowEnvironment`/`SessionEnvToImport`/`ShowEnvironment` seam，
≤10s 轮询，`ImportTimeout` 可被 T0 置 0）；在 `ApplyX11HiDPI` **取锁之前**调用（持锁轮询会阻塞并发 `install`）；
`writeHidpi` 的跳过消息改为打印真实 `err` + `DISPLAY=%q`。顺带修好 `hyprctl` 缺 `HYPRLAND_INSTANCE_SIGNATURE`
导致 `force_zero_scaling` 前置空转的问题（读不到时告警）。

**回归**：`TestApplyX11HiDPIResolvesSessionEnvFromManager`、`TestParseShowEnvironment`、`TestSessionEnvToImport`。

### P2 `Xft.dpi` 与 `GDK_SCALE` 叠加成 4×
**根因**：两把缩放权。Omarchy 自带 `monitors.lua` 默认 `GDK_SCALE=2`；GTK3 的 X11 后端同时应用 `GDK_SCALE`（控件）
与 `Xft.dpi`（字体 DPI）。Chromium/Electron 22 的二进制里 `Xft.dpi`/`GDK_SCALE`/`XSETTINGS` 字面量 0 命中 ⇒ 不受影响。

**证据**

```bash
systemctl --user show-environment | rg GDK_SCALE        # GDK_SCALE=2
hyprctl getoption xwayland:force_zero_scaling           # bool: true
strings /usr/lib/libgdk-3.so.0 | rg GDK_SCALE           # 命中
strings /usr/lib/baidunetdisk/baidunetdisk | rg -c 'Xft.dpi|GDK_SCALE'   # 0
```

**修法**：`hidpi.ToolkitScaleFactors` + `ToolkitScaleFactorsInSession`（后者同时读进程 env 与 manager——裸 shell 里没有 `GDK_SCALE`）；
`writeHidpi` 发布前告警、`observe.X11ToolkitFactors` + `verify` 的 X11 行让 `doctor` 常驻告警。**不代改** `monitors.lua`（§16-26）。

**回归**：`TestToolkitScaleFactors*`、`TestX11ToolkitNote`。

### P3 全角状态把锁屏密码输成全角
**根因四环**：`full_shape` 无 `reset` 且 `save_options` 含它（跨会话记忆）→ 锁屏是 omarchy-shell 的 `LockView.qml`
普通 `TextInput`（无 `inputMethodHints`，走 `text-input-v3`）→ fcitx5 wayland 前端**没有** password 特判
（`strings libwaylandim.so | rg -i 'password|sensitive'` → 0）→ PAM 只做字符比对。

**修法**：D7（`switches/@4/reset: 0` + `save_options` 排除 `full_shape`，两者成对）+ `verify.checkFullShapeIndex` 防上游重排。
第二道（锁屏时停 IME）未做：idle 锁屏直接 exec `omarchy-system-lock`，且 session PATH 里 `/usr/share/omarchy/bin`
排在 `~/.local/bin` 之前，影子覆盖不可行。

**回归**：`TestGrammarPatchGolden`、`TestDefaultPatchGolden`、`TestSwitchNames`、`TestCheckFullShapeIndex`。

### P4 `make smoke` 通过却把发布单元写进临时目录
**根因**：`update --self` 先收敛再替换二进制，而 `ExecStart` 来自 **运行中**二进制的 `os.Executable()`；冒烟从
`$work/bin/ompinyin-selftest` 跑，单元因此指向 `$work`，脚本 `trap` 退出即删。冒烟自己的 `doctor` 在 `--self` **之前**跑，没抓到。

**修法**：`scripts/t4-smoke.sh` 在 `--self` 之后用已安装的 `ompinyin install -y` 复原单元，并断言
`ExecStart` 可执行、不含 `$work`；末尾再跑一次 `doctor`。

**回归**：重跑 `make smoke`，末行应为 `publisher unit restored to …/ompinyin; doctor still clean`。

### P5 服务停止时用 D-Bus 探测会造出游离实例（历史，已入 `design.md §6.3`）
**根因**：`org.fcitx.Fcitx5` 是唯一总线名，任何 `fcitx5-remote`/总线调用都会激活一个非单元 fcitx5 占住它
⇒ 单元自己 spawn 的实例启动即退出 0 ⇒ `Restart=always` 拖成 start-limit-hit；而 `Type=simple` 的
`systemctl start` 仍返回 0（假成功，主机没有可用输入法）。
**修法**：存活探测只用 `pgrep`（`FcitxRunning`/`FcitxCount`）；`startUnit` 在单元未激活时先清游离实例并等它让出总线名。
**回归**：`make smoke` 的 stray 步骤（会真的造出游离实例再要求 `install` 清掉）。

### P6 整文件覆盖吞掉用户设置（`classicui.conf`）
**现象**：用户手调的 `TrayFont`/`PerScreenDPI`/`EnableFractionalScale`/`VerticalCandidateList` 在下一次收敛后消失（有备份，非静默）。
**修法**：`theme.ManagedConfKeys` + `ConfContent(existing, font)` 键级合并（保留未受管键、覆盖受管键、幂等）；`ManagedFormat` 递增。
**回归**：`TestConfContentPreservesUnmanagedKeys`。

### P7 `update` 中途取消 → 资产已重放、`build/` 陈旧
**现象**：`default.yaml`（源码）与 `build/default.yaml`（运行期）对同一开关给出不同值，例如右 Shift 忽然失效。
**根因**：`update` 是 overwrite 模式；中断后 L2 资产已是新版但 `rime_deployer --build` 没跑。`default.yaml` 不在受管清单，静默。
**判据**：`ompinyin status --json | rg '"deploy"'` 为 `true`。
**修法**：跑一次 `ompinyin install -y`。**要固化行为就进受管 patch**，别手改 `default.yaml`（`update` 会覆盖）。

---

## 维护这份文档的规则

- 决策变了就改对应的 `D<n>`（不要新增一条"补充说明"），并同步 `design.md` 的对应 §。
- 开发问题修掉后在条目标题上标版本（如 `P1 …（v1.3 修）`），保留证据与回归测试名。
- 用户能感知的操作步骤不要写这里 —— 写 `qa.md`。

### P8（误诊，已撤回）曾把「飞书打不出中文」归因为 fcitx5 重启

**当时的判断**：飞书在 fcitx5 重启之前启动、重启之后中文失效 ⇒ 客户端与旧 IM 的
`zwp_input_method` 会话失效且不会重建，需要重启应用。

**为什么是错的**：真因是飞书走原生 Wayland 却没传 `--enable-wayland-ime`（Chromium 默认关闭
`zwp_text_input_v3`）——它**从来就没能**中文输入，和重启无关。加上该 flag 之后，飞书在 fcitx5
又重启两次（`make smoke`）的情况下**无需重启也正常**；Obsidian（08:34 启动）、Zed（11:41 启动）
同样跨过重启仍然可用 ⇒ 现代 fcitx5 + Hyprland 会在 IM 重启后重新协商，客户端自己重建
text-input 上下文。

**教训**：「重启之后失效」和「一直没有过」在症状上长得一模一样。写进档案之前先证伪：该应用在
**没有任何改动**时到底能不能用？判据是 `pgrep -af <app> | rg enable-wayland-ime` 与
`hyprctl -j clients` 的 `xwayland` 字段。真实修复见 `qa.md`「某个应用里打不出中文」。

### P9 左 Shift 与 Ctrl+Space 变成同一件事（v1.3.2 修）

**现象**：升级后左 Shift 与 `Ctrl+Space` 行为完全相同（都在 `rime ↔ keyboard-us` 之间切），右 Shift 正常
（在 rime 内切中英）。用户报告「左 Shift 和 Ctrl+Space 功能一样了」。

**根因（源码 + 真机）**：
- fcitx5 的 `TriggerKeys`（`Ctrl+Space`）与 `AltTriggerKeys`（出厂 `Shift_L`）**最终都调用同一个
  `Instance::toggle()`**：`Instance::altTrigger` → `toggle`；`Instance::trigger(ic, totallyReleased=true)` → `toggle`。
  差别只在 `canTrigger()`（组条目 > 1）、`canAltTrigger()`（需激活态或由它关闭）与「按住修饰键时的枚举」。
- 因此左 Shift 与触发键**默认就是同义**——只要组里有第二个 IM。
- 老版本 ompinyin 的 `DefaultProfile` 建过 `keyboard-us`，而 `EnsureRime` 只加不删；v1.3.1 改成「新装只留
  `rime`」后两代机器不一致：新装组内 1 项（`canTrigger()` 假 → 左 Shift 落到 rime，正常），升级机组内 2 项
  （`canTrigger()` 真 → 左 Shift 被 AltTrigger 吃掉）。
- 而 v1.3.1 写的 `doctor` 检查只看 `[Hotkey/TriggerKeys]` 是否等于目标值，从不看组条目数与
  `AltTriggerKeys`，于是一台坏机器也报「[完成] 触发键：Control+space（开/停用输入法；中英切换在 rime
  内由 Shift 完成）」——假绿。

**修复**：把「组的形状」和「AltTriggerKeys 为空」都当成终态。
`Desired.IMs`（默认 `[rime keyboard-us]`；`--single-im` / `--english-im`）+ `profile.ConvergeGroup`
（增删/重排/重编号）+ `hotkey.EnsureAltTriggerEmpty`（空 section 编码，INI 无法表达空列表值）+
L5 两条新检查（`输入法组`、`AltTriggerKeys`）+ doctor 的触发键文案按 IM 数分流。

**真机验证**（fcitx5 5.1.23，2026-11-03）：`wtype -M shift -m shift` 后 `fcitx5-remote -n` 仍为 `rime`
（修复前会变 `keyboard-us`）；对照组 `wtype -M ctrl -k space -m ctrl` 仍正常切到 `keyboard-us`。
`fcitx5` 重启、`install` 收敛、`provision` 全跑一遍后保持。

**回归**：`internal/profile`（ConvergeGroup/GroupIMs）、`internal/hotkey`（EnsureAltTriggerEmpty）、
`internal/plan`（形状漂移算 L4 工作）、`internal/verify`（两条新检查）、
`internal/converge`（`TestInstallConvergesLegacyGroupAndAltTrigger`）——共 10 个用例。

**教训**：一个键「和另一个键一样」时，先查**它们是不是调了同一个函数**，再查前提条件（这里是
`canTrigger()`）；声明式终态必须把**前提**也纳入终态，否则「收敛成功」只证明配置文件写对了，
不证明承诺的行为发生。

### P10 Omarchy 4.5 把输入法集成交还上游（v1.4 适配）

**现象**：在 Omarchy 4.5.0 上跑老版本 ompinyin，顶栏会**两个输入法图标**（fcitx5 的 SNI 项 + Omarchy 自绘的
`omarchy.keyboard-layout`）；`install` 写进去的 `Control+space` 过一阵又不见了；候选框主题时好时坏、颜色混装。

**根因（三条独立机制，都用本机安装路径实证）**：

1. **托盘由 shell 自渲染，fcitx5 的托盘项被故意关掉。**
   `default/systemd/user/omarchy-fcitx5.service` 的 `ExecStart=/usr/bin/fcitx5 --replace --disable notificationitem`，
   注释写明 "notificationitem duplicates the tray entry omarchy already renders itself"。我们的 drop-in 把
   `--disable` 剥掉 → 重复一个图标。
2. **热键收归上游，而且会被反复改回来。**
   `default/config/fcitx5/config` 里 `TriggerKeys=Zenkaku_Hankaku/Hangul`（**没有 `Ctrl+space`**）、
   `AltTriggerKeys=0=Shift_L`、`ActiveByDefault=False`；`Super+I` 由 `bin/omarchy-input-method cycle` +
   `default/hypr/bindings/input-method.lua` 提供。更关键的是两处**反悔机制**：
   `configure.py::without_ctrl_space()` 每次 `omarchy setup input --defaults` 都删 `Control+space`；
   迁移 `migrations/1791676907.sh` 会把空的 `[Hotkey/AltTriggerKeys]` 段填回 `0=Shift_L`。
   所以「写触发键 + 清空 AltTrigger」在 4.5 上是**长期打补丁**，每次上游 `--defaults` 都要重新收敛。
3. **候选框主题由上游生成，和我们的钩子并发写同一批文件。**
   `default/themed/fcitx5-theme.conf.tpl` → `omarchy-theme-set-templates` →
   `~/.local/state/omarchy/current/theme/fcitx5-theme.conf` → `bin/omarchy-theme-set-fcitx5`（`omarchy theme set`
   调用它，写 `~/.local/share/fcitx5/themes/omarchy/` 并设 classicui `Theme=omarchy`）。本仓库的 theme-set 钩子
   写同一个 `theme.conf` 与同名 SVG —— **现网已观察到混装**（哪一方最后跑就赢，颜色/圆角对不上）。

**还有一个陷阱：`version` 文件不可信。** `/usr/share/omarchy/version` 在 4.5.0 包里仍写着 `4.0.0.alpha`，
按版本分支必然误判。所以能力探测只认**特性**：`observe.OmarchyManagesIME` =
`/usr/share/omarchy/default/input-methods/indicator.py` 是否存在（`observe.OmarchyInputMethodsPath` 是可注入 seam，
T0 用它同时造 4.5 与 4.0 两种机器；Omarchy 是从 4.0 直接跳到 4.5 的，没有 4.1–4.4）。

**决定**（完整规格见 `docs/adapt-omarchy-4.5.md`，已折叠进 `design.md` §6.2/§6.4/§6.6）：

- **D1 热键跟随 Omarchy**：左 Shift = `keyboard-us ↔ rime`（`AltTriggerKeys=Shift_L` 由上游写），`Super+I` 循环。
  不再写 `[Hotkey/TriggerKeys]`、不碰 `[Hotkey/AltTriggerKeys]`，并删掉 rime 补丁项
  `ascii_composer/switch_key/Shift_L`（先删成不写、后发现「两边抢键」再改成显式的 `noop`，见附 2bis；右 Shift 保留 `commit_code`）。
- **D2 安装器选的 `pinyin` 引擎移出组，但必须明说**：组形状仍完全由 `Desired.IMs` 声明；plan 与 doctor 文案点名
  「将把 pinyin 移出组（引擎与包保留，`omarchy setup input pinyin` 可加回）」。不新增 `--im`。
- **D3 兼容 4.0（4.5 之前的唯一版本；Omarchy 从 4.0 直接跳到 4.5，没有 4.1–4.4）**：保留一条 capability 分支，旧路径**一字不改**；4.5+ 走新路径并做一次性清理（全部幂等）：
  drop-in、pin、theme hook、我们自己生成的两个 SVG、`Control+space` 键。
- **D4 主题交还 `omarchy-theme-set-fcitx5`**：ompinyin 只拥有 `classicui.conf` 的 6 个键；`NeedTheme = !ThemeEqual || ThemeHookOK`
  （生成目录不再参与判定，它归 Omarchy）。
- `catalog.ManagedFormat` 1.2.0 → **1.4.0**：受管文件集合变了（hook 退役、主题产物不再归我们），老主机只重写一次。

**真机/接口证据**：`ompinyin status --json` 在 4.5 机器上给出 `barIndicator:true`、`staleTrayArtifacts:true`
（老版本装的 drop-in/pin/hook 都在）、`themeOK:false`（钩子还在），正是「升级机」的形状。

**迁移坑（写给未来的自己）**：

- `omarchy bar put omarchy.keyboard-layout` 需要**外壳在跑**；不在跑时只能靠 Omarchy 默认配置（4.5 默认就带这个 widget），
  `doctor` 会说明「无法确认」——不要为了让它变绿去手改 `shell.json`。
- 若用户手动删掉 `indicator.py`，会被判成旧版本并重装 drop-in/pin（可接受的降级，不破坏配置）。
- **降级（4.5 的机器回到 4.0）会失去候选框跟随主题**：4.5 前的 Omarchy 没有 `omarchy-theme-set-fcitx5`，
  `theme.ApplyUpstream` 返回 `ErrNoUpstream`，classicui 指向不存在的主题名，fcitx5 回落默认外观。

**实施修正（主 Agent 复核后）**：

1. **drop-in 的删除挪进了 stop 窗口。** 原先（本文第一版实现）是收敛在 `runStopWindow` 之后调
   `tray.RemoveDropIn`：文件是删了，但**正在跑的 fcitx5 仍是带着旧 ExecStart 起来的**（`--disable notificationitem`
   已被 drop-in 剥掉），SNI 托盘项照发 → 顶栏两个图标一直留到下次登录。而 `NeedHost` 当时不含 drop-in 残留，
   下次运行不会开 stop 窗口 → 永远不会自愈。修法（不新增停止窗口）：
   - `plan.NeedHost`（4.5+）加入 `c.DropInExists` —— 残留 drop-in 只能靠一次 unit 重启生效，所以它本身就是
     窗口内的工作项；`PinnedHasFc` / `ThemeHookOK` 不需要重启，不进 `NeedHost`。
   - `tray.RemoveDropIn` 移到 `runStopWindow` 的 4.5+ 分支、`service.DaemonReload()` 之前；`trayTerminal`
     只留 `EnsureIndicator` + `RemovePinned`。文案也随之拆开（窗口内报 drop-in、窗口外报 pin），避免
     「说在窗口外删了、其实在窗口内」的假话。
   - 回归用例：`TestInstallRemovesLegacyDropInInsideStopWindow`（只有 drop-in 残留的 4.5 机 → `NeedHost` 为真、
     日志顺序 stop→移除→start、赛后文件不存在、二次运行窗口不开）。
2. **L5「顶栏输入法指示器」在外壳没跑时不再误报红。** `bar.EnsureIndicator` 在外壳没跑时是 no-op，而 Omarchy
   默认配置本来就带 `omarchy.keyboard-layout`，所以「widget 不在 + 外壳没跑」是一次运行也清不掉的假红。
   现在三分支：在 → OK；不在且外壳在跑 → 失败（收敛本该放回去）；不在且外壳没跑 → OK + 「无法确认」说明。
   回归用例：`verify.TestBarIndicatorCheck`。

**另一个事实修正**：Omarchy 是从 **4.0 直接跳到 4.5** 的，**没有 4.1–4.4**。本文与 `design.md`/README/SKILL 里
的「4.0–4.4」一律读作「4.0（4.5 之前）」——实现里只有一条分支（`indicator.py` 在不在），与具体小版本号无关。

### P10 附：v1.0 的 `override.conf` 残留（真机 2026-11-05 抓到的第二个图标）

**现象**：4.5 机上收敛完成后 `systemctl --user show omarchy-fcitx5.service -p ExecStart` 仍是
`/usr/bin/fcitx5`——Omarchy 的 `--replace --disable notificationitem` 被吃掉了，于是 fcitx5 继续发自己的
SNI 托盘项，顶栏与 `omarchy.keyboard-layout` 并存（两个图标）。而我们的 `tray.RemoveDropIn` 只删
`ompinyin-notificationitem.conf`，覆盖不到它。

**根因**：`~/.config/systemd/user/omarchy-fcitx5.service.d/override.conf`，内容恰好是 v1.0（commit 7c289dc）
写的

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/fcitx5
```

**没有 managed header**，文件名又是通用的 `override.conf`。所以：

- **不能只认自己的文件名**：`ompinyin-notificationitem.conf` 是专用名，`override.conf` 不是——系统、用户、
  其它工具都可能写它。删名字 = 删别人的配置。
- **也不能只认 managed header**：v1.0 的文件根本没有头。
- 唯一可靠判据是**内容**：`tray.LegacyOverrideMatches(content, derivedBody)` 只接受两种 body —— v1.0 的字面量
  （`tray.LegacyOverrideBody`）与 `DeriveDropInContent` 的输出（早期版本可能把它写到通用名下）。其它内容一律不动，
  交给 `doctor` 去说。
- **而且必须读「生效值」而不是文件**：drop-in 的优先级是 systemd 算的。`observe.ExecStartOK` 走
  `systemctl --user show <unit> -p ExecStart --value`（seam `observe.UnitExecStart`），L5/doctor 新增
  `fcitx5 启动参数` 一行——这样**任何来源**的覆盖（用户手写的、dotfiles 的、别的工具的）都能被抓到，
  而不是只盯着我们自己写过的名字。命令失败（无 systemd / 无 unit）时视为不可判定，不算失败。

**删除时机同样在 stop 窗口内**（和 drop-in 一样）：文件删掉不会改变正在跑的进程，只有重启才重新读 drop-in；
所以 `plan.NeedHost`（4.5+）同时含 `DropInExists` 与 `StaleOverride`。删除前先 `copyToBackup`（§5.1）。

**新增/相关回归**：`tray.TestRemoveLegacyOverride`（三种内容 + 同目录用户 drop-in 存活）、
`tray.TestDerivedDropInBody`、`observe.TestCollectLegacyOverride`、`observe.TestCollectExecStartProbe`、
`plan.TestDiffLegacyOverrideResidueIsHostWork`、`verify.TestExecStartCheck`、
`converge.TestInstallRemovesLegacyV1Override`、`converge.TestInstallKeepsUserOverride`。
`make smoke` 增加两条廉价断言：`.d/` 里没有我们的 drop-in 且 `override.conf` 不覆盖 ExecStart / 生效 ExecStart
含 `--disable notificationitem`；`themes/omarchy/theme.conf` 存在且我们的两个 SVG 不存在。

### P10 附二：`AltTriggerKeys` 缺段 = 默认 `Shift_L`（真机/源码复核）与 smoke 的分支

**1. 不写 `[Hotkey/AltTriggerKeys]` 与 Omarchy 出厂等价，不需要 `EnsureAltTriggerEmpty`。**

真机 + 源码核实（`src/lib/fcitx/globalconfig.cpp`）：该配置项的默认值就是 `Shift_L`
（`defaultValue{{Key("Shift_L")}}`）。所以 4.5+ 我们的终态「不写这段」与 Omarchy 出厂的
`AltTriggerKeys=0=Shift_L` **语义完全相同**：

- 4.0（旧路径）：那段是**我们的**终态，必须显式清空（空 section），否则左 Shift 被 AltTrigger 抢走、
  rime 的 `Shift_L: commit_code` 永不触发（P9）；**缺段 = 回落 `Shift_L`**，所以「空 section」是唯一编码。
- 4.5+：左 Shift 本来就该是「进出输入法」（D1），上游也一直在写 `0=Shift_L`，而且迁移脚本会把空的段填回
  `0=Shift_L`。所以我们**不写、不清空、不断言**它 —— 缺段落到默认值，正是我们要的行为。
  同一个「缺段 = Shift_L」在两代里含义相反，是因为 rime 侧受管的值变了（`catalog.DefaultPatch` 从
  `Shift_L: commit_code` 改成 `Shift_L: noop`，见附 2bis），键的归属变了，不是 fcitx5 的行为变了。

**2. `fcitx5-remote`（无参）不能用来判断「左 Shift 是否生效」。**

它读的是 **dbus frontend 那个 input context**，不是当前聚焦窗口的 IC。焦点窗口的 input context 由各客户端
（Wayland text-input / XIM / fcitx4 前端）自己的对象提供，命令行根本拿不到。所以真机验证「左 Shift 进出输入法 /
右 Shift 切中英」只能**实际打字看效果**（上屏内容 / 候选框 / 截图），不能用 `fcitx5-remote -n` 反推。
`fcitx5-remote` 仍然可用于**全局**状态：`-n` 的 IM 名（`rime` / `keyboard-us`）反映 fcitx5 当前组内激活项，
`doctor` 的「IM 三态」用的就是这个，它证明不了具体窗口里的按键行为。

**2bis. 左 Shift 必须写 `Shift_L: noop`，否则「一个键两个主人」。**

fcitx5 的语义（`instance.cpp`）：

```c
bool Instance::altTrigger(ic)   { toggle(ic, AltTrigger); }        // 只翻转 active（开/关输入法）
bool Instance::toggle(ic, ...)  { inputState->setActive(!isActive()); }  // 不换组内 IM
```

而 rime 出厂 `default.yaml` 里 `Shift_L: commit_code`。两个引擎都处理同一个按键，可见结果就取决于
「输入法是开还是关」「当前 IM 是 rime 还是 keyboard-us」以及按键处理顺序 —— 真机表现为
**同一个左 Shift 有时进 rime、有时进英文键盘、有时要按两下**（2026-11-05 用户报告）。

结论：左 Shift 只能有一个主人。我们保留 Omarchy 的归属（fcitx 的 `AltTriggerKeys`），把 rime 那边写死
`Shift_L: noop`。于是三条轴互不重叠：

| 键 | 唯一含义 | 归属 |
|---|---|---|
| 左 Shift | 开/关输入法（只翻 `active`，不改组内 IM） | fcitx5 `AltTriggerKeys` |
| 右 Shift | rime 内 中↔英（先上屏编码） | rime `ascii_composer/Shift_R: commit_code` |
| `Super+I` | 换当前 IM（rime ↔ keyboard-us） | Omarchy `omarchy-input-method cycle` |

这条同时也解释了为什么「把 `[Hotkey/AltTriggerKeys]` 清空、让 rime 独占左 Shift」不是好方案：那等于
`English(US)` 那一项失去键盘入口，而且每次 `omarchy setup input` 之后又要跟上游的 `0=Shift_L` 打架。

**2ter. 左 Shift「打开」后是哪个 IM：不是「上次那个」，是 `localIM_` 或 `DefaultIM`。**

真机观察（2026-11-05）：`Super+I` 切到 `keyboard-us`，按左 Shift 关掉再打开，会回到 rime（自然码）。
读 `src/lib/fcitx/instance.cpp` 的 `Instance::inputMethod(ic)` 才看清，它有三种分支：

```c
if (active) {
    if (!localIM_.empty() && groupContains(group, localIM_)) return localIM_;  // Super+I / SetCurrentIM 写的那一个
    return group.defaultInputMethod();                                          // = DefaultIM = rime
}
return group.inputMethodList()[0].name();   // 未激活时也报组内第一项（我们的组第一项就是 rime）
```

推论（都已在真机或源码上确认）：

- 左 Shift 只翻 `active`，**不改组内 IM**；「打开」时的落点是**窗口级**的：那个输入上下文被
  `Super+I` 显式设过就用它，否则回落到 `DefaultIM`。换窗口 / 应用重建输入上下文后就不再适用。
  所以「Super+I → 英文键盘 → 左 Shift 关 → 左 Shift 开 → 又是中文」是 fcitx 的既定行为，不是收敛漂移。
- 顶栏指示器读的是 `CurrentInputMethodInfo`，即上面的 `inputMethod(ic)`：**未激活时它照样报 rime**，
  所以「关掉了」和「在 rime 里」在顶栏上无法区分（Omarchy 的指示器不显示激活状态）。
- `fcitx5-remote`（无参）同理只能读 dbus frontend 那个 IC 的 `active`，读不到聚焦窗口的状态（附二 §2）。

用户可执行的结论（写进 `qa.md`）：要临时打英文用左 Shift；要稳定待在英文键盘就用 `Super+I` 再切一次，
不要指望左 Shift 记住它。若将来要彻底去掉这条歧义，只能二选一：停用左 Shift（写 `[Hotkey/AltTriggerKeys]`
空段，与上游抢配置），或组里只留 rime（`--single-im`，`canTrigger()` 为假、Omarchy 的 Setup/Typing 退化）。

**3. `make smoke` 的游离实例步骤在 4.5 上换了断言。**

4.5 的 `~/.local/share/dbus-1/services/org.fcitx.Fcitx5.service` 带
`SystemdService=omarchy-fcitx5.service`，所以 D-Bus 激活**直接把受管 unit 拉起来**，不再产生游离实例 ——
「stop 后调用 `fcitx5-remote` 造一个游离进程」这个舞台在 4.5 上不成立（实测：stop 后调用会让
`is-active` 立刻变 active，旧脚本据此 die）。脚本按能力分支：

- 4.5+（`SystemdService=$UNIT`）：stop 并轮询确认真停 → 调 `fcitx5-remote` → 轮询到 active（~5s 超时）→
  断言 `busctl` 的 PID == unit 的 `MainPID`、`pgrep -c -x fcitx5` == 1 → 再跑一次 `install -y` 并复查（幂等）。
- 4.0：保留原来的「制造游离 → install 清理 → owner == MainPID」。

顺带把「我们的主题产物不在」断言改准：我们的生成器写的是**普通文件**，Omarchy 的主题目录用**符号链接**
（`/usr/share/fcitx5/themes/default/*.svg`），所以只对「普通文件」判红，符号链接放过 —— 上游哪天自己发
`background.svg` 也不会误报。
