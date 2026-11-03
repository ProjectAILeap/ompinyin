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
**影响**：`Ctrl+Space` 的语义 = 开/停用输入法（停用即走 XKB 布局）；`AGENTS`/skill/qa 的措辞都要跟着。见 `design.md §2.3`。

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
| 输入法开/停用（单 IM 时） | `Ctrl+Space` | fcitx5 `[Hotkey/TriggerKeys]`（`hotkey.DefaultKeys`） |

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

**影响**：单 IM 时 `Ctrl+Space` = 开/停用；停用后 rime 收不到键（Shift/F4 失效）属预期。
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
