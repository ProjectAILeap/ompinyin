# ompinyin — Omarchy 中文输入法一键部署 CLI（设计文档 · v1.0）

> **一句话定位**：在 Omarchy（Arch + Hyprland + Wayland）上以「声明式终态收敛」一键部署中文输入法——**雾凇全拼 + 万象 LMDG + 顶栏输入法图标**；双拼按需可选。
>
> 本文档描述 **v1.0** 的收敛模型与 CLI 表面。许可证 **MIT** · 无 TUI。

---

## 1. 目标与范围

### 1.1 范围

- 仅面向 Omarchy（Arch + Hyprland + Wayland），**不做多发行版抽象**。
- 纯 CLI，支持 `--yes` 全自动；v1 无 TUI。
- 前提：`ID=omarchy`、磁盘空闲 ≥2GB、图形会话中的普通用户；`fcitx5-remote` / `rime_deployer` / `omarchy` 在 PATH。

### 1.2 默认终态（零选项 `ompinyin install`）

| 维 | 值 | 说明 |
|---|---|---|
| 词库 | **雾凇 `rime_ice`** | v1 唯一方案包 |
| 输入布局 | **全拼** | `schema_list` 只含 `rime_ice`，F4 菜单干净 |
| 整句模型 | **万象 LMDG** `wanxiang-lts-zh-hans.gram` | 挂到启用方案上，不是万象拼音方案 |
| 顶栏输入法指示器 | **在 bar 上** | Omarchy 4.5+ 自己渲染（`omarchy.keyboard-layout`）；本工具只 `omarchy bar put` 确认 + 清掉旧版 drop-in/pin。无 `--no-tray` |
| 热键 | **归 Omarchy** | `Super+I` 循环输入法、左 Shift 开/关输入法、`F4` 选方案；本工具不写 `[Hotkey/*]`，只删旧版写过的 `Control+space` |
| 候选框 | **跟随当前 Omarchy 主题（默认）** | classicui.conf 的键归我们，配色由 Omarchy 的 `omarchy-theme-set-fcitx5` 生成 |
| 宿主 | fcitx5-rime + Omarchy 用户服务 | 一行环境变量都不写 |

### 1.3 非目标

- 不装**万象拼音方案**；万象只作为 LMDG **模型**。
- 不装五笔 / 墨奇音形 / 九键 / 云拼音。
- 不写 `GTK_IM_MODULE` / `~/.xprofile` / 任何 IM 环境变量。
- 不管 ibus、fcitx4、非 Omarchy 发行版。
- 手感项只受管一组**有默认答案**的集合（§5.4）：`page_size`、`,.` 翻页、中英切换（左右 Shift）、`Control`/Caps 的不误切、全半角从半角起步且不跨会话记忆；`ascii_punct`/简繁/emoji 的**默认值**和其余修饰键仍归用户。
- v1 无 TUI。

---

## 2. 终态空间：Layout × Model

工具内的「期望终态」是一个结构体，不是硬编码的五个文件名。`install` / `update` / `switch` / `status` / `doctor` 都对同一份 `Desired` 做 diff。

```text
Desired
├── Primary    布局 ID              默认 quanpin（schema_list[0]）
├── Extra      []布局 ID            默认空；--dsp 时追加
├── Model      bool                 默认 true（万象 LMDG）
├── Channel    stable | nightly     默认 stable（--channel 选项，见 §5.3）
└── IMs        []IM ID              默认 [rime keyboard-us]（--single-im / --english-im，见 §6.1）
```

顶栏输入法指示器不是 Desired 的可选项，是 L4 不变量：每次收敛都必须达到 §6.4（Omarchy 4.5+ 由 Omarchy 自己渲染，本工具只保证它在栏上并清理旧产物）。

### 2.1 布局目录

CLI 与状态清单使用短 ID；落到磁盘的是雾凇 schema 名。algebra 来自官方 `others/recipes/config.recipe.yaml`（`algebra_${schema}`）。

| ID | 名称 | schema | radical / melt algebra |
|---|---|---|---|
| `quanpin` | 全拼 | `rime_ice` | `algebra_rime_ice` |
| `zrm` | 自然码 | `double_pinyin` | `algebra_double_pinyin` |
| `flypy` | 小鹤 | `double_pinyin_flypy` | `algebra_double_pinyin_flypy` |
| `mspy` | 微软 | `double_pinyin_mspy` | `algebra_double_pinyin_mspy` |
| `sogou` | 搜狗 | `double_pinyin_sogou` | `algebra_double_pinyin_sogou` |
| `abc` | 智能 ABC | `double_pinyin_abc` | `algebra_double_pinyin_abc` |
| `ziguang` | 紫光 | `double_pinyin_ziguang` | `algebra_double_pinyin_ziguang` |
| `jiajia` | 拼音加加 | `double_pinyin_jiajia` | `algebra_double_pinyin_jiajia` |

### 2.2 `schema_list` 生成规则

| 选项 | Primary | Extra | F4 菜单 |
|---|---|---|---|
| （默认） | `quanpin` | — | 仅全拼 |
| `--dsp zrm` | `quanpin` | `zrm` | 全拼（默认）+ 自然码 |
| `--dsp flypy --dsp-default` | `flypy` | `quanpin` | 小鹤（默认）+ 全拼 |
| `--dsp zrm --no-quanpin` | `zrm` | — | 仅自然码 |
| `--dsp-default` 且无 `--dsp` | 非法 | — | 用法错误（exit 2） |

约束：

- v1 **最多一个双拼**。`--dsp` 可重复视为错误。
- `radical_pinyin` / `melt_eng` 的 algebra **跟随 Primary**。F4 切到 Extra 方案时反查/英文派生不匹配——与上游行为一致，写入 README。
- `schema_list` 条目**必须**写成 `/schema_list` 子项的 `- schema: <id>` map 格式。短格式 `- <id>` 会被 `rime_deployer --build` 忽略（只写 `build/default.yaml` 骨架、不编译任何 schema），导致 fcitx5-rime 无 schema 可载入、全局打不出中文。

### 2.3 两层切换，不要混搞

| 层 | 谁在这层 | 谁来切 | 本工具怎么验证 |
|---|---|---|---|
| **中英**（引擎内） | rime 的 `ascii_mode` | 右 Shift（`commit_code`，受管手感项）。左 Shift 在 rime 里是 `noop`（§5.4），因此**不会**在这里生效 | `fcitx5-remote` 三态 1↔2；人工按 Shift 看候选框 |
| **Rime schema** | `rime_ice` / `double_pinyin` / … | **`F4`** 呼出方案选单 | 人工按；读 `user.yaml` 的 `previously_selected_schema`。**禁止**用 `fcitx5-remote -s <schema>` |
| **fcitx5 IM** | profile 里的 IM 组（默认 `[rime keyboard-us]`，§6.1） | **`Super+I`** 换当前 IM（Omarchy 的 `omarchy-input-method cycle`）；左 Shift 只开/关输入法、不改 IM（落点见 §2.3 末）；旧版用的是 `Ctrl+Space` | `fcitx5-remote` 三态 1↔2 |

三条轴互不重叠：中英在引擎里（不受 fcitx5 激活态影响），方案在 rime 里（不影响其它应用），
停用是 fcitx5 层（**停用时 rime 收不到键，方案与中英热键都不生效**，这是预期）。

键位选择的理由（都踩过）：

- **`F4` 是方案选单唯一入口。** 上游 rime-ice 一次绑了 `F4`/`Control+grave`/`Control+Shift+grave`，
  而 `Control+grave` 与 VS Code 的终端开关、JetBrains 的 Quick switch scheme 直接冲突——误触一次就多开一个终端，
  所以只留 `F4`（`catalog.SwitcherHotkeys`）。
- **热键归 Omarchy（4.5 起）**：`Super+I` 循环、`Super+Shift+I` 反向、左 Shift 开/关输入法（`AltTriggerKeys=Shift_L`，不改组内 IM）、
  `Ctrl+Space` 在终端里是 non-consuming 直通。本仓库**不再写触发键**——上游 `configure.py::without_ctrl_space()`
  每次 `--defaults` 都会删掉 `Control+space`，迁移脚本还会把空的 `[Hotkey/AltTriggerKeys]` 段填回 `0=Shift_L`，
  长期打补丁等于永久战争（`dev-notes.md` P10）。
- **旧的 `Ctrl+Space` 只在 4.0 仍然写入**（那些机器上热键本来就归我们，`hotkey.EnsureTrigger` 原样保留）。
  4.5+ 上收敛只**删**掉 `Control+space` / `Alt+space`，且绝不创建 `[Hotkey/AltTriggerKeys]`。
- **冲突仍要能看见**：`observe.HerdrPrefix` 按 herdr 自己的解析顺序读 `$HERDR_CONFIG_PATH` →
  `~/.config/herdr/config.toml`，`HerdrConflict` 与之归一比较；旧路径写触发键时才告警（4.5+ 没有我们写的键，
  告警只会是噪音）。
- 组内 IM 数仍是前提（§6.1）：fcitx5 的所有 IM 热键都以 `canTrigger()`（条目 > 1）为前提，Omarchy 的热键也一样。

---

## 3. 架构：五层终态收敛模型

**声明式而非过程式**：每次执行走同一条路径——采集现状 → diff 终态 → 计划 →（dry-run 预览）→ 执行差异 → 复核终态。

由此获得：幂等、dry-run 与真实执行同源、install/update/switch 同一条收敛路径、status/doctor 即现状 diff 报告。

