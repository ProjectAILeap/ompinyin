# 适配 Omarchy 4.5.0：把输入法集成交还上游

状态：**已批准（2026-11-05），已完成于 v1.4.0**（`ManagedFormat` 同步递增到 `1.4.0`；本分支 `feat/omarchy-4.5-adapt`。发版前仍需 `make smoke` 真机 T4）。
性质：一次性实施规格。完成并发布后，可执行内容折叠进 `docs/design.md`（§6.1/§6.2/§6.4/§6.6/§16）
与 `docs/dev-notes.md`（新增 P10），本文件保留为“4.5 迁移说明”。

---

## 0. 背景：4.5.0 把什么变成了它自己的终态

| 机制 | 证据（本机安装路径） | 对本仓库的后果 |
|---|---|---|
| fcitx5 由 systemd 托管，**故意关掉** fcitx5 自己的托盘项 | `default/systemd/user/omarchy-fcitx5.service`：`ExecStart=/usr/bin/fcitx5 --replace --disable notificationitem`（注释写明“notificationitem duplicates the tray entry omarchy already renders itself”）+ `default/input-methods/org.fcitx.Fcitx5.service`（D-Bus 激活指向该 unit） | L4 的 notificationitem drop-in 失去意义，且**造成两个图标** |
| 顶栏输入法指示器由 shell 自己渲染 | `shell/plugins/bar/widgets/KeyboardLayout.qml` + `shell/Commons/InputMethodState.qml` + `default/input-methods/indicator.py`；`config/omarchy/shell.json` 默认右区含 `omarchy.keyboard-layout` | “顶栏有图标”不再是本仓库要创建的东西，只需确认 widget 在栏上 |
| 热键收归上游 | `default/config/fcitx5/config`：`TriggerKeys=Zenkaku_Hankaku/Hangul`（**无 Ctrl+Space**）、`AltTriggerKeys=0=Shift_L`、`ActiveByDefault=False`；`bin/omarchy-input-method cycle` + `default/hypr/bindings/input-method.lua`（`SUPER+I` / `SUPER+SHIFT+I`；终端里 Ctrl+Space 为 non-consuming 直通）；`configure.py::without_ctrl_space()` 每次 `--defaults` 都会删 `Control+space`；迁移 `migrations/1791676907.sh` 把空的 `[Hotkey/AltTriggerKeys]` 段填回 `0=Shift_L` | 写 `Control+space` 与清空 AltTriggerKeys 都会被上游反复撤销 → 长期打补丁 |
| 候选框主题由上游生成 | `default/themed/fcitx5-theme.conf.tpl` → `omarchy-theme-set-templates` → `~/.local/state/omarchy/current/theme/fcitx5-theme.conf` → `bin/omarchy-theme-set-fcitx5`（`omarchy theme set` 会调用，写 `~/.local/share/fcitx5/themes/omarchy/` 并设 classicui `Theme=omarchy`） | 与我们的 `internal/theme` 生成器 + theme-set hook **并发写同一批文件**（已在本机观察到混装） |

**版本号不可用于分支**：`/usr/share/omarchy/version` 在 4.5.0 包上仍写着 `4.0.0.alpha`。一律特性探测。

**没有 4.1–4.4**：Omarchy 是从 4.0 直接跳到 4.5 的，中间没有大版本号。所以本文里「旧路径 / 4.0–4.4」都指
**同一批机器**：升级前跑的 4.0.x（以及更早）。下文保留「4.0–4.4」只为与 D3 的原措辞一致，实现与文档
（`design.md`/`dev-notes.md`/README）里一律写作「4.0（4.5 之前）」。

其它已确认、**不需要**改动的：
- `default/environment.d/10-omarchy-fcitx.conf` 注入 IM 环境变量（本仓库的 env 红线检查本来就以它为前提）。
- `fcitx5` 与全部引擎已在 `install/omarchy-base.packages`（含 `fcitx5-chinese-addons`）。

---

## 1. 已批准的三个决策

- **D1 中英模型：跟随 Omarchy。** 左 Shift = 组内 `keyboard-us` ↔ `rime`（`AltTriggerKeys=Shift_L` 由上游写），`Super+I` 循环。因此：
  - 不再写 `[Hotkey/TriggerKeys]`，不再清空 `[Hotkey/AltTriggerKeys]`；
  - rime 补丁项 `ascii_composer/switch_key/Shift_L`：最初删成「不写」，真机发现会与 fcitx 的 AltTrigger 抢同一个键
    （左 Shift 时灵时不灵），最终**显式写 `noop`**（`dev-notes.md` P10 附 2bis）。
