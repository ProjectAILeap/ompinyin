# AGENTS.md

ompinyin 是一台声明式、幂等的 CLI，在 **Omarchy**（Arch + Hyprland + Wayland）上一键部署中文输入法栈：**雾凇全拼（`rime_ice`）+ 万象 LMDG 语法模型 + 顶栏 tray 图标**。双拼可用 `--dsp` 可选。

```text
语言：Go 1.27（仅标准库，go.mod 无依赖）· 许可证：MIT
命令：install/update/switch/status/doctor/clean/uninstall/source
流程：lock → facts → desired → current → plan →(dry-run)→ backup → L1..L5 → verify → state → unlock
```

设计权威：[DESIGN.md](./DESIGN.md) —— 全文用 § 编号引用。修改收敛模型或 CLI 表面前，先读对应的 §。本文档只标明编码 Agent 需要前置知道的东西：流程、非显见的坑、以及边界。

> **读你需要的切片，而不是全部。** 这里没有装饰性的内容；但每个 token 在每次请求都会被加载。按你的任务选一行：

| 你的任务 | 先读 | 再读 |
|---|---|---|
| 修改收敛行为 / 加功能 | 「它做什么」→「关键陷阱」 | DESIGN §3 / §5 |
| 新增 / 修复某 L1–L5 模块 | 「分层模型」→「关键陷阱」 | 该模块代码 + DESIGN §16 |
| 写 / 扩展测试 | 「分层模型」→「测试」 | `setupFakeHost` fixture |
| 改 fcitx5 / rime / tray | 「关键陷阱」→「边界」 | DESIGN §5 / §6 / §16 |
| 加 CLI flag / 子命令 | 「CLI 契约」 | `cmd/ompinyin` 的 flag 解析 |
| 用 Agent 脚本化 ompinyin | 「CLI 契约」 | exit codes + `--json` 结构 |

---

## 命令

```bash
make build   # go build -ldflags "-X …/internal/catalog.Version=$(git describe)" -o bin/ompinyin ./cmd/ompinyin
make test    # go test -race ./...
make lint    # golangci-lint run --timeout 5m ./...   (配置：.golangci.yaml)
make fmt     # gofmt -w .
make release-check  # goreleaser check .goreleaser.yaml
```

`catalog.Version` 是由 ldflags 注入的变量（`ompinyin version` / 下载的 User-Agent 会打印它）——它**不在** `ManagedHeader()` 里，后者盖的是 `catalog.ManagedFormat`。若在头部盖构建戳，每次编译都会改写每个受管文件的内容 → 每跑一次就强制 L3 重写 + 完整 rime 重建，从而破坏幂等性。当生成内容形状变化时应递增 `catalog.ManagedFormat`，让旧主机只重写一次。