```text
lock（CheckHome 已在 CLI 入口一次性校验）
facts（预检）
desired ← 以 state.json 的上次终态为基线，只应用命令行显式给出的选项
current ← observe()
plan    ← diff(desired, current, forceL2=update 或 channel 变更)   # 同时产出 Need* 谓词
if dry-run: print; unlock; return
backup  （仅当 plan.NeedsApply() 或 -b；同秒碰撞自动加后缀）
L1 pkgs
L2 assets（plan.NeedL2 才跑：install 不覆盖已落位文件；update/channel 变更强制刷新）→ SaveLedger
L3 patches（字节已一致 → 跳过，不碰 mtime）→ SaveLedger
if deploy(产物缺失/配置变更) or 宿主(profile/hotkey/drop-in) 有工作:
  stop fcitx5                        # 唯一一次 stop，窗口一直开到 start
       rime_deployer --build         # 写盘，必须在 fcitx5 停着时做；产物齐且配置未变时跳过
       L4 写 profile（+ 旧路径的 hotkey/drop-in；4.5+ 只删旧触发键残留）
       daemon-reload
  start fcitx5                       # defer 守门：任何提前返回都会 start，start 失败 → 退出码 1
  SaveLedger
L4 顶栏指示器（4.5+：omarchy bar put + 删旧 drop-in/pin；旧路径：写 drop-in + pin + 重启外壳）
L4 候选框主题（classicui.conf + 交给 omarchy-theme-set-fcitx5 + 退役旧钩子/SVG + DBus 热重载）
L5 verify（只读，build 缺失为硬失败）
if L5 未达终态: SaveLedger（只存事实）; return 1     # 不推进 Desired
write state.json（含 Desired）+ 备份轮转（留最近 5 个）
unlock
```

**幂等零扰动**（收敛后重跑）：L2 谓词不成立 → 整层不跑 → L3 字节一致全跳过 → stop 窗口不开 → 不建备份目录 → L5 复核全绿。`plan` 除了输出步骤文本，还输出 `NeedL1/L2/L3/Deploy/Host/Tray` 谓词，`Install` 消费同一组谓词，杜绝「计划 [跳过]、执行重做」的脱节。

落地形态：每层一个 `converge()`，不引入通用 reconciler 框架。**`rime_deployer --build` 不是 L5**：L5 只读；build 是 L3 与 L4 start 之间的缝。

| 层 | 内容 | 变更方式 |
|---|---|---|
| L1 系统包 | fcitx5-rime fcitx5-configtool fcitx5-gtk（最小集：fcitx5/fcitx5-qt/librime/opencc 由依赖闭包带入；octagram= librime 的 librime-octagram.so 插件，非独立包）；`xorg-xrdb` 仅 `--x11-hidpi` 时加入 | `pacman -S --needed --noconfirm`（`--yes` 时） |
| L2 数据资产 | 雾凇 `full.zip` + 万象 `.gram`，带版本号和 sha256 | 下载 → 校验 → 解压/落位 |
| L3 配置覆盖 | 受管 `*.custom.yaml`（集合随 Layout 变） | 整文件生成（所有权协议见 §5） |
| （缝）部署 | `rime_deployer --build $RIME /usr/share/rime-data $RIME/build` | **fcitx5 必须已停** |
| L4 宿主集成 | profile、start、**顶栏输入法指示器、候选框主题**（4.5+ 三者都由 Omarchy 提供，本工具只确认与清理）；旧路径另含触发键与 drop-in；可选的 X11 HiDPI 兼容模式 | 一个 stop 窗口（见 §6.0）；指示器与候选框主题在 stop 窗口之外处理 |
| L5 验证 | 编译产物、模型编入、IM 三态、指示器在栏上、候选框主题、人工 checklist | **只读** |

> **L1 仓库（收敛不接管镜像）**：三个 L1 包都在 **`extra`** 仓库（`stable-mirror.omarchy.org/extra`）。`pacman -S` 的镜像来自系统 `/etc/pacman.d/mirrorlist` + `/etc/pacman.conf`，`install`/`update`/`switch` 收敛**不接管、不修改**。镜像改动的唯一入口是独立的 **`source`** 命令（§7）。`--mirror` 只作用于 **L2 资产**，对 L1 无效。Omarchy 官方 `stable-mirror.omarchy.org` / `pkgs.omarchy.org` 走 Cloudflare CDN，大陆直连可能不稳；处理方案：代理 / `XferCommand` 重试续传 / `source --preset cn` 把 `core`+`extra` 指向国内 stock-Arch 镜像——注意 stable 是**锁版本快照**有漂移风险，`[omarchy]` 仓库**无国内镜像**。

---

## 4. 技术栈与包结构

语言 **Go**（stdlib only，`go.mod` 无第三方依赖）；产物为单静态二进制，交叉编译、分发零依赖。许可证 **MIT**，v1 无 TUI。

```text
cmd/ompinyin        CLI 入口：flag 解析、命令分发
internal/catalog   布局目录、grammar 常量、资产 URL（纯数据，无 I/O）
internal/facts     预检：os-release(ID=omarchy)、octagram、磁盘≥2GB、herdr prefix
internal/pkgs      pacman -S --needed（sudo exec）
internal/assets    L2：下载→校验→缓存→解压（含 zip 安全扫描）
internal/patches   L3：受管 custom.yaml 模板生成 + 所有权哈希记账
internal/profile   fcitx5 profile INI 宽容读/严格写
internal/hotkey    触发键读 + 白名单校验（写仅旧路径；4.5+ 只删残留 Control+space）
internal/tray      L4 顶栏（旧路径）：专用 drop-in + shell.json pinned 读/并/写数组 + 重启外壳；4.5+ 只用来删残留
internal/bar       L4 顶栏（4.5+）：shell.json 里 omarchy.keyboard-layout 是否在栏上 + `omarchy bar put`
internal/theme     L4 候选框主题：classicui.conf 的键 + 交给 omarchy-theme-set-fcitx5 + 退役旧钩子/SVG + 热重载
internal/hidpi     L4 可选 X11 HiDPI：~/.Xresources 行级块 + 登录/监听 user 单元 + Xft.dpi 发布
internal/deploy    rime_deployer --build $RIME /usr/share/rime-data $RIME/build
internal/service   systemctl --user 封装（omarchy-fcitx5 优先）
internal/state     ~/.local/state/ompinyin/state.json 状态清单
internal/observe   采集当前宿主快照（Current）用于 diff
internal/verify    L5 只读复核 + doctor checklist
internal/plan      diff(desired, current) → 有序步骤列表
internal/source    独立 pacman 仓库镜像助手：写 /etc/pacman.d/mirrorlist（sudo）
internal/selfup    update --self：自升级二进制（备份 + checksums.txt 校验 + 原子替换）
internal/execcmd   exec seam 的公共底座：exec.CommandContext 绑定运行 context（SIGINT 一并终止子进程）
```

纯包（`catalog`、`profile`、`hotkey`、`plan`）只吃字符串输入、无 I/O，便于无假宿主单测。

---

## 5. 数据与配置管理（红线 + 所有权协议）

### 5.1 受管文件（随终态变化）

受管文件 **整文件重生成，绝不合并 YAML**。文件头：

```text
# managed by ompinyin v1.0.0 — hand edits will be overwritten
```

头里的版本号是 **`catalog.ManagedFormat`（受管文件格式版本）**，不是构建版本。构建版本（`catalog.Version`，ldflags 注入 git describe）只用于 `ompinyin version` 与下载 User-Agent。把构建号写进头会让每次构建都改变所有受管文件的字节 → 每次收敛都触发 L3 重写 + 全量重编译，幂等承诺被摧毁。`ManagedFormat` 只在生成内容**形状**变化时 bump，届时老主机恰好重写一次。

| 文件 | 何时受管 | 内容 |
|---|---|---|
| `default.custom.yaml` | 始终 | `schema_list`（§2.2，**map 格式**）+ `menu/page_size: 9` + `,.` 翻页 |
| `<schema>.custom.yaml` | schema_list 中每个方案且 Model=true | 官方 grammar 块（**每个启用方案各打一份**） |
| `radical_pinyin.custom.yaml` | 始终 | `speller/algebra.__include` → Primary 的 algebra |
| `melt_eng.custom.yaml` | 始终 | 同上 |

Model=false 时不生成方案级 grammar 文件（若文件是本工具上次写的则删除，手改过则走所有权协议）。

**内容哈希记账**（状态清单）：重写前比对——

| 磁盘上的文件 | 行为 |
|---|---|
| 不存在 | 写入 |
| 哈希 == 上次工具生成 | 正常重写 |
| 哈希不匹配（用户改过） | 交互确认 / `--yes` 备份后覆盖并明示 |
| **无记账哈希**（他方写入 / 手改遗留） | **视为外来**：同样确认 / `--yes` 备份后整文件替换 |

绝不静默吞掉手改。`--yes` 首次安装时，会换掉先前 rime 配置/手改留下的 `default.custom.yaml`——这是既有取舍的后果，README 必须写明，备份在 `backup-<ts>/`。

用户长期自定义写**非受管叠加补丁**（rime patch 机制支持多文件）；工具承诺永不触碰非受管文件。

### 5.2 Grammar 常量（官方 recipe，golden 锁死）

来源：雾凇 `others/recipes/grammar.recipe.yaml`（`schema=` 支持全拼和全部双拼）。本工具按**官方常量**打进 schema_list 中**每一个**启用方案（每个方案各打一份），并按统一参数，不因方案而异。

```yaml
grammar:
  language: wanxiang-lts-zh-hans
  collocation_max_length: 6
  collocation_min_length: 3
  collocation_penalty: -14
  non_collocation_penalty: -6
  weak_collocation_penalty: -100
  rear_penalty: -20
translator/contextual_suggestions: false
translator/max_homophones: 8
```

### 5.3 资产与版本

| 数据 | 官方上游 | 国内镜像（CN） | 加速代理（Proxy） |
|---|---|---|---|
| 雾凇 `full.zip`（纯数据，不含 `*.custom.yaml`） | `https://github.com/iDvel/rime-ice/releases/latest/download/full.zip` | NJU：`https://mirror.nju.edu.cn/github-release/iDvel/rime-ice/LatestRelease/full.zip` | `gh-proxy.com` / `ghfast.top` 包裹上游 URL |
| 万象 `wanxiang-lts-zh-hans.gram`（~420MB） | `https://github.com/amzxyz/RIME-LMDG/releases/download/LTS/wanxiang-lts-zh-hans.gram` | CNB：`https://cnb.cool/amzxyz/rime-wanxiang/-/releases/download/model/wanxiang-lts-zh-hans.gram` | 同上 |