- **D2 安装器选中的 `pinyin` 引擎：收敛时移出组，但必须明说。** 组形状仍由 `Desired.IMs` 完全声明（`profile.ConvergeGroup` 现状语义），plan 步骤与 doctor 文案必须指出“将把 pinyin 移出组（引擎与包保留，`omarchy setup input pinyin` 可加回）”。不新增 `--im`。
- **D3 兼容 4.0（4.5 之前，没有 4.1–4.4 这回事）：保留一条 capability 分支。** 探测 `default/input-methods/indicator.py` 是否存在：存在 → 4.5+ 新路径；不存在 → 完全走现有旧路径（一字不改）。升级机因此能被识别并执行一次性清理。

---

## 2. 能力探测（唯一新增的探测面，必须 seam 化）

`internal/observe/observe.go`：

```go
// Current 新增字段
OmarchyManagesIME bool // 4.5+: /usr/share/omarchy/default/input-methods/indicator.py 存在
BarIndicator      bool // shell.json 任一分区含 id == "omarchy.keyboard-layout"
StaleTray         bool // 4.5+ 专有“残留”：drop-in 文件存在 或 pinned 含 "Fcitx" 或 theme hook 存在
ControlSpace      bool // [Hotkey/TriggerKeys] 含 Control+space（本仓库历史写入的残留）
```

- 路径必须走包级 seam（例如 `var omarchyInputMethodsPath = "/usr/share/omarchy/default/input-methods/indicator.py"`），**并同步 `setupFakeHost` 的置假与复原**（AGENTS 规则 7）。
- 现有 `GroupIMs`、`AltTriggerKeys`、`HotkeyOK`、`DropInExists`、`DropInOK`、`PinnedHasFc` 保留：旧路径仍用；4.5+ 只把 `DropInExists`/`PinnedHasFc` 当“残留证据”。

---

## 3. P1 托盘：从“造图标”改成“确认图标 + 清理旧产物”

**终态（4.5+）**：`omarchy.keyboard-layout` 在 bar 上（`omarchy bar put` 幂等）；老 drop-in、老 pin 全部不存在。

| 文件 | 改动 |
|---|---|
| `internal/tray/tray.go` | **保留旧路径的写函数**（`DropInContent`、`DeriveDropInContent`、`WriteDropIn`、`RestartShell`、`SetPinned` 及其私有 helper）——D3 优先：4.0 的旧路径必须一字不改，删了它们旧路径直接不能工作。实现时**没有按最初的删除清单执行**（本表原文写「删」，实施时改为「保留」，见 `docs/dev-notes.md` P10）。4.5+ 不再调用它们。另保留 `RemoveDropIn`（含 legacy 路径）、`ReadPinned`、`RemovePin`、`HasPin`、`DropInEnabled`（判残留）、`ShellJSONPath`。**新增** `RemovePinned(shellJSONPath string) (changed bool, err error)`：读 → 去 `Fcitx` → 原子写，带与旧 `SetPinned` 相同的“写入后重读比对、最多 3 次”竞态保护；只在清理时调用 |
| 新增 `internal/bar/bar.go` | `IndicatorPresent(shellJSON []byte) bool`（纯，T0 可测）；`EnsureIndicator() error` = 跑 `omarchy bar put omarchy.keyboard-layout`，`ShellRunning()` 为假时直接返回 nil；`var Run = ...` seam |
| `internal/converge/install.go` | `hostNeeded` 分支：4.5+ 调用 `writeBarIndicator(opts)`（失败只 `errf` 警告，不阻断）；旧路径保留 `tray.WriteDropIn`。`traySet()` 改成 `trayTerminal(opts, backupDir, cur, needWork)`：4.5+ `bar.EnsureIndicator()` + `tray.RemovePinned`（幂等，输出 `[完成] L4 已移除旧的 Fcitx pin`）。**drop-in 的删除必须发生在 stop 窗口内**（`runStopWindow` 的 4.5+ 分支、`daemon-reload` 之前）：文件删掉了但跑着的 fcitx5 仍带着旧 ExecStart（`--disable notificationitem` 已被剥掉），只有重启才生效；放在窗口外删就会留下「顶栏两个图标到下次登录」。因此 4.5+ 的 `NeedHost` 也要含 `c.DropInExists`（实施修正，见 `docs/dev-notes.md` P10）。`NeedTray` 触发时不再写任何东西 |
| `internal/plan/plan.go` | `NeedTray = c.OmarchyManagesIME ? (!c.BarIndicator \|\| c.StaleTray) : 旧式`；`NeedHost`（4.5+）额外含 `c.DropInExists`（残留 drop-in 只能靠一次 unit 重启生效 → 属 stop 窗口工作；`PinnedHasFc`/`ThemeHookOK` 不需要重启，不算）。步骤标题 4.5+：`顶栏输入法指示器（omarchy.keyboard-layout）+ 清理旧 drop-in/pin`。**JSON key 仍叫 `tray`** |
| `internal/verify/verify.go` | 删 `dropInDetail`；新增 `checkBarIndicator`（OK=`BarIndicator`；失败 Detail 给出 `omarchy bar put omarchy.keyboard-layout`；4.5+ 若 `StaleTray` 则提示残留）。`Doctor` 里该行标题 4.5+ 改成「顶栏输入法指示器」 |
| **（真机补充）`internal/tray` + `internal/observe` + `internal/verify`** | **v1.0 的 `override.conf`**（commit 7c289dc，无 managed header，内容是 `ExecStart=` + `ExecStart=/usr/bin/fcitx5`）仍会覆盖 Omarchy 的 `--replace --disable notificationitem` → fcitx5 继续发 SNI 图标，顶栏依旧两个。实现：`tray.LegacyOverrideRelPath/LegacyOverrideBody/LegacyOverrideMatches/RemoveLegacyOverride`（**只删内容等于两种「我们写过的」body 之一的文件**，通用文件名不能按名字删）、`tray.DerivedDropInBody`（收敛与 observe 共用派生逻辑）、`observe.StaleOverride` + `observe.StaleTray` + `plan.NeedHost`（4.5+，理由同 drop-in：重启才生效）、stop 窗口内删除（先备份）、`observe.EffectiveExecStart`/`ExecStartOK`（读 `systemctl --user show <unit> -p ExecStart --value`，seam `observe.UnitExecStart`）+ L5/doctor 新增一行 `fcitx5 启动参数`（抓任何来源的覆盖，不只是我们的） |
| `internal/converge/commands.go` | `HostReport` 增 `BarIndicator bool \`json:"barIndicator"\``、`StaleTrayArtifacts bool \`json:"staleTrayArtifacts"\``；`dropInOK`/`pinnedHasFcitx` 保留（4.5+ 语义=残留），README 标注 deprecated |