退出码：`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检失败（非 Omarchy / 磁盘 / root / 缺工具）· 第二次 SIGINT 硬退出 `130`。

---

## 它做什么

收敛命令（`install`/`update`/`switch`）把主机收敛到单一 `catalog.Desired` 终态：把观测到的 `current` 与 `desired` 做差，只应用差异，幂等、可 `--dry-run`。`status`/`doctor` 是**只读**的 diff / 体检报告——不落盘、不收敛，可安全随时运行。

顶栏图标**不**属于 `Desired`；它是 L4 恒量：每次收敛都启用 `notificationitem` **并** pin `Fcitx`。**没有** `--tray-pin`/`--no-tray` 这个 flag。

### 分层模型

应用过程是 5 层收敛；顺序很重要（见「关键陷阱」）：

```text
L1 软件包（fcitx5-rime/configtool/fcitx5-gtk）→ L2 资源（下载、sha256、解压）
→ L3 受管 *.custom.yaml → L4 profile/hotkey/drop-in + 服务启停 + tray drop-in + shell.json pin → L5 只读校验。
系统操作走包级函数变量（"exec seam"）。octagram 是 librime 插件，不是 pacman 包。
```

纯包（`catalog`、`profile`、`hotkey`、`plan`）只接收字符串输入、保持 I/O 无关——无需假主机即可单测。

---

## 关键陷阱（本文档存在的理由）

这些代价是真实的主机 bug；光看代码不会告诉你它们为什么重要。

1. **单一停止窗口。** 别对 profile/hotkey/drop-in 分开 `stop→write→start`——那是竞态。所有工作在同一个停止窗口里做，且**只在里面有活干时**才打开（需要 deploy 或主机配置已变）。`runStopWindow` 通过 `defer` 关闭它，所以任何提前 return 都不会让 fcitx5 停在停止态（启动失败会把退出码降为 1）。

2. **`rime_deployer --build` 在 fcitx5 停止时运行**，且 `default.custom.yaml` 的 `schema_list` **必须**用 `- schema: <id>` 这种 map 形式。`rime_deployer --build <userDir> /usr/share/rime-data <buildDir>` 只会对 list-of-maps 编译所有已启用 schema；裸 `- <id>` 形式会被**忽略** → `--build` 只产出 `build/default.yaml` 脚手架（exit 0，"0 success"），而那残留的 `build/` 会让 fcitx5-rime 跳过自己的懒部署 → **没有任何 schema 被编译 → 打不出中文**。这是真实主机根因。当 L3 内容变化时应重新 build，而不是只在产物缺失时。`--compile` 永远不需要。

3. **tray pin 是读→合并→写，绝不是裸覆盖**。裸 `omarchy bar set ... pinned '["Fcitx"]'` 会丢掉用户的 pin。代码把**数组**直接原子写入 `shell.json`，因为在 4.0.1 上 `bar set --json` 和 `shell setBarWidget` 都会把数组强转成字符串，导致图标没被 pin（`Tray.qml` 需要数组）。随后 `omarchy restart shell` 让 shell 重新枚举刚启用的 SNI 项。

4. **L2 资源 tag pin + 两段式完整性。** Stable 通过 releases API pin 一个发布 tag（`assets.ResolveStableTag`；CI 从不碰网络）；只有当**没有可用 pin** 时才查 API，所以被墙的 `api.github.com`（国内常见）不会重试/报警——`update` 是唯一重新 resolve 的入口。Nightly 绝不用 NJU 的 `LatestRelease` 镜像（它会提供 stable 字节）。完整性分两段：**形状门**（`MinBytes` + zip magic）拒绝以 HTTP 200 返回的报错/portal 页；然后是 **ledger 交叉校验**——不可变 tag（rime-ice 日期 tag）不匹配是**硬错误**，可变 tag（wanxiang `LTS`、nightly）只警告。普通安装绝不覆盖已放置的文件；只有 `update` 才刷新。`.part` 断点续传**仅限同源**（`.part.src` 边车文件；切换镜像会删掉还没下完的部分）。

5. **Ledger 先于 Desired。** `SaveLedger()` 在每一层把字节落盘后都执行（L2、L3、停止窗口，以及 L5 失败时），持久化 `ManagedFiles`/`Assets`，同时让 `Desired`/`SchemaList` 保持磁盘上的值。否则，一次在 L3 之后失败的收敛，会让下一次运行把 ompinyin 自己的文件归类成 `StatusForeign`。`Save()`（推进 `Desired`）只在成功时执行。

两层切换（别混淆）：**fcitx5 IM**（`keyboard-us` ↔ `rime`，触发键 `Alt+Space`——选它为了避开 Omarchy herdr 的 Ctrl+Space 前缀）vs **Rime schema**（`rime_ice`/`double_pinyin`…，**F4**）。

---

## 测试

CI（**绝不**跑系统操作、**绝不**碰网络）：gofmt → `go vet` → `golangci-lint run` → `go test -race` → build + version 冒烟。Lint 是硬门槛（好几个已评审的缺陷都是 lint 类 bug）。覆盖率以 T0 stub 为主；真机校验（T1–T4）为手动，见 DESIGN §15。

- **T0 stub**（CI）：临时 HOME + systemctl/rime_deployer/pacman/omarchy 的假实现 + 被 stub 掉的 `assets.ResolveStableTag`。标准 fixture 是 `internal/converge/install_test.go` 里的 `setupFakeHost` —— 新增收敛路径或 exec 调用时**照着它做**。seam 名就是那些包级函数变量（`facts.Run`、`facts.LookPath`、`pkgs.Run`、`service.Run`、`deploy.Run`、`tray.Run`、`assets.ResolveStableTag`，…）。
- **T1–T4**（手动）：Distrobox Arch · systemd-nspawn · QEMU/KVM Omarchy · 真机幂等重跑。
- Golden 测试锁定官方语法常量（`internal/catalog/catalog_test.go`）。

---

## 代码风格

只写 `go fmt`/`go vet`/golangci 默认集不会告诉你的：

- **I/O 只走 seam** —— 别在收敛路径里内联 `exec.Command`/`os.Open`；经由包级函数变量，让 T0 能造假其实现。
- **errcheck 守护的是承诺，不是风格** —— 用 `.golangci.yaml` 里的显式 `exclude-functions` 清单；任何守护用户可见承诺（覆盖前备份、进缓存前校验、终态前 `SaveLedger`）的错误都必须检查。
- **受管文件是整文件生成、绝不 YAML 合并**，每个都以 `catalog.ManagedHeader()` 开头；字节相同的文件会被跳过（无 mtime 抖动）。
- **注释写*为什么*，不写*是什么*** —— 根因 + ADR/DESIGN 引用强过复述代码。

---

## Git 工作流

- **提交风格**：Conventional Commits（`fix:` · `feat:` · `docs:` · `refactor:` · `chore:` · `test:`）；一次提交只做一个逻辑变更；包级改动加作用域（`fix(L1): …`）。goreleaser 的 changelog 会排除 `docs:`/`chore:`/`test:` —— 别乱用这些前缀。
- **发布**：在 `main` 上打 `v*` tag → goreleaser 构建（`.goreleaser.yaml`）。changelog 由 git log 生成 → 提交正文写成人能读的。生成内容形状变化应走 `catalog.ManagedFormat`，而不是一个无说明的版本号跳跃。
- **绝不提交**：`bin/`、`dist/`、生成的 rime 产物、机器本地路径、密钥（见 `.gitignore`）。
- **CI 纪律**：workflow 里不加网络、不加系统操作（T0 假实现 + `--mirror <dir>`）。

---

## 边界

硬技术恒量（完整清单 + 理由）：**DESIGN §16**。下面是设计层面 Agent 不可越过的决定：

- 无 TUI；无 `--tray-pin`/`--no-tray`（tray 是必须的）；只针对 Omarchy（不做多发行版抽象）。
- 绝不写 IM 环境变量（`GTK_IM_MODULE`、`~/.xprofile`）；绝不碰遗留的 `~/.config/fcitx/rime`。
- 受管文件整文件生成、绝不合并；绝不静默覆盖用户手改（owner-ledger → 确认 / `--yes` + 备份）。
- 语法模型进**每一个**已启用 schema，且用官方常量；`radical_pinyin`/`melt_eng` 的 algebra 跟随 `Primary`。
- 只有 `pacman` 与 `source` 以 root 跑（都经 `sudo` 提权；`ompinyin source` 内部 sudo，**勿** `sudo ompinyin source`）；预检拒绝 root 与缺失的 `rime_deployer`/`fcitx5-remote`/`omarchy`。
- `install` 把持久化的 `Desired` 当作基线——只覆盖确实出现在命令行的 flag。
- 清理孤儿（ledger − desired）；绝不删除 ompinyin 没写过的文件。
- 不管理 `Shift`/`Control`/Caps/`ascii_punct`/`full_shape` 这些"手感"设置（用户所有）；不装白霜（`rime-frost`）也不装万象 scheme（万只作**模型**用）；走 `tray.SetPinned`，绝不手改 `shell.json` 或直接调 `omarchy bar set`。

---

## CLI 契约（给 Agent）

机器可动作的表面。保持 flag 与退出码稳定——改动会破坏 agent 提示词与脚本。

- **退出码**（§7）：`0` 成功 · `1` 执行失败 · `2` 用法错误 · `3` 预检失败 · 第二次 SIGINT 后 `130`。
- **`--json`** → stdout 是纯 JSON（诊断走 stderr）：`status --json`、`doctor --json`，以及 `install/switch/update --dry-run --json` → `{tool,version,command,desired,plan}`，其中 `plan = {need:{l1,l2,l3,deploy,host,tray}, steps, needsApply}`。**这三个命令 `--json` 而不加 `--dry-run` 是用法错误（exit 2）**——stdout 绝不混人类文本与 JSON。
- **非交互**：`-y/--yes`；干净序列 = `--dry-run --json` → 断言 `plan.needsApply` → `-y`。无 tty 时自动用 `sudo -n`（需要给 pacman 配 NOPASSWD sudoers；绝不 run as root）。
- **完成信号**：`plan.needsApply:false`（或 `status` 无差异）= 没事可做——停止，别再跑。
- **`update --self`**：替换二进制（备份旧的、按 `checksums.txt` 校验 sha256、原子替换）。
- **布局 ID（`--dsp`）**：`quanpin`（默认，唯一的非双拼）· `zrm`/`flypy`/`mspy`/`sogou`/`abc`/`ziguang`/`jiajia`。`--dsp-default`/`--no-quanpin` 需要配 `--dsp`；**最多一个**双拼。
- `--os-override` 绕过 `ID=omarchy` 预检（仅 VM/CI）；`--mirror` 接受预设 / URL / 本地目录（离线 T1/T3 播种）。