- 通道 `--channel stable|nightly`，**默认 stable**。stable 走 `RimeIceTagged(<解析出的具体 tag>)`：先经 releases API（直连→ghproxy 回退）解析最新非 nightly release（如 `2026.06.30`），上游 URL、NJU 镜像（支持 `/github-release/<owner>/<repo>/<tag>/` 路径）与 ghproxy 三路候选同 tag 逐字节一致；解析失败回退 `releases/latest`（该 repo 上是滚动 nightly）。nightly 走 `.../download/nightly/full.zip`，**不混用 NJU LatestRelease**（那是 stable 快照，会静默击穿通道）。
  - **解析时机**：清单里已记有具体 stable tag 时，plain `install` 直接沿用（不重复查 API，否则大陆用户 api.github.com 不可达会每次收敛都重试）；只有当没有可用 pin、或本次是 `ompinyin update`（forceRefetch，唯一重新 pin 的入口）时才查 API。`update` 解析失败时沿用已 pin 的 tag（重新下载同一 tag 仍有意义），不会静默退回 nightly 字节。
  - **诚实报告**：回退到 `releases/latest` 时实际构建就是滚动 nightly。`status` 会比对 `channel` 与记账 tag，不一致时提示「channel=stable 但未 pin 到具体 stable tag（实际构建=nightly）」，不假装 pin 住了。
  - GitHub `releases/latest` 本身是滚动 nightly；NJU `LatestRelease` 镜像喂的是最后一个 STABLE 快照。**stable 通道必须 pin 具体 tag**，否则同一 channel 在不同镜像回退路径下拿到不同字节。
  - `wanxiang`：GitHub `RIME-LMDG` 的 `LTS` 与 CNB `rime-wanxiang` 的 `model` 提供**逐字节一致**的 `wanxiang-lts-zh-hans.gram`（均 420248620 B），仅 release tag 名不同。`LTS` 是移动 tag：重下载内容与记账 sha256 不符时告警并接受（模型更新），不硬失败。
- **下载源 `--mirror` 预设**（默认 `cn`）：`auto`（官方优先→回退 CN→Proxy）；`cn`（CN 优先→Proxy→官方）；`ghproxy`（Proxy 优先→CN→官方）；`upstream`（仅官方）。`--mirror <URL>` 自定义镜像兜底；每次候选失败自动降级到下一个（排序逻辑见 `catalog.Candidates`）。`cn` 是大陆无代理用户的正确默认：GitHub 上游对 420MB 大文件会限速/超时，镜像与代理稳定。
- 解析后固化具体 release/tag + sha256 入清单。`update` 对比上游（重解析 stable tag、清缓存），`status` 能回答当前版本。
- **首次下载的可信基准**：记账 sha256 只能和「自己上次记的值」比，第一次无可比。因此 `catalog.Asset` 带 `MinBytes`（雾凇 zip 1MiB / 万象 gram 50MiB，均远低于真实体积 16MB / 420MB）+ zip 魔数 `PK\x03\x04`：以 200 返回的错误页 / 门户页过不了这道门，永不会被当成「已校验」永久缓存。
- **sha256 记账 + 校验**：immutable tag（雾凇日期 tag）重下载与记账不符 → **硬失败**（镜像内容异常，换下一候选）；缓存命中但与记账不符 → 告警重下载。移动 tag（wanxiang LTS/nightly）不符 → 仅告警（内容更新）。
- 缓存 `~/.cache/ompinyin/`，键含 immutable tag（`rime-ice-full-stable.zip@2026.06.30`），stable/nightly 不互撞；命中时复验记账 sha256。

L2 解压规则：

- **zip 安全**：拒绝 `..` / 绝对路径（zip slip）；解压前扫描清单，受管同名 `*.custom.yaml` **跳过 + 警告**。
- **永不覆盖**（无论 install/update）：`user.yaml`、`installation.yaml`、`*.userdb`、`custom_phrase.txt`。
- **plain install 不覆盖已存在文件**（已收敛宿主重跑零落盘）；`update`（overwrite 模式）刷新非保护文件。数据是否就位由 default.yaml **与** rime_ice.schema.yaml 双锚点探测（上游 zip 若加顶层目录会发现，而非误报就位）。
- `full.zip` 利用关键事实：纯数据、不含 custom.yaml → L2/L3 可独立更新。

### 5.4 受管 vs 不受管（手感项）

**受管手感项（默认集，随收敛生效）：**

写入 `default.custom.yaml`（`ascii_composer` 与 `switcher` 都定义在 `default.yaml`，这里打得到）：

- `menu/page_size: 9`（候选词个数，覆盖上游默认 5）
- `,` `.` 翻页（`key_binder/bindings/+`：comma→`Page_Up`, period→`Page_Down`）
- 中英切换（引擎内）：`ascii_composer/switch_key/Shift_R` = `commit_code`（「先把已打编码上屏，再切 `ascii_mode`」）；`Control_L/R` = `noop`（防 Ctrl 尾随误切）；`good_old_caps_lock: true`（CapsLock 只切大写）
- **`ascii_composer/switch_key/Shift_L` = `noop`**（4.5 起）：左 Shift 归 fcitx5 的 `AltTriggerKeys`（开/关输入法）。
  rime 出厂值是 `Shift_L: commit_code`，两边都处理同一个键时，可见结果取决于「输入法是开还是关」和「当前 IM 是 rime 还是键盘」——
  同一个键有时进 rime、有时进英文键盘、有时要按两下（真机 2026-11-05）。写 `noop` 让左 Shift 只有一个主人：
  **左 Shift = 开/关输入法，右 Shift = rime 内中英，`Super+I` = 换 IM**，三条轴互不重叠（`dev-notes.md` P10）。
- `switcher/hotkeys: [F4]`（方案选单；`Control+grave` 家族被移除，理由见 §2.3）
- `switcher/save_options: [ascii_punct, traditionalization, emoji, search_single_char]`（**不含 `full_shape`**）

写入**每个方案**的 `<schema>.custom.yaml`（`switches` 定义在 schema 里，`default.custom.yaml` 打不到）：

- `switches/@4/reset: 0`（`full_shape` = 全半角，永远从**半角**起步）

> 这两条必须**成对**：rime 规定「要记忆的开关不能设 `reset`」。根因是一次真实事故——锁屏前是「全角」，解锁时密码里的字符被按全角输入（`！` 而不是 `!`），PAM 校验失败进不去系统；而 `full_shape` 连字母数字都会变全角，所以「密码别放符号」并不构成兜底。

**仍不受管（用户所有）：**

- `ascii_punct`（中英标点）/ `traditionalization` / `emoji` / `search_single_char` 的**默认值**（只受管「是否跨会话记忆」，见上）
- `page_size` 以外的菜单项、`,.` 以外的 `key_binder` 绑定
- `Shift` / `Control` / `Caps` 之外的修饰键

> 注：`switches/@4` 是**下标**选择器（rime patch 只能按下标选列表项）。`verify` 因此会读 `build/<schema>.schema.yaml` 断言该下标仍是 `full_shape`，上游重排就报「手感项（全半角）」未达标，而不是静默打到别的开关上。
> 注：`switches/@4/reset` 只在 `Model=true`（生成 `<schema>.custom.yaml`）时落地；`--no-model` 下没有 grammar 补丁、也就没有这个文件 —— 但 `save_options` 已经把 `full_shape` 排除在记忆之外，所以仍从半角起步。
> 注：`,.` 翻页写在 `default.custom.yaml` 对自带 `key_binder` 的方案**不一定生效**；若某方案失效需在方案层 custom 补。

推荐分工（三条轴，互不重叠）：中英 = 右 `Shift`（引擎内，受管手感项）；左 `Shift` = **开/关输入法**（Omarchy 的 `AltTriggerKeys`，只翻 `active`，不改组内 IM）；IM = `Super+I` 循环（Omarchy 的 `omarchy-input-method cycle`）；方案 = `F4`。四者各占一个键，左 Shift 与 rime 不共享（`Shift_L: noop`），否则同一个键在不同状态下做不同的事。

左 Shift 的「打开」落点由 fcitx5 决定（`Instance::inputMethod()`）：激活时用该输入上下文 `localIM_`（有就用，`Super+I` / `SetCurrentIM` 写的），否则用组 `DefaultIM`（= `rime`）；未激活时报告数组第一项（也是 `rime`）。所以「英文键盘 → 左 Shift 关 → 左 Shift 开」可能落回 rime，且顶栏指示器不反映「已关」——不是收敛漂移（`dev-notes.md` P10 附三）。

> **fcitx5 的硬约束（2026-11-03 以源码 + 真机确认）**：所有 IM 热键 —— `TriggerKeys`、`AltTriggerKeys`、`ActivateKeys`、`DeactivateKeys` —— 都先判 `canTrigger()`，而它是 `inputMethodList().size() > 1`。所以「组里只有 `rime`」时 IM 热键是**完全的 no-op**，不存在「单 IM 下开/停用」。默认组因此是两项 `[rime keyboard-us]`（Omarchy 的 `Super+I` 真的能切英文键盘）；`--single-im` 只为脚本兼容保留，4.5+ 上会打警告（Omarchy 的 Setup > Typing 会报错、顶栏指示器不显示）。

---

## 6. 宿主集成协议（L4）

### 6.0 时序：整个 L4 只停一次 fcitx5

禁止 profile / hotkey / drop-in 各自 `stop→写→start`（三次竞态）。唯一合法顺序：