---

## 4. P2 热键与组形状：交还，只留一次性清理

| 文件 | 改动 |
|---|---|
| `internal/hotkey/hotkey.go` | 删写入：`DefaultKeys`、`PruneKeys`、`EnsureTrigger`、`EnsureAltTriggerEmpty`。保留读：`TriggerKeys`、`AltTriggerKeys`、`HasEmptyAltTrigger`。**新增** `RemoveTriggerKey(content, key string) (string, bool, error)`：复用现有 INI 段落解析，删该键后若段落变空**保留空段**（fcitx5“段存在但无键”= 无热键，语义不同于缺段回退默认） |
| `internal/converge/install.go` | `writeHotkey` → `cleanupHotkey`：4.5+ 只删 `Control+space`（及历史 `Alt+space`），**绝不改 `[Hotkey/AltTriggerKeys]`**；一次读→写→一份备份（保持“一份备份不覆盖用户原始快照”的不变量）。旧路径保留 |
| `internal/plan/plan.go` | 4.5+：`NeedHost = !c.ProfileHasRime \|\| groupDrift \|\| c.ControlSpace`；去掉 `!c.HotkeyOK`、`!c.AltTriggerOK`、`!c.DropInOK`。组漂移文案必须列出将被移出的引擎：`catalog.RemovedIMs(c.GroupIMs, want)`（新增纯函数，`internal/catalog`）→ 「将把 pinyin 移出组（引擎保留，`omarchy setup input pinyin` 可加回）」 |
| `internal/verify/verify.go` | 4.5+：`triggerDetail` 改为「触发键归 Omarchy：`Super+I` 循环、左 Shift 进出输入法；Ctrl+Space 留给终端」；`checkAltTrigger` 不再输出（恒真项等于谎话）。`checkGroupIMs` 文案补“未声明的引擎会被移出组” |
| `cmd/ompinyin/main.go` | `--single-im` 保留解析，4.5+ 运行期警告：组内没有 `keyboard-*` 时 Omarchy 的 Setup > Typing > Input Methods 会报错、顶栏指示器不显示。不删 flag（脚本兼容） |
| rime 补丁 | `ascii_composer/switch_key/Shift_L` 改成显式 `noop`（原计划是删掉不写，真机证明会与 fcitx 的 AltTrigger 抢键；`internal/catalog` + `catalog_test`）。属行为变更，写进 `design.md` §5.4 与 `dev-notes.md` P10 附 2bis |

