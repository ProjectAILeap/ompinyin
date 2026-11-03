# AGENTS.md

ompinyin 是**声明式、幂等**的 CLI：把一台 **Omarchy**（Arch + Hyprland + Wayland）主机收敛到
「雾凇全拼（双拼可选）+ 万象 LMDG 模型 + 顶栏输入法图标 + 候选框跟随 Omarchy 主题」的终态。
Go 1.27，仅标准库，`go.mod` 无依赖，MIT。


## 始终需要知道

1. **一切皆 diff。** 收敛命令观测 `current`、与 `desired` 相减、只应用差异；`status`/`doctor` 是只读的。
   任何新行为都必须进 `plan`（`Need`），否则 `--dry-run --json` 与 `needsApply` 会撒谎。
2. **一个停止窗口。** 所有"必须停 fcitx5 才能改"的事都在 `runStopWindow` 里一次做完，`defer` 保证重启；
   只有真有活时才开窗。别为单个文件另开 `stop→write→start`。
3. **受管文件整文件生成**，一律以 `catalog.ManagedHeader()` 开头，字节相同即跳过（无 mtime 抖动）。
   粒度例外：`~/.Xresources` 行级、`classicui.conf` 键级、`hotkey`/`profile` 最小合并。
4. **ledger 先于 desired。** 每层落盘后 `SaveLedger()`；`Save()`（推进 `Desired`）只在成功时。否则失败一次，
   下次运行会把自家文件判成 foreign。
5. **`catalog.ManagedFormat` 是内容形状的版本**，不是构建版本。生成内容形状变了就递增它（老主机只重写一次）；
   把构建戳写进 header 会破坏幂等。
6. **边界**：只针对 Omarchy；绝不写 IM 环境变量（`GTK_IM_MODULE` 等）；只有 `pacman` 与 `source` 走 root；
   不碰遗留 `~/.config/fcitx/rime`；无 TUI、无 `--tray-pin`/`--no-tray`。
7. **I/O 只走 seam**（包级函数变量）；`catalog`/`patches`/`plan`/`profile`/`hotkey` 是纯包，只吃字符串。
   新增 seam 必须同步 `setupFakeHost` 的置假与复原。
8. **打 tag 前必须 `make smoke`**（真机 T4）。T0 在结构上看不见真机（跨挂载点 `rename(2)`、游离 fcitx5、
   信号时序），加测试补不了这个洞。

## 命令

```bash
make build   # go build -ldflags "-X …/catalog.Version=$(git describe)" -o bin/ompinyin ./cmd/ompinyin
make test    # go test -race ./...
make lint    # golangci-lint run --timeout 5m ./...   （硬门槛）
make fmt     # gofmt -w .
make smoke   # 真机 T4 预检：幂等 / 游离实例自愈 / doctor / update --self 跨文件系统（~100s，会短暂重启 fcitx5）
```

退出码：`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检失败 · 第二次 SIGINT `130`。

## 任务 → 去哪读

| 你的任务 | 读这个 |
|---|---|
| 改收敛行为 / 加功能 / 加 CLI flag | `docs/design.md` §3（流程与分层）、§7（CLI）、§16（不变量）；本文件「始终需要知道」 |
| 改某个 L1–L5 模块 | 该模块的包 + `docs/design.md` §4（包结构）+ 对应 §5/§6 小节 |
| 动受管文件 / 所有权 / 备份 | `docs/design.md` §5.1（所有权协议）；`docs/dev-notes.md` D5、P6 |
| 动 rime 补丁（schema_list、grammar、手感项） | `docs/design.md` §2、§5.2、§5.4 |
| 动 X11 HiDPI / 缩放 | `docs/design.md` §6.7；`docs/dev-notes.md` P1、P2 |
| 动 fcitx5 服务启停 / 游离实例 | `docs/design.md` §6.0、§6.3；`docs/dev-notes.md` P5（D-Bus 探测陷阱） |
| 动 tray / 主题 / 候选框外观 | `docs/design.md` §6.4、§6.6 |
| 写/改测试 | `docs/design.md` §9、§15；fixture 见 `internal/converge/install_test.go:setupFakeHost` |
| 发版 / 打 tag | `docs/design.md` §10；本文件「命令」（`make smoke` 是前提） |
| 写文档 | `docs/`：`design.md` 只放方案，开发问题与决策放 `dev-notes.md`，用户问答放 `qa.md` |
| 给用户排障 | `docs/qa.md`（用户语言）；`.agents/skills/ompinyin-install/SKILL.md`（agent 执行手册） |

## 永远不要做

- **在 fcitx5 服务停止时**用 `fcitx5-remote`/任何 D-Bus 调用探测存活（会激活游离实例占住唯一总线名）。
  存活只用 `pgrep`。细节：`docs/dev-notes.md` P5。
- 手改受管文件来"修"问题（下次收敛会覆盖）；要改行为就改参数/代码。
- 在没递增 `ManagedFormat` 的情况下改动受管文件的正文形状。
- 把 `DESIGN`/设计理由塞进代码注释以外的散落文档：方案进 `design.md`，决策与事故进 `dev-notes.md`。

## CLI 契约（脚本/agent 可依赖的表面）

- `--json` → stdout 纯 JSON（诊断走 stderr）：`status --json`、`doctor --json`、
  `install/switch/update --dry-run --json` → `{tool,version,command,desired,plan}`，
  `plan = {need:{l1,l2,l3,deploy,host,service,tray,theme,hidpi}, steps, needsApply}`。
  **这三个命令 `--json` 而不加 `--dry-run` 是用法错误（exit 2）**。
- 非交互：`-y/--yes`。干净序列 = `--dry-run --json` → 断言 `plan.needsApply` → `-y`。无 tty 时用 `sudo -n`。
- 完成信号：`plan.needsApply:false`（或 `status` 无差异）= 没事可做，停止。
- 布局 ID（`--dsp`）：`quanpin`（默认，唯一非双拼）· `zrm`/`flypy`/`mspy`/`sogou`/`abc`/`ziguang`/`jiajia`；
  `--dsp-default`/`--no-quanpin` 需配 `--dsp`；最多一个双拼。
- 详细 CLI 表与 flag 组合规则：`docs/design.md` §7。