1. `systemctl --user stop omarchy-fcitx5.service`（优先该单元；没有再停通用 fcitx5）
2. `rime_deployer --build`（属部署缝，挂在这次 stop 里）
3. 写 profile；旧路径另写 fcitx5 `config` 与专用 drop-in（4.5+ 只**删**旧触发键残留，绝不写 `[Hotkey/AltTriggerKeys]`）
4. `systemctl --user daemon-reload`
5. `systemctl --user start omarchy-fcitx5.service`
6. 顶栏指示器（4.5+：`omarchy bar put omarchy.keyboard-layout` + 删旧 drop-in/pin；旧路径：写 pin + `omarchy restart shell`，**外壳保活**，禁止 stop）

restart **不触碰**主单元文件里的 ExecStart。失败不自动回滚，不写 `state.json`；下次 converge 接着修。备份已在 stop 前做好。

### 6.1 profile 注册（竞态 + 格式双坑）

在 §6.0 的 stop 窗口里改 `~/.config/fcitx5/profile`。严格格式（`Name=rime`、`Layout=` 无空格、GroupOrder 末尾），宽容读取/严格写出。

**组的条目列表本身是终态**（`Desired.IMs`，`profile.ConvergeGroup`）：

- 增删条目并**按给定顺序**重排（顺序 = `Ctrl+Space` 的枚举顺序），`packItems()` 保证 `Groups/0/Items/N` 从 0 连续、`GroupOrder` 居末、`DefaultIM=rime`（§16.18/32）。
- **必须能删**：v1.3.0 及更早的 `DefaultProfile` 建过 `keyboard-us`，而 `EnsureRime` 只加不删 → 老机器永远带第二个 IM，与新装机器不一致（这正是 v1.3.1 注释里 "so no two hosts agreed" 的问题）。删/重排都有备份，`--single-im` 就是显式要求删到只剩 `rime`。
- **默认两项的理由**：fcitx5 的 `canTrigger()` 要求条目数 > 1（见 §5.4 引文），单 IM 时 `Ctrl+Space` 完全不动作；两项才让「切英文键盘」这个承诺成立。

### 6.2 热键：归 Omarchy（4.5+）

Omarchy 4.5+ 把 IM 热键收归上游，本仓库**不再写它们**：

| 谁拥有 | 证据（本机安装路径） |
|---|---|
| 触发键 | `default/config/fcitx5/config`：`TriggerKeys=Zenkaku_Hankaku/Hangul`（**没有 `Ctrl+space`**）、`AltTriggerKeys=0=Shift_L`、`ActiveByDefault=False` |
| 循环键 | `bin/omarchy-input-method cycle` + `default/hypr/bindings/input-method.lua`（`SUPER+I` / `SUPER+SHIFT+I`；终端里 `Ctrl+Space` 是 non-consuming 直通） |
| 反悔机制 | `configure.py::without_ctrl_space()` 每次 `--defaults` 都删 `Control+space`；迁移 `migrations/1791676907.sh` 把空的 `[Hotkey/AltTriggerKeys]` 段填回 `0=Shift_L` |

结论（D1）：写 `Control+space`、清空 `AltTriggerKeys` 都会被上游反复撤销 → **不写**。4.5+ 收敛只做一次
**幂等清理**：删掉旧版 ompinyin 写进 `[Hotkey/TriggerKeys]` 的 `Control+space` / `Alt+space`
（`hotkey.RemoveTriggerKey` + `hotkey.LegacyTriggerKeys`），读→写一次、备份一次。

- **段留着、键删掉**：fcitx5 的 INI 里「段存在但无键」= 无触发键；缺段 = 回落出厂默认。所以删键后段保留。
- **绝不碰 `[Hotkey/AltTriggerKeys]`**：那是 Omarchy 的 `Shift_L`（左 Shift 进出输入法），删它会让左 Shift 落回 rime，
  与上游承诺的行为不一致。
- 计划/doctor 文案：`触发键归 Omarchy：Super+I 循环、左 Shift 进出输入法；Ctrl+Space 留给终端`；
  有残留时 report 出 `删除 [Hotkey/TriggerKeys] 中的 Control+space`。doctor **不再输出 `AltTriggerKeys` 行**——
  4.5+ 上它恒真（上游每次都会写回 `Shift_L`），恒真的检查等于谎话。

**旧路径（Omarchy 4.0，即 4.5 之前唯一在用的版本；`observe.OmarchyManagesIME == false`）一字不改**：那些机器上热键仍归本工具——
`hotkey.EnsureTrigger` 无条件写 `Ctrl+Space`（herdr 出厂 prefix 也是 `ctrl+space`，冲突只告警）、
`hotkey.EnsureAltTriggerEmpty` 清空 `[Hotkey/AltTriggerKeys]`（fcitx5 出厂 `Shift_L` 与触发键走同一个 `toggle()`，
见 `dev-notes.md` P9）、doctor 保留 `AltTriggerKeys` 行。分支依据是**特性探测**（见 §6.4），不是版本号。

### 6.3 服务操作

优先发现 `omarchy-fcitx5.service`，缺失回退通用 fcitx5。启停只走 §6.0 那一次，这里不再 stop。

**start 前先清游离实例。** `org.fcitx.Fcitx5` 是唯一总线名，任何非单元 fcitx5 占着它时，单元自己 spawn 的实例会**启动即退出 0**（"another fcitx already running"），`Restart=always` 把它拖成 start-limit-hit 死循环；而 `Type=simple` 的 `systemctl start` 只要进程被 spawn 就返回 0 —— 调用方看到「启动成功」，主机却没有任何可用输入法（案例见 `dev-notes.md` P5）。所以 `startUnit` 在**单元未激活**时先 `pkill -x fcitx5`，并**等它真的让出总线名**（`pkill` 只保证信号送出）再 start；单元已激活时绝不清——`Start` 同时是幂等的关窗调用，那样会把健康实例一起杀掉。

这个游离实例**极易被自己造出来**：服务停止时任何 `fcitx5-remote` / 总线调用都会 D-Bus 激活一个新的 fcitx5。因此存活探测只用 `pgrep` 进程探针（`FcitxRunning` / `FcitxCount`），**绝不用 `fcitx5-remote` 或总线调用探测存活**（`theme` 的 `ReloadAddonConfig` 是唯一的例外，且它只在已知 fcitx5 在跑时才发）；`verify` 也只在单元已激活时才调 `RemoteState`。

### 6.4 顶栏输入法指示器（必做终态，4.5+ 由 Omarchy 渲染）

Omarchy 4.5+ 自己渲染输入法指示器，并且**故意关掉** fcitx5 的 SNI 托盘项：

| 证据 | 内容 |
|---|---|
| `default/systemd/user/omarchy-fcitx5.service` | `ExecStart=/usr/bin/fcitx5 --replace --disable notificationitem`，注释写明 "notificationitem duplicates the tray entry omarchy already renders itself" |
| `shell/plugins/bar/widgets/KeyboardLayout.qml` + `shell/Commons/InputMethodState.qml` + `default/input-methods/indicator.py` | 指示器本体，读 fcitx5 的 IM 状态 |
| `config/omarchy/shell.json` | 默认右区含 `omarchy.keyboard-layout` |

所以终态从「造一个图标」变成「**确认图标在栏上 + 清掉旧产物**」：

- `internal/bar`：`IndicatorPresent(shellJSON)`（纯函数，扫 `bar.layout.{left,center,right}` 找
  `id == "omarchy.keyboard-layout"`）+ `EnsureIndicator()`（`omarchy bar put omarchy.keyboard-layout`，幂等；
  外壳没在跑直接返回 nil——默认配置本来就带这个 widget）。
- `tray.RemovePinned`（读 → 去 `Fcitx` → 原子写，带与旧 `SetPinned` 相同的「写入后重读比对、最多 3 次」竞态保护）：
  清掉旧版 ompinyin 的 SNI pin；`tray.RemoveDropIn` 清 drop-in。两者都是「否则顶栏会出现两个图标」的那一半。
- **drop-in 的删除必须在 stop 窗口内**：文件删了、跑着的 fcitx5 并不会忘记它（旧 ExecStart 已经生效，只有重启才重新读），
  所以 4.5+ 的 `NeedHost` 也含 `c.DropInExists`，删除发生在 `daemon-reload` 之前、`start` 之前。放在窗口外删
  = 「两个图标留到下次登录」，而 `NeedHost` 不含它的话下次运行连窗口都不会开。
- plan 谓词：`NeedTray = !BarIndicator || StaleTray`，步骤 `顶栏输入法指示器（omarchy.keyboard-layout）+ 清理旧 drop-in/pin`
  （JSON key 仍叫 `tray`）。`StaleTray` = drop-in 存在 **或** pinned 含 `Fcitx` **或** 旧 theme hook 存在。
  `PinnedHasFc` / `ThemeHookOK` 不需要重启，只进 `NeedTray` / `NeedTheme`，不进 `NeedHost`。
- L5/doctor：一行 `顶栏输入法指示器`。OK 三分支：widget 在 → OK；不在且**外壳在跑** → 失败（收敛本该放回去），
  Detail 给出精确修复 `omarchy bar put omarchy.keyboard-layout`；不在且**外壳没跑** → OK + 说明
  「外壳未运行，无法确认（Omarchy 默认配置已含该 widget）」——外壳没跑时 `bar put` 是 no-op，红在这里是
  一次运行也不可能清掉的假红。有残留时上述三种 Detail 都点名。`HostReport` 只增字段 `barIndicator` /
  `staleTrayArtifacts`；`dropInOK` / `pinnedHasFcitx` 保留不改名，但 4.5+ 上语义是「残留」（不是终态）。
- **边界**：`omarchy bar put` 需要外壳在跑；不在跑时只能靠 Omarchy 默认配置，doctor 会说「无法确认」。
  `◀` 抽屉的渲染仍由 Omarchy 决定，不在这里 patch。