**不改**：`Desired.IMs` 默认 `[rime keyboard-us]`、`profile.ConvergeGroup` 的“恰好等于声明列表”语义（D2 就是它）、`DefaultIM=rime`。

---

## 5. P3 主题：交还 `omarchy-theme-set-fcitx5`

| 文件 | 改动 |
|---|---|
| `internal/theme/theme.go` | 删 `Generate`、`hookScript`、`HookContent`、`HookRelPath`、`HookPath`、`ThemeDirRel`、`ThemeDir`、`ThemeDirPopulated`。保留 `ConfContent`（classicui 的受管键）、`ReadConfContent`、`ConfPath`、`ConfRelPath`、`Observe`（`ConfOK`/`ThemeEqual`/`HookOK` 仅用于残留判定）、`Reload`、`CurrentFont`、`ThemeName`。**新增** `ApplyUpstream() error`（exec seam：`omarchy-theme-set-fcitx5 --activate`；命令不存在 → 返回可忽略错误）与 `RetireArtifacts(home) ([]string, error)`（删 hook 文件、删 `themes/omarchy/background.svg`、`highlight.svg`——只删本仓库生成过的文件名；**不删** `theme.conf` 与 Omarchy 的 symlink） |
| `internal/converge/install.go` | `themeApply`：4.5+ 只写 classicui（`writeThemeFile`）+ 调 `theme.ApplyUpstream()` + `RetireArtifacts`；删除 hook 写入与 `theme.Generate`。hook 的 ledger 记录必须从 `st.ManagedFiles` 退役（`delete(st.ManagedFiles, theme.HookRelPath)`），否则留下 orphan 判定的幽灵 |
| `internal/plan/plan.go` | 4.5+：`NeedTheme = !c.ThemeEqual \|\| c.HookExists`（`HookExists` 用于清理；不再看 `ThemeDirOK`） |
| `internal/verify/verify.go` | `themeCheck` 4.5+ Detail：候选框主题由 Omarchy 的 `fcitx5-theme.conf.tpl` 提供；OK = `ConfOK`（+ hook 不存在） |

---

## 6. 迁移、状态与版本

- `internal/catalog/catalog.go`：`ManagedFormat` 递增到 `1.4.0`（受管文件集合变了：hook 退役、主题产物不再归我们）。
- 顺序不变：先落盘 → `SaveLedger()`；`Save()` 只在成功时。
- 清理项（4.5+ 首次收敛执行一次，全部幂等）：drop-in、pin、theme hook、我们自己生成的两个 svg、（`Control+space` 的）TriggerKeys 键。
- `uninstall`（`internal/converge/commands.go`）补：`theme.RetireArtifacts`；`RemoveDropIn`/`RemovePin` 已在。

---

## 7. 对外契约（脚本可依赖面）

- `plan.need` 的 key 不变（`tray`/`host`/`theme` 语义按上文更新，不新增 key）。
- `HostReport` 只**增**字段（`barIndicator`、`staleTrayArtifacts`），不删不改名；`dropInOK`/`pinnedHasFcitx` 在 4.5+ 表示“残留”。
- 退出码与 `--json` 纪律不变；`--single-im` 仅新增警告。

---

## 8. 测试

**必须改的既有用例**（它们断言的是旧终态）：`internal/hotkey/hotkey_test.go`（写用例）、
`internal/verify/verify_test.go`（`checkAltTrigger`/drop-in）、`internal/plan/plan_test.go`（Need\* 语义）、
`internal/theme/*_test.go`、`internal/converge/install_test.go` 中断言 `0=Control+space`、空 AltTrigger 段、
`HostReport` 字段的用例。

**必须新增的用例**：

1. `bar.IndicatorPresent`：有/无 widget、坏 JSON。
2. `hotkey.RemoveTriggerKey`：删键保留空段；键不存在时 `changed=false`。
3. `catalog.RemovedIMs`：`[keyboard-us pinyin rime]` vs `[rime keyboard-us]` → `[pinyin]`。
4. `observe`：capability 探测的 seam 置真/置假两条路径。
5. **端到端 fixture `setupFakeHost`**：造一台“从 4.0 升级到 4.5”的机器 ——
   老 drop-in 存在、`pinned:["Fcitx"]`、theme hook 存在、`background.svg` 存在、
   `[Hotkey/TriggerKeys] 0=Control+space`、`[Hotkey/AltTriggerKeys]` 缺段、profile `[keyboard-us, rime]`。
   断言一次收敛后：
   - drop-in / pin / hook / svg 全消失；
   - `Control+space` 被删且**空段的 AltTriggerKeys 没有被我们创建**；
   - `profile` 仍是 `[rime, keyboard-us]`（顺序按声明）；
   - 只写了一份备份；
   - 第二次运行 `plan.NeedsApply == false`（幂等），`status --json` 的 `needsApply` 为 false；
   - `doctor` 退出码 0。