**旧路径（4.0）一字不改**：`tray.WriteDropIn`（内容由原 unit 的 `ExecStart` 派生，剥掉
`--disable notificationitem`）+ `tray.SetPinned`（读 pinned → 并入 `Fcitx` → 原子写数组，`omarchy bar set` 会把数组
强转成字符串，`Tray.qml` 只认数组）+ `tray.RestartShell`（新增 pin 时才重启外壳刷新 SNI）。
分支依据：`observe.OmarchyManagesIME`（`/usr/share/omarchy/default/input-methods/indicator.py` 是否存在）——
**版本号不可用**：4.5.0 包里的 `/usr/share/omarchy/version` 仍写着 `4.0.0.alpha`。

### 6.5 环境变量红线与活目录

一行环境变量不写（Omarchy 自带 `10-omarchy-fcitx.conf`；Wayland 下 `GTK_IM_MODULE` 明确不设）。doctor 校验该 conf 存在，并确认用户 `environment.d` 没有把 `GTK_IM_MODULE` 设回来。

只写 `~/.local/share/fcitx5/rime`。`~/.config/fcitx/rime` 是历史兼容副本，本工具不写；`clean --legacy` 可删。

### 6.6 候选框跟随 Omarchy 主题（默认终态）

候选框（fcitx5 classicui 候选词窗口）**默认**跟随当前 Omarchy 主题配色，换主题即自动刷新、无需重启 fcitx5。**所有权分工**——颜色映射逻辑只有一份（在钩子脚本里，Go 侧不重复实现）：

**4.5 起配色交还上游**：`default/themed/fcitx5-theme.conf.tpl` → `omarchy-theme-set-templates` →
`~/.local/state/omarchy/current/theme/fcitx5-theme.conf` → `bin/omarchy-theme-set-fcitx5`（`omarchy theme set`
会调用，写 `~/.local/share/fcitx5/themes/omarchy/` 并设 classicui `Theme=omarchy`）。本仓库原来的 theme-set 钩子
与它**并发写同一批文件**（现网已观察到混装，`dev-notes.md` P10），所以钩子退役。

| 谁拥有 | 内容 | 说明 |
|---|---|---|
| ompinyin（受管、记账） | `~/.config/fcitx5/conf/classicui.conf` | 只受管 6 个键：`Theme=omarchy` / `DarkTheme` / `UseDarkTheme=False` / `UseAccentColor=False` / `Font` / `MenuFont`；其余键原样保留（§5.1 键级拥有） |
| Omarchy | `~/.local/share/fcitx5/themes/omarchy/` | `omarchy-theme-set-fcitx5` 生成 theme.conf + 资源；**不入账、不删**。ompinyin 只删自己曾经生成过的 `background.svg` / `highlight.svg` |
| （退役） | `~/.config/omarchy/hooks/theme-set.d/fcitx5-theme` | 旧版 ompinyin 装的钩子：收敛与 uninstall 都会删（`theme.RetireArtifacts`），并从账本里退役 `theme.HookRelPath` |

- **颜色映射**（验证过，见 fcitx5 theme.cpp `populateColor`）：面板=`background`+`lighter_background` 边框；选中候选=`accent` 圆角高亮，文字按亮度**自动取对比色**（亮 accent→深字，暗 accent→浅字；`lum()` = ITU-R BT.601）；其余候选/序号/注释=`foreground`/`dark_foreground`；预编辑高亮=`selection`。SVG 9-slice（Radius=边框 margin），librsvg 矢量缩放不糊。
- **热重载**：`fcitx5-remote -r` 只重载全局配置、**不会**重读 `classicui.conf`/主题（真机验证过：重载后仍旧主题）。必须走 DBus `org.fcitx.Fcitx.Controller1.ReloadAddonConfig classicui`（fcitx5-configtool 同款）；钩子与收敛后的 `theme.Reload` 都用它，fcitx5 未跑时静默。
- **收敛时序**：在 stop 窗口**之外**（L4 顶栏之后、L5 之前）——这些文件 reload 即生效，无需重启。顺序是
  「先退役旧钩子/SVG → 写 classicui 键 → `omarchy-theme-set-fcitx5 --activate` → DBus 热重载」，
  这样上游生成器可以覆盖我们删掉的同名文件。
- **计划谓词**：`NeedTheme = !ThemeEqual || ThemeHookOK`（`ThemeEqual` = classicui 字节一致；钩子存在即算工作）。
  生成目录**不参与判定**：4.5 上它归 Omarchy，不是我们能收敛的东西。
- **受管文件的字体**：`Font`/`MenuFont` 取自 `omarchy-font-current`（Pango 会经 fontconfig 回退 CJK 字体）；取不到用 `Sans`。
- **边界**：`omarchy-theme-set-fcitx5` 不存在（4.0）时 `theme.ApplyUpstream` 返回可忽略的
  `theme.ErrNoUpstream`：classicui 指向一个不存在的主题名，fcitx5 自然回落默认外观（4.5 前的机器失去候选框跟随主题，
  属交给上游的代价）。UI 字体换肤（Tray/inline preedit 等）不属于本终态。

### 6.7 可选 X11 HiDPI 兼容模式（ClassicUI 候选框）

**问题**：Wayland 下 classicui 是**双 UI**（同一进程同时建 `x11::0` 与 wayland 两套），X11/XWayland 应用（微信，走 fcitx4 老协议）命中 X11 候选框；XWayland 的 RandR 恒上报 96 DPI，`PerScreenDPI` 在 XWayland 被源码强制忽略，`ForceWaylandDPI` / `GDK_SCALE` / `QT_SCALE_FACTOR` 都够不到这套 → 2x 屏上候选框字小。唯一杠杆是根窗口 `RESOURCE_MANAGER` 的 `Xft.dpi`（`XCBUI::scaledDPI` 读它；XCBUI 只在启动时解析一次，改了要重启 fcitx5）。

**它是全局 X11 缩放权，不是候选框私有开关**：其它 X11/Electron 应用若已由合成器或自身缩放，读取后可能二次放大。因此**默认只诊断**：`status`/`doctor` 永远读回并展示 X11 事实（scale、期望/实际 `Xft.dpi`、受管块与两个单元的残留），不写文件、不装 `xorg-xrdb`；用户确认旧 X11 应用候选框过小时，才以 `install --x11-hidpi` 明确 opt-in（持久化进 `Desired.X11HiDPI`）。混合 DPI 多屏没有正确的自动解：XWayland 和 Xft 都是单个全局值，焦点屏动态切换会影响所有 X11 应用并打断输入，故不做自动随焦点切换。

**取值**：`dpi = round(96 × scale)`（X 资源只收整数）。96 是 1x 基准的三重约定——CSS 参考像素、X11 历史缺省 DPI、Pango/fontconfig 缺省分辨率（面板真实 DPI 在逻辑缩放体系里用不上）；映射 1.0→96 / 1.25→120 / 1.5→144 / 1.6→154 / 1.75→168 / 2.0→192。`scale` 只读、逐级降级：① `hyprctl monitors -j` 聚焦屏实时值（能处理 `monitors.lua=auto`）；② `monitors.lua` 的 `omarchy_monitor_scale`；③ 沿用现有 `Xft.dpi`（首次无源按 96，不放大）。

**落地**：`~/.Xresources` 仅行级拥有 marker 块中的 `Xft.dpi`，保留其余用户内容；不写 Hyprland 配置或 `classicui.conf`。发布单元 `ompinyin-x11-hidpi.service` 以**安装时解析的绝对二进制路径**（`os.Executable()`）为 `ExecStart`，不经 `/usr/bin/env`（systemd user 的 PATH 不含 `~/.local/bin`）。`DISPLAY` / `HYPRLAND_INSTANCE_SIGNATURE` 由 `x11-hidpi-apply` **在运行时向 `systemctl --user show-environment` 解析**（≤10s 轮询，见下），**既不继承、也不硬编码 `:0`**（Hyprland 的 XWayland 编号不保证）。`ompinyin-x11-hidpi.path` 监听 `~/.config/hypr/monitors.lua`，缩放变化即重算重推（仅值变化时才重启 fcitx5，避免无谓打断输入）。

**`graphical-session.target` 不保证会话已就绪。** systemd 在单元启动时**冻结环境快照**，而 uwsm/Omarchy 往 manager 导入 `DISPLAY` 发生在之后，所以单元里 `DISPLAY` 是空的，`xrdb` 会报 `Can't open display ''`；`.path` 只监听 `monitors.lua`，这一跳失败就等于整个会话不发布。等待修不了冻结的快照，只能**向 manager 要**：`hidpi.ImportSessionEnv` 带 ≤10s 轮询，补全时打印实际补到的键；同样的解析让 `hyprctl` 拿到 `HYPRLAND_INSTANCE_SIGNATURE`，否则 `force_zero_scaling` 前置会退化成"按默认 true 猜"（读不到时另行告警）。发布决策在 L1 之后重新探测 X 会话，不复用 L1 前的 `X11Available` 快照；opt-in 意图在收敛开始时就落盘（双向），本轮即使 L5 失败也不会被下一次裸 `install` 反向解释。案例见 `dev-notes.md` P1。opt-in 意图在收敛开始时就落盘（双向）：本轮即使 L5 失败，下一次裸 `install` 也不会把用户刚选的模式误判成相反意图（opt-in 被拆 / opt-out 被重开）。

撤销用 `install --no-x11-hidpi`：先 `disable --now` path 单元（防监视器变化再发布）→ 删两个 user 单元 → 移除受管块（空则删 `~/.Xresources`）；当前会话已发布的值保留到注销（`xrdb -load` 会清空用户整库，破坏性过大，不做运行时还原）。`ompinyin x11-hidpi-apply`（内部命令，不进 CLI 契约）是登录发布/缩放监听的私有入口：取与 `install` 相同的状态锁（避免第二个 stop 窗口），未 opt-in 时 no-op，防残留 enable 链接继续发布全局资源；重启 fcitx5 失败会把退出码降为失败（同 §6.0 的 stop 窗口守卫）。

**`force_zero_scaling` 是前置。** Hyprland 的 `xwayland:force_zero_scaling` 决定 X11 窗口是否由合成器缩放：Omarchy 默认 `true`（`/usr/share/omarchy/default/hypr/envs.lua`）= **不由合成器缩放**，每个 toolkit 各自缩，X11 候选框才需要 `Xft.dpi`；若为 `false`（Hyprland 默认）= 合成器已把 X11 整体放大（像素化但尺寸对），此时发布 `Xft.dpi` 会让候选框与所有 X11 应用一起 4×，故本模式**自动失效**（no-op + 说明，不装包、不落文件、不发布）。fcitx5 侧没有更小的替代杠杆：`PerScreenDPI` 在 XWayland 上被源码强制忽略、`classicui.conf` 只有 Wayland 专用的 `ForceWaylandDPI`，而 `Font` 只放大文字——`XCBWindow::setScale(dpi/96)` 通过 `cairo_surface_set_device_scale` 缩放**整个候选窗口**（背景/边距/高亮/文字一起），所以 Font 不能替代它。

**规则：一个框架只能有一个缩放权，禁止叠加。** `Xft.dpi` 的已知读者：fcitx5 XCBUI（目标）、Qt（`QXcbScreen::logicalDpi()` 直接返回 Xft.dpi；`QT_AUTO_SCREEN_SCALE_FACTOR=1` 时缩 2×，但任何显式 `QT_SCALE_FACTOR` 会再乘一次）、GTK（DPI 设置与 `GDK_SCALE` 可能叠加）、纯 Xft 应用（xterm 等，`Xft.dpi` 正是它们唯一可用的缩放）。**ompinyin 只诊断不代改**：发布时若会话里已有 `GDK_SCALE` / `GDK_DPI_SCALE` / `QT_SCALE_FACTOR`（Omarchy 自带的 `monitors.lua` 默认就写 `GDK_SCALE=2`），打 `[警告]` 说明叠加后果（GTK3 约 4×），由用户决定移除哪一边。启用本模式后必须清掉各 toolkit 的显式缩放因子；否则就是微信那种 4×。微信的 per-app 消解用 `QT_SCREEN_SCALE_FACTORS=2`：显式 screen factor **覆盖** DPI 派生因子（源码 `screenSubfactor()` 注释：factors from `QT_SCREEN_SCALE_FACTORS` takes precedence over the factor computed from platform plugin DPI），而不是叠加。

**doctor 检查项（默认也展示，只读）**：根窗口值是否 `== round(96 × scale)`（未 opt-in 也报 scale / 期望 / 实际与残留产物）；受管块与两个单元是否存在且内容正确；`~/.Xresources` 用户内容是否被破坏（只告警，不收敛）。

**所有权与冲突（§5 的延伸）：** `~/.Xresources` 是**行级拥有**——ompinyin 只写自己的 marker 块，**绝不删除块外的用户/他人 `Xft.dpi`**。`xrdb -merge` 按文件顺序应用（后者胜），所以若块外 `Xft.dpi` 排在块后，它会赢过 ompinyin → `实际≠期望` → 重发布仍被覆盖 → **L5 明确报错（exit 1）**，不是静默错值。`observe` 把「块外 Xft.dpi」记为 `x11ForeignDpi`，`status`/`doctor`/`--json` 都提示顺序风险。运行中有人直接改根窗口属性：**无实时守护**（path 单元只听 `monitors.lua`），在下次 `install`/`doctor`/登录/缩放变化时纠正。作用域：`Xft.dpi` 是**每个 X 会话**（X display 根窗口属性），不同 OS 用户各自会话/XWayland 互不影响，`~/.Xresources` 也各自 home。

**已知局限**：无 XWayland 的纯 Wayland 会话探测不到 `RESOURCE_MANAGER`，仍收敛文件与单元、但发布跳过并注记；`hyprctl` 缺失时按上面的优先级降级；`--no-x11-hidpi` 后已发布值保留到注销；marker 用 `#` 时，有真 cpp 的发行版会让 `xrdb` 在 stderr 报 `invalid preprocessing directive`（xrdb 不检查其退出码，功能正常；Arch 无 `/usr/lib/cpp`，xrdb 直接按 cpp 行指令跳过）。

---

## 7. CLI

```text
ompinyin install [--dsp ID|none] [--dsp-default] [--no-quanpin]
              [--model | -s|--no-model] [--channel stable|nightly]
              [--x11-hidpi | --no-x11-hidpi] [-y|--yes] [--dry-run [--json]]
              [--mirror auto|cn|ghproxy|upstream|URL] [-b|--full-backup]
              [--os-override omarchy]         # 测试后门：容器/VM 内绕过 ID=omarchy 预检

ompinyin update                            # L2 资产刷新到最新并重编译（--self 一并自升级；--dry-run [--json] 可预览）
ompinyin switch --dsp ID [--dsp-default]   # 改/加双拼（重写 schema_list + 反查跟随 Primary；--mirror/-b/--dry-run [--json] 可用）
ompinyin switch --dsp none                 # 去掉双拼，回到仅全拼
ompinyin switch --full                     # 全拼改回 schema_list[0]（已装的双拼留在 Extra）
ompinyin status                            # 现状 vs 终态 diff（含布局 / 资产版本 / 顶栏指示器）
ompinyin doctor                            # 服务健康 / IM 三态 / 环境变量红线 / 热键 / 顶栏指示器 / 候选框主题 / X11 HiDPI / 遗留目录
ompinyin clean [--legacy]                  # 清缓存 / 老路径 ~/.config/fcitx/rime 的重复模型
ompinyin uninstall                         # 受管文件删除 + profile 移除 rime + 顶栏还原（去 Fcitx pin）+ 退役旧主题产物（系统包不动，数据目录留手动）
ompinyin source [--preset cn|upstream]     # 独立：配置 /etc/pacman.d/mirrorlist（sudo；见下）
ompinyin version                           # 构建注入的版本号（--version / -v 同义）
ompinyin status|doctor --json              # 机器可读输出（脚本 / CI 用）
```

**`--mirror` 的三种形态**：预设名（`auto|cn|ghproxy|upstream`）· 镜像 URL · **本地目录**（路径或 `file://`）。本地目录是离线入口：按资产名在目录里找 `rime-ice-full-stable.zip` / `rime-ice-full-nightly.zip` / `wanxiang-lts-zh-hans.gram`，拷入缓存后同样过形状护栏（尺寸/魔数）——本地文件也不能绕过校验。**`--mirror` 只作用于 L2 资产，不作用于 L1 pacman 包。**

**`source` 命令（pacman 仓库镜像助手，独立于收敛）**：manage `/etc/pacman.d/mirrorlist`。`--preset cn`（默认）把 `core`/`extra`/`multilib` 指向国内 stock-Arch 镜像（阿里 / 清华）并在末尾保留 `stable-mirror.omarchy.org` 回退；`--preset upstream` 还原为仅官方 Omarchy 镜像。写入前覆盖备份，`--dry-run` 只打印目标不落盘，`--yes` 全自动。红线：仅用 `pacman -Sy` / `-S --needed`，别跑 `pacman -Syu`（会漂移 stable 锁版本）；`[omarchy]` 仓库无国内镜像。这是 root 级配置，收敛流程（install/update/switch）**不触碰**。

`--dsp ID` 取值见 §2.1。`--dsp-default` 必须伴随 `--dsp`（`switch --full` 除外）。`--no-quanpin` 仅在 `--dsp` 时合法。

**终态继承（§3「flags 覆盖 state.json」的确切语义）**：`install` 以 state.json 里的上次 `Desired` 为**基线**，**只有命令行上显式出现的选项**才覆盖它（`flag.FlagSet.Visit` 判定）。所以裸跑 `ompinyin install` 不会静默掉你之前选的 `--dsp`、不会复位 `--channel`、也不会把 `--no-model` 改回来。去掉双拼：`ompinyin install --dsp none` 或 `ompinyin switch --dsp none`；重新启用模型：`ompinyin install --model`。`switch` 同样接受 `--mirror` / `-b`（下载策略默认 `cn`，见 §5.3）。

**`update --self`**：替换二进制——备份旧的、按 `checksums.txt` 校验 sha256、原子替换；装在 `~/.local/bin` 免权，装在 `/usr/local/bin` 等需 sudo 路径会尝试 sudo（无 tty 用 `sudo -n`）。

无 `--tray-pin` / `--no-tray`。顶栏输入法指示器是必做终态（4.5+ 由 Omarchy 渲染，本工具保证它在栏上并清理旧产物）。

`--single-im` 保留解析（脚本兼容），4.5+ 运行期打警告：组内没有 `keyboard-*` 时 Omarchy 的
Setup > Typing > Input Methods 会报错、顶栏指示器不显示。

步骤化输出 `[计划]/[完成]/[跳过]/[失败]`；失败给修复提示（`fcitx5-diagnose`、build 产物检查等）。

退出码：`0` 成功 / `1` 执行失败 / `2` 用法错误 / `3` 预检失败（非 Omarchy、磁盘不足、octagram 缺失、**以 root 运行**、**必需工具不在 PATH**、非法 Layout、无法确定家目录）。

**预检门槛（§1.1 的真实现）**：`facts.Collect` 检查 ① 非 root（只有 pacman 通过 sudo 提权）；② `ID=omarchy`——`--os-override` 的**任意非空值**即绕过并打警告（容器/VM 测试后门，不是「填入另一个发行版名」）；③ 磁盘 ≥2GB；④ octagram；⑤ `fcitx5-remote` / `rime_deployer` / `omarchy` 在 PATH——`rime_deployer` 跑在 stop 窗口里，发现得太晚就是「已停 fcitx5 且无法部署」；提供方包未装 → 仅告警（L1 会装）。

---

## 8. 可靠性与状态