6. **D2 告知**：组里含 `pinyin` 的 fixture，plan 步骤文案必须含 “pinyin”。
7. **drop-in 清理必须开 stop 窗口**（实施补充）：只有旧 drop-in 残留的 4.5 fixture → `plan.NeedHost == true`、
   日志里 stop→移除→start 的顺序成立、运行后 drop-in 不存在、第二次运行窗口不开。
8. **v1.0 `override.conf` 残留**（真机补充）：`tray` 三种内容（legacy body / derived body / 用户自定义）→
   删 / 删 / 不删（同目录其它 drop-in 一律不动）；`observe` 识别残留 + `EffectiveExecStart` 解析（含命令失败回退）；
   `plan` 只有 override 残留时 `NeedHost` 为真（pin / theme hook 残留则为假，它们不需要重启）；
   e2e：4.5 + v1.0 override → 一次收敛后删除（**有备份**）、`ExecStartOK` 变真、二次运行 `needsApply:false`；
   用户自定义 override → **不删**，install 在 L5 失败、doctor 指红并点出目录；`verify` 覆盖 `ExecStartOK`
   真/假两条文案，且旧路径不输出该行。

**门槛**：`make fmt && make test`（`go test -race ./...`）全绿。`make lint` 在本机可能因
golangci-lint 版本（2.13.2 < 2.15.0）读不了 Go 1.27 export data 而失败 —— 那是本机工具问题，
记录现象即可，不要为它改代码或 CI。

---

## 9. 文档

- `docs/design.md`：§6.2（热键→归 Omarchy）、§6.4（托盘→顶栏指示器归 Omarchy）、§6.6（主题→归 Omarchy）、
  §2/§5.4（删 Shift 补丁的理由）、§16（不变量 32/33 改写：组形状仍由我们声明；热键/图标/主题不再由我们创建）。
- `docs/dev-notes.md`：新增 **P10**：4.5.0 的 `without_ctrl_space` 会删触发键；`theme-set-fcitx5` 与我们的
  hook 并发写 `themes/omarchy/theme.conf`（现网已混装）；`version` 文件不可信（`4.0.0.alpha`）。
- `README.md` 与 `.agents/skills/ompinyin-install/SKILL.md`：去掉“安装 notificationitem 托盘图标”的承诺，
  改成“顶栏指示器由 Omarchy 提供；ompinyin 保证它在栏上并清理旧产物”；写明左 Shift / Super+I。
- 本文件保留，标题下加“已完成于 vX.Y.Z”。

---

## 10. 验收

自动（必须全绿）：

```bash
make fmt && make test
go build -o bin/ompinyin ./cmd/ompinyin
./bin/ompinyin status --json | jq '.plan.needsApply'   # 只读，不应崩
```

真机（人工执行，需要短暂影响 fcitx5 的一律不在自动化里）：

1. 升级机首次收敛后：顶栏只剩一个输入法指示器（SNI 不再被 pin）；
2. `wtype -M shift -m shift` 后 `fcitx5-remote -n` = `keyboard-us`（上游 AltTrigger 生效）；
3. `omarchy-input-method cycle` 可用，指示器标签随之变化；
4. `omarchy theme set <另一主题>` 后 `~/.local/share/fcitx5/themes/omarchy/` 只有 Omarchy 一个来源、候选框换色；
5. 二次运行 `ompinyin install --dry-run --json` → `needsApply:false`。

---

## 11. 明确不做

- 不加 `--im pinyin` / 不接管 Omarchy 的 Setup > Typing 菜单。
- 不写 IM 环境变量（红线不变）。
- 不改 `--dsp`/rime 数据/`deploy`/`build` 任何逻辑。
- 不删除 Omarchy 的 `omarchy.keyboard-layout` widget，不 pin 任何 SNI 项。
- 不为 4.0 引入新特性；旧路径只保持现状。

---

## 12. 已知风险

- 4.5+ 后 `AltTriggerKeys=Shift_L` 生效 → rime 的 `ascii_composer` Shift 行为变化（D1 已接受，需在 release notes 写明）。
- `omarchy bar put` 需要 shell 在跑；不在时只能靠默认配置（4.5 默认就有该 widget），doctor 会提示。
- 若用户手动把 `indicator.py` 删掉，会误判为旧版本 → 走旧路径（可接受的降级）。