- **状态清单** `~/.local/state/ompinyin/state.json`：布局 ID、schema_list、资产版本/sha256、受管文件哈希、时间戳。
- **账本先于终态落盘**：每层写完字节就 `SaveLedger()`（只持久化 `ManagedFiles` / `Assets` 这些「磁盘事实」），`Desired` / `SchemaList` 只在收敛成功后由 `Save()` 推进。否则一次中途失败会让下次把工具自己写的文件判成「外来」并索要覆盖确认。
- **锁**：`~/.local/state/ompinyin/lock`，防并行实例互踩（尤其 stop/start 窗口期）。`Home()`/`Dir()` 不判失败即终止进程（会跳过 `defer lock.Release()`）；家目录在 CLI 入口用 `CheckHome()` 一次性校验（退出码 3）。
- **原子写**：所有配置 temp 文件 + rename，中断不留半截文件。
- **备份**：改 L3/L4 前快照 `backup-<ts>/`（默认仅受管文件 + drop-in + shell.json 片段，`-b` 整目录）；**只有存在写作的层时才建**（已收敛的机器重跑零副作用）；同秒碰撞自动加 `-N` 后缀；收敛成功后只保留最近 5 个，且总量超 `LedgerBackupBudget`（4 GiB）时从最旧的开始继续删（最新的永远保留；`-b` 单份可达 ~1GB）。`uninstall` 删除前同样先快照（含 `state.json` —— 它记录用户选过的 `Desired`，如 `--dsp`，重建无法恢复）。回滚 = 拷回 + 重编译重启；`switch` 双向可逆。
- **磁盘预检** ≥2GB（模型 420MB × 数据 + 缓存副本）。
- **安全**：不以 root 跑除 pacman 外的步骤；zip slip 拒绝；下载先过**形状护栏**（`MinBytes` + zip 魔数，拦住 200 返回的错误页/门户页），再按记账 sha256 校验（immutable tag 不符硬失败、移动 tag 告警）；`Content-Length` 与实际字节数不符即截断失败；坏字节不进缓存、不落半截 `.gram`（落位后还会复算一次校验）。
- **断点续传**：420MB 模型 `Range` + `.part` 临时文件，校验通过再 rename。**续传只认同源**：`.part` 旁写一个 `.part.src` 记录来源 URL，换镜像/换 tag 时先删 `.part`（跨源拼接会静默产出损坏模型），`.part` 超 24h 也不再续；`ompinyin clean` 连 `.part` 一起清。

---

## 9. 测试策略

| 层 | 内容 |
|---|---|
| 单测 | `catalog` 映射表；patches golden（全拼/各双拼 × 有/无模型）；profile roundtrip 四场景（缺失/无 rime/DefaultIM 错/正常）；hotkey keysym 白名单 + `RemoveTriggerKey`（删键保空段）；tray drop-in 文本 + shell.json pinned 合并/移除（已有其它 pin 不丢）；bar `IndicatorPresent`（有/无 widget/坏 JSON）；catalog `RemovedIMs`；assets 缓存命中/镜像回退/校验和不匹配/zip slip（httptest） |
| 收敛逻辑 | 临时 HOME + fake systemctl/rime_deployer + stub `assets.ResolveStableTag` 断言终态达成（CI 可跑、绝不触网） |
| 真机验收 | 见 §15 T0–T4 金字塔 |
| CI | gofmt / go vet / **golangci-lint**（`.golangci.yaml`：默认集 errcheck+govet+ineffassign+staticcheck+unused，加 gofmt formatter） / `go test -race` / build 冒烟 + `ompinyin version`；绝不在 CI 跑系统操作、绝不触网（tag 解析可注入 stub，本地资产目录可离线跑） |

- **T0 的结构性盲区**：fixture 把 `$TMPDIR`、systemd、D-Bus、信号时序全部归一化，所以"真实主机交互"类缺陷（跨文件系统 `rename`、游离 fcitx5、`Restart=always` 抖动、SIGINT 落在 stop 窗口）在 T0 里**不可能复现**，这不是覆盖率能补的洞。两条出路都要用：① §15 的 `make smoke`；② 在那类 seam 上**主动造出异常环境**（先例：`t.Setenv("TMPDIR", <另一挂载点>)` 复现 EXDEV，见 `TestApplyAcrossFilesystems`）。

---

## 10. 工程化与发布

- 许可证 **MIT**；README 中文优先，写明：Wayland 下 `GTK_IM_MODULE` 是反模式；F4 切方案 / Super+I 与左 Shift 归 Omarchy；顶栏指示器由 Omarchy 提供、本工具保证它在栏上；`--mirror` 只管 L2、L1 镜像靠 `ompinyin source`；终端候选窗不可见属 Hyprland 渲染器问题（域外）。
- goreleaser 出 amd64/arm64 静态二进制 + sha256 → GitHub Releases；可选 Omarchy 插件薄壳。

---

## 11. 交付范围与后续

当前交付范围 = 五层收敛 + 全部 CLI + 候选框主题 + 可选 X11 HiDPI + 状态清单。未做/待决项（Omarchy 插件薄壳、`uninstall --purge-packages`）记在 `dev-notes.md` 的「待决」。

---

## 12. 风险与已知局限

1. **上游接口脆弱性**：依赖 `full.zip` 不含 custom.yaml。缓解 = zip 安全扫描（§5.3）。
2. **420MB 模型下载体验**：GitHub 上游直连超时，故 `cn` 是大陆无代理用户的默认；镜像链 / 断点续传 / 超时细节见 §5.3。
3. 终端候选窗不可见为 Hyprland 渲染器 bug，工具域外。
4. **`omarchy.tray.pinned` / `shell.json`（仅 4.0）**：写数组是整键覆盖，必须读→合并→写回，否则吞 pin。外壳没在跑时写 pin 失败。`omarchy update` 若重排 layout，再跑一次 `ompinyin` 收敛重贴。4.5+ 只做删除（`tray.RemovePinned`），不再 pin。
5. **4.5+ 顶栏指示器依赖外壳**：`omarchy bar put` 需要外壳在跑；不在跑时只能靠 Omarchy 默认配置，`doctor` 会说明。
6. **`indicator.py` 被手动删除会误判为旧版本**：探测降级到旧路径（不会破坏配置，但会重新装 drop-in/pin）。
5. 待决策：uninstall 是否加 `--purge-packages`（当前倾向不加）。
6. **顶栏 IM 图标在 foot 内不刷新**（仅显示问题）：Omarchy < 4.0.2 的 quickshell 靠 SNI `NewIcon` 重读图标，而 IM 切换只发 `dbusmenu.LayoutUpdated`；**4.0.2 已解除**。兜底 `omarchy restart shell` 强制重枚举 SNI。
7. **X11 HiDPI 是全局缩放权**（§6.7）：`Xft.dpi` 会让同时设了显式缩放因子的 X11/Electron 应用二次放大（实测微信 4×），且多屏混合 DPI 无单值解；故默认只诊断，`force_zero_scaling=false` 时自动 no-op。

---

## 13. 关键决策（结论）

核心取舍已分别在 §1–§8 展开，这里只列**非显而易见**的结论：

- **默认仅全拼**：双拼是 `--dsp`；v1 只做雾凇，万象只作 LMDG **模型**（§1.2 / §1.3 / §2.2）。
- **顶栏输入法指示器必做**：4.5+ 由 Omarchy 渲染（`omarchy.keyboard-layout`），本工具只 `omarchy bar put` 确认 + 清掉旧 drop-in/pin，无退出选项（§6.4）。
- **能力探测不认版本号**：`observe.OmarchyManagesIME` 看 `default/input-methods/indicator.py` 是否存在（4.5.0 包的 `version` 仍写 `4.0.0.alpha`）；旧路径在 4.0 上原样保留（§6.2/§6.4/§6.6）。
- **热键/配色交还上游**：不写 `[Hotkey/TriggerKeys]`、不碰 `[Hotkey/AltTriggerKeys]`、不生成 fcitx5 主题；只删旧版残留并退役旧钩子（§6.2/§6.6，`dev-notes.md` P10）。
- **L1 镜像与自升级解耦**：收敛不接管 `/etc/pacman.d/mirrorlist`（单独 `source`）；`update --self` 自升级与数据收敛解耦（§7）。

---

## 14. 术语表

| 词 | 说明 |
|---|---|
| IM / 输入法层 | fcitx5 的输入法实例（`keyboard-us` ↔ `rime`），用触发键切中英 |
| schema / 方案层 | Rime 输入方案（`rime_ice` / `double_pinyin` / …），用 **F4** 切换 |
| `Desired` | 声明式期望终态（Primary / Extra / Model / Channel / IMs） |
| 收敛 | 采集现状 → diff 终态 → 只执行差异 → 复核 |
| LMDG | Language Model Data Group；万象整句模型（`.gram`） |
| SNI | StatusNotifierItem：fcitx5 顶栏托盘项的通信协议 |
| `notificationitem` | fcitx5 的 SNI 托盘插件；Omarchy 4.5+ 默认禁用（它自己渲染 `omarchy.keyboard-layout`） |
| Algebra | Rime 的拼音反查 / 英文派生规则（`radical_pinyin` / `melt_eng`） |
| 所有权协议 | 以哈希记账区分「工具生成 / 用户改过 / 外来」三类文件的处理策略 |

---

## 15. 测试环境矩阵（T0–T4 金字塔）

| 层 | 环境 | 覆盖 | 成本 |
|---|---|---|---|
| T0 Stub 单测 | 临时 HOME + fake systemctl/rime_deployer/pacman + stub `assets.ResolveStableTag` | 五层 converge、golden、profile roundtrip、tray 合并、幂等零扰动（T0 语句覆盖率 ~65%），CI 用 | 秒级 |
| T1 Distrobox Arch 容器 | `distrobox create --image archlinux:latest` | L1–L3 全真实；**需 `--os-override`** | 分钟级 |
| T2 systemd-nspawn（按需） | pacstrap rootfs + `systemd-nspawn -bD` | L4 服务层：stop→写→start；drop-in 真生效 | 中 |
| T3 QEMU/KVM 全新 Omarchy | 官方 ISO + qcow2 backing-file 基线快照 | 端到端：Wayland/Hyprland、IM 三态、herdr、F4、**顶栏图标可见** | 小时级 |
| T4 真机幂等回归（零成本） | 已安装宿主 | ① 重跑全 `[跳过]`；② 游离实例自愈（`install` 清掉占着 `org.fcitx.Fcitx5` 的非单元实例并真正接管）；③ `update --self` **跨文件系统**替换 + `releases/latest` sha256；④ `--dsp zrm` 后两个 build schema 都有 grammar；⑤ 顶栏有图标；⑥ 备份可回滚；⑦ SIGINT 落在 stop 窗口后 fcitx5 仍被拉起 | 零 |

- **T4 不是一段文档，是 `make smoke`**（`scripts/t4-smoke.sh`，~100s）：跑 ① ② ③ + `doctor`；④⑤⑥⑦ 仍手动（把信号时序脚本化会变成 flaky 测试，比没有更坏）。它**必须跑在真机**并会短暂重启 fcitx5，所以**打 tag 前跑**。存在理由就是 §9 的 T0 结构性盲区。

- T3：`qemu-img create -b <clean>.qcow2` 基线快照，每轮副本，装坏即弃；T4 前置为 git checkpoint / 备份 `$RIME` 与 `shell.json`。
- **T1/T2（非 Omarchy 容器）**：预检需 `--os-override`（绕过 `ID=arch`）**且**在 PATH 放一个 `omarchy` 替身——`omarchy` 不是 pacman 包，缺了预检直接失败（§7）；`fcitx5-remote`/`rime_deployer` 由 L1 安装（预检只告警）。
- 执行顺序：T0 → T1 → T4 幂等 → T3（验收）→ T2 按需。

---

## 16. 正确性不变量（实现时对照）

1. 不写任何 IM 环境变量。
2. 不写 `~/.config/fcitx/rime`。
3. 不合并 YAML；受管文件整文件生成。
4. 不静默覆盖用户改过的受管文件。
5. grammar 打进 schema_list 里每一个启用方案，参数与官方常量一致。
6. radical / melt algebra 跟随 Primary。
7. 改 profile / user.yaml / （旧路径的）专用 drop-in：`stop → 写 → start`。改 tray pinned 也只走「读→并/去→写数组」，不手改 `shell.json` 其它字段；4.5+ 只做「去 `Fcitx`」。
8. 写触发键（仅 4.0）只写白名单 keysym（`Shift_L` 不是 `Shift`）；4.5+ 只删 `hotkey.LegacyTriggerKeys`，删键保留空段。
9. `fcitx5-remote -s` 只使用 IM 名（`rime` / `keyboard-us`），从不使用 schema id。
10. （4.0）专用 drop-in 去掉 `--disable notificationitem`，且 `omarchy.tray.pinned` 数组含 `Fcitx`；写数组必须读→合并→写回，不得丢用户已有 pin。4.5+ 的终态相反：这两样都必须**不存在**，终态是 `omarchy.keyboard-layout` 在 bar 上。
11. 除 `pacman` 外不以 root 跑；`source` 改 mirrorlist 也只经 sudo。
12. zip 条目不得含 `..` 或绝对路径。
13. 计划与执行共用同一组谓词：已收敛的重跑不重解、不重写字节一致的受管文件、不开 stop 窗口、不建备份目录。
14. stop 窗口必须由 `defer` 关闭：任何提前返回（daemon-reload / drop-in / hotkey 失败）都要把 fcitx5 重新 start，start 失败则把返回值降级为退出码 1 —— 绝不把用户留在无输入法状态。
15. 账本先于终态：每层落盘后就 `SaveLedger()`（只存 `ManagedFiles`/`Assets` 事实），`Desired`/`SchemaList` 仅在收敛成功时由 `Save()` 推进。中途失败不得丢失所有权记账。
16. `install` 以 state.json 的 `Desired` 为基线，只覆盖命令行上**显式出现**的选项；不得静默重置用户选过的 `--dsp` / `--no-model` / `--channel`。
17. `.part` 只能同源续传（`.part.src` 记录来源 URL，换源即删）；下载必须过形状护栏（`MinBytes` + 魔数 + `Content-Length` 比对）后才能进缓存。
18. 写 fcitx5 `profile` 时 `GroupOrder` 永远居末，`Groups/0/Items/N` 永远从 0 连续编号（不留空洞）。
19. （4.0）专用 drop-in 的路径**跟随发现的 unit**（`fcitx5.service` 就写 `fcitx5.service.d/`），且 L5 校验的是**内容**（ExecStart 不再 `--disable notificationitem`），不是文件存在。4.5+ 删这份 drop-in 并删空掉的 `.d/`，但绝不删同目录下用户的其它 drop-in。
20. 孤儿受管文件 = 账本 − 期望集合，每次收敛都清（不只 `Model=false`）；只删账本记过的文件，别人写的永不删。
21. 备份失败即拒绝该次覆盖/删除（§5.1「先备份后写」是硬承诺，错误不得丢弃）；覆盖、删除与 `uninstall` 都适用。
22. 预检必须拦 root 与缺失的 `rime_deployer`/`fcitx5-remote`/`omarchy`；`--os-override` 任意非空值即绕过 ID 检查并告警。
23. SIGINT/SIGTERM 走 context 取消（第二次信号硬退出 130），使 stop 窗口的 defer 仍能收尾；同一 context 经 `execcmd` 绑定到所有 exec seam，子进程（rime_deployer/systemctl/pacman/sudo/DBus）一并被终止，不留孤儿进程与 stop 窗口竞争。
24. 候选框主题：颜色映射的唯一来源是 **Omarchy 的 `omarchy-theme-set-fcitx5`**（Go 侧不重复实现）；ompinyin 只拥有 `classicui.conf` 的 6 个键（入账、按 §5.1 覆盖），退役自己的旧钩子与两个 SVG（`theme.RetireArtifacts`），**不删** `themes/omarchy/` 里 Omarchy 生成的任何文件；热重载只用 `ReloadAddonConfig`（`fcitx5-remote -r` 不重读 addon 配置）。
25. X11 HiDPI（§6.7）默认只诊断：`--x11-hidpi` 未显式给出时，收敛绝不写 `~/.Xresources`、不装 `xorg-xrdb`、不装两个 user 单元；`Desired.X11HiDPI` 由且仅由命令行显式 flag 置位。
26. 已 opt-in 的 X11 HiDPI 是**行级拥有**（marker 块内仅 `Xft.dpi`）；`monitors.lua` 与 Hyprland 配置只读不写；`classicui.conf` 永不写。撤销（`--no-x11-hidpi`）先禁 path 单元再删文件；`ApplyX11HiDPI` 在未 opt-in 时 no-op。
27. X11 HiDPI（§6.7）以 Hyprland `xwayland:force_zero_scaling` 为前置：为 `false`（合成器已缩放 X11）时本模式恒为 no-op——不写 `~/.Xresources`、不装 `xorg-xrdb`、不发布 `Xft.dpi`，plan/doctor 说明原因。`Xft.dpi` 是全局 X11 缩放权，禁止与显式 toolkit 因子叠加。
28. `~/.Xresources` 的 `Xft.dpi` 是**行级拥有且不与他人竞争**：ompinyin 绝不删除块外的 `Xft.dpi`；块外存在时把 `x11ForeignDpi` 暴露给 plan/doctor——`xrdb` 后者胜，冲突以 L5 报错收场，不静默覆盖。运行中第三方改根窗口属性无实时守护，下次收敛纠正。
29. 启动服务前先清掉占着 `org.fcitx.Fcitx5` 的游离实例，且**仅当单元未激活时**清（`Start` 是幂等关窗调用，不得误杀健康实例）；清完必须确认它已让出总线名再 start。存活性探测只走进程探针（`pgrep`），**绝不用 `fcitx5-remote`/总线调用**——服务停止时那会 D-Bus 激活一个新的游离实例，正是本故障的成因。
30. L5 的「服务在跑」判定不得只看 `systemctl --user is-active` 的快照：`Restart=always` 下濒死实例会让它读到几百毫秒的 `active`。单元自称激活却存在**多于一个** fcitx5 进程（`FcitxCount > 1`）即与游离实例抖动共存，L5 必须失败——`install` 绝不能报「终态收敛完成」而 `doctor` 立刻报两项不达标。
31. 自升级替换文件必须**落进目标目录再 rename**（copy → 同目录 rename），**绝不**从暂存目录直接 `rename(2)` 到二进制：暂存用 `$TMPDIR`（Omarchy 上是 tmpfs），二进制在 `$HOME`/`/usr/local/bin`（btrfs/root fs），跨挂载 rename 返回 `EXDEV`。替换后必须仍是 `0755`（`CopyFile` 写 0644），失败不得在二进制旁留 `.*.new`。
32. fcitx5 输入法组**始终**是终态（`Desired.IMs`）：收敛必须增删 + 按声明顺序重排条目（`profile.ConvergeGroup`），并保持 `DefaultIM=rime`、`GroupOrder` 居末、条目 `0..n-1` 连续——**热键、图标、主题交还上游，组形状没有**。理由：所有 IM 热键都以 `canTrigger()`（条目数 > 1）为前提，条目集合一改，`Super+I` / 左 Shift 落到哪个 IM 就变。被移出的引擎（例如安装器自己选的 `pinyin`）**引擎与包保留**，plan/doctor 必须点名并给出 `omarchy setup input <id>` 的加回方式（D2）。
33. （4.0）`[Hotkey/AltTriggerKeys]` 必须是**存在且为空**的段：fcitx5 出厂 `Shift_L` 会与 rime 的 `Shift_L: commit_code` 抢同一个键（>1 IM 时走同一个 `toggle()`）。4.5+ **反过来**：该段归 Omarchy（`0=Shift_L`，左 Shift 进出输入法），ompinyin 绝不创建、清空或断言它；同时绝不写 `[Hotkey/TriggerKeys]`，只删旧版残留的 `Control+space`/`Alt+space`。
