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
| 顶栏图标 | **显示且固定（必做）** | 去掉 `--disable notificationitem` + pin `Fcitx`。无 `--no-tray` |
| 宿主 | fcitx5-rime + Omarchy 用户服务 | 一行环境变量都不写 |

### 1.3 非目标

- 不装**万象拼音方案**；万象只作为 LMDG **模型**。
- 不装五笔 / 墨奇音形 / 九键 / 云拼音。
- 不写 `GTK_IM_MODULE` / `~/.xprofile` / 任何 IM 环境变量。
- 不管 ibus、fcitx4、非 Omarchy 发行版。
- 不把 Shift 中英切换、`Control/Caps` 等纳入受管终态（§5.4）；`page_size` 与 `,.` 翻页**受管**。
- v1 无 TUI。

---

## 2. 终态空间：Layout × Model

工具内的「期望终态」是一个结构体，不是硬编码的五个文件名。`install` / `update` / `switch` / `status` / `doctor` 都对同一份 `Desired` 做 diff。

```text
Desired
├── Primary    布局 ID              默认 quanpin（schema_list[0]）
├── Extra      []布局 ID            默认空；--dsp 时追加
├── Model      bool                 默认 true（万象 LMDG）
└── Channel    stable | nightly     默认 stable（--channel 选项，见 §5.3）
```

顶栏图标不是 Desired 的可选项，是 L4 不变量：每次收敛都必须达到 §6.4。

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
| `--dsp-default` 且无 `--dsp` | 非法 | — | 预检失败 |

约束：

- v1 **最多一个双拼**。`--dsp` 可重复视为错误。
- `radical_pinyin` / `melt_eng` 的 algebra **跟随 Primary**。F4 切到 Extra 方案时反查/英文派生不匹配——与上游行为一致，写入 README。
- `schema_list` 条目**必须**写成 `/schema_list` 子项的 `- schema: <id>` map 格式。短格式 `- <id>` 会被 `rime_deployer --build` 忽略（只写 `build/default.yaml` 骨架、不编译任何 schema），导致 fcitx5-rime 无 schema 可载入、全局打不出中文。

### 2.3 两层切换，不要混搞

| 层 | 名字 | 谁来切 | 本工具怎么验证 |
|---|---|---|---|
| fcitx5 IM | `keyboard-us` ↔ `rime` | 触发键 `Alt+Space` | `fcitx5-remote` 三态 1↔2 |
| Rime schema | `rime_ice` / `double_pinyin` / … | **F4**（或 Ctrl+`) 方案选单 | 读 `user.yaml` / 人工按 F4；**禁止**用 `fcitx5-remote -s <schema>` |

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
       L4 写 profile + hotkey + drop-in（drop-in 由原 unit ExecStart 派生）
       daemon-reload
  start fcitx5                       # defer 守门：任何提前返回都会 start，start 失败 → 退出码 1
  SaveLedger
L4 tray pin（数组直接写入 shell.json + 重启外壳刷新 SNI）
L5 verify（只读，build 缺失为硬失败）
if L5 未达终态: SaveLedger（只存事实）; return 1     # 不推进 Desired
write state.json（含 Desired）+ 备份轮转（留最近 5 个）
unlock
```

**幂等零扰动**（收敛后重跑）：L2 谓词不成立 → 整层不跑 → L3 字节一致全跳过 → stop 窗口不开 → 不建备份目录 → L5 复核全绿。`plan` 除了输出步骤文本，还输出 `NeedL1/L2/L3/Deploy/Host/Tray` 谓词，`Install` 消费同一组谓词，杜绝「计划 [跳过]、执行重做」的脱节。

落地形态：每层一个 `converge()`，不引入通用 reconciler 框架。**`rime_deployer --build` 不是 L5**：L5 只读；build 是 L3 与 L4 start 之间的缝。

| 层 | 内容 | 变更方式 |
|---|---|---|
| L1 系统包 | fcitx5-rime fcitx5-configtool fcitx5-gtk（最小集：fcitx5/fcitx5-qt/librime/opencc 由依赖闭包带入；octagram= librime 的 librime-octagram.so 插件，非独立包） | `pacman -S --needed --noconfirm`（`--yes` 时） |
| L2 数据资产 | 雾凇 `full.zip` + 万象 `.gram`，带版本号和 sha256 | 下载 → 校验 → 解压/落位 |
| L3 配置覆盖 | 受管 `*.custom.yaml`（集合随 Layout 变） | 整文件生成（所有权协议见 §5） |
| （缝）部署 | `rime_deployer --build $RIME /usr/share/rime-data $RIME/build` | **fcitx5 必须已停** |
| L4 宿主集成 | profile、触发键、drop-in、start、**顶栏图标** | 一个 stop 窗口（见 §6.0） |
| L5 验证 | 编译产物、模型编入、IM 三态、托盘可见、人工 checklist | **只读** |

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
internal/hotkey    [Hotkey] 写入 + Shift_L 白名单校验
internal/tray      L4 顶栏：专用 drop-in + shell.json pinned 读/并/写数组 + 重启外壳
internal/deploy    rime_deployer --build $RIME /usr/share/rime-data $RIME/build
internal/service   systemctl --user 封装（omarchy-fcitx5 优先）
internal/state     ~/.local/state/ompinyin/state.json 状态清单
internal/observe   采集当前宿主快照（Current）用于 diff
internal/verify    L5 只读复核 + doctor checklist
internal/plan      diff(desired, current) → 有序步骤列表
internal/source    独立 pacman 仓库镜像助手：写 /etc/pacman.d/mirrorlist（sudo）
internal/selfup    update --self：自升级二进制（备份 + checksums.txt 校验 + 原子替换）
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
  - **解析时机**：API 只在「没有可用 pin」且「本运行确实要跑 L2」时查一次；清单里已记有具体 stable tag 时 plain install 直接沿用（不重复查 API）。否则大陆用户（api.github.com 不可达）会每次收敛都重试。重新 pin 的唯一入口是 `ompinyin update`。
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

`default.custom.yaml` 里的 `key_binder` / `ascii_composer` **不一定打进方案**——`double_pinyin.schema.yaml` 用自己的 `key_binder`，要生效必须写方案层 custom。裸 Shift 切中英在 Rime/fcitx5 没有可靠开箱方案。

**v1 受管（写入 `default.custom.yaml`，随收敛生效）：**
- `menu/page_size: 9`（候选词个数，覆盖上游默认 5）
- `,` `.` 翻页（`key_binder/bindings/+`：comma→`Page_Up`, period→`Page_Down`）

**仍不受管（用户可自己做非受管补丁）：**
- `Shift_L: inline_ascii` / `commit_code`（Shift 中英切换）
- `Control_L/R` / `Caps_Lock` / `good_old_caps_lock`
- `ascii_punct` / `full_shape`

> 注：`,.` 翻页写在 `default.custom.yaml` 对自带 `key_binder` 的方案**不一定生效**；若某方案失效需在方案层 custom 补。

推荐分工（写入 README，不当作收敛目标）：`Alt+Space` 切 IM；`Shift` 用上游默认。

---

## 6. 宿主集成协议（L4）

### 6.0 时序：整个 L4 只停一次 fcitx5

禁止 profile / hotkey / drop-in 各自 `stop→写→start`（三次竞态）。唯一合法顺序：

1. `systemctl --user stop omarchy-fcitx5.service`（优先该单元；没有再停通用 fcitx5）
2. `rime_deployer --build`（属部署缝，挂在这次 stop 里）
3. 写 profile、fcitx5 `config`、专用 drop-in
4. `systemctl --user daemon-reload`
5. `systemctl --user start omarchy-fcitx5.service`
6. tray pin（直接写数组到 `shell.json`）+ `omarchy restart shell`（**外壳保活**，禁止 stop）

restart **不触碰**主单元文件里的 ExecStart。失败不自动回滚，不写 `state.json`；下次 converge 接着修。备份已在 stop 前做好。

### 6.1 profile 注册（竞态 + 格式双坑）

在 §6.0 的 stop 窗口里改 `~/.config/fcitx5/profile`。严格格式（`Name=rime`、`Layout=` 无空格、GroupOrder 末尾），宽容读取/严格写出；已有 rime 只确保 `DefaultIM=rime` 最小改动。

### 6.2 触发键避让

Omarchy **原厂** herdr prefix 就是 Ctrl+Space，所以 **无条件** 写入触发键，不要「检测到 herdr 才写」（配置文件缺了反而漏改）。

写 `~/.config/fcitx5/config` 的 `[Hotkey/TriggerKeys]`：受管键 `Alt+space` 写入优先位，**用户自加的触发键保留在后面（合并、不吞并）**。**INI 合并不整文件覆盖**（保留用户其它热键段）。键名白名单校验——裸 `Shift` 会被 fcitx5 **静默丢弃**。已含全部受管键则跳过。

### 6.3 服务操作

优先发现 `omarchy-fcitx5.service`，缺失回退通用 fcitx5。启停只走 §6.0 那一次，这里不再 stop。

### 6.4 顶栏输入法图标（必做终态）

Omarchy 默认 `ExecStart=/usr/bin/fcitx5 --disable notificationitem`（注释：避免与 Omarchy 自绘托盘重复），因此**原厂顶栏没有输入法图标**。**取舍**：重开 = 主动接受与 Omarchy 自绘托盘的重复。之所以仍做，是因为顶栏 `omarchy.keyboard-layout` 只显示 XKB 布局（如 `us`），不反映 `rime`/英文这种 IM 级状态——要拿到「当前是中还是英」的可视信号，重开 SNI 是 v1 唯一现实手段（此取舍写入 README）。本工具把「顶栏不点 `◀` 就能看到输入法图标」做成 **L4 必做终态**：每次 `install` / `update` 都收敛到这一步，无退出选项。分两步，缺一不可：

| 步 | 作用 | 不做的后果 |
|---|---|---|
| A. 启用 notificationitem | fcitx5 发出 SNI 托盘项 | 顶栏完全没有输入法图标 |
| B. pin `Fcitx` | 图标常驻顶栏，不进抽屉 | 图标在 `◀` 抽屉里，不点展开看不见 |

**A. 启用 notificationitem**（systemd user drop-in，**专用文件名**）：

```ini
# ~/.config/systemd/user/omarchy-fcitx5.service.d/ompinyin-notificationitem.conf
[Service]
ExecStart=
ExecStart=/usr/bin/fcitx5
```

不用 `override.conf`，也不用 `systemctl revert`：`revert` 会清掉该单元 **整个** `.d/`（用户自己的其它 drop-in 一起没）。`daemon-reload` 后走 stop→start。仅 `uninstall`：删这一份 conf + daemon-reload + stop→start。

**ExecStart 派生而非硬编码**：drop-in 内容由原 unit 的 `ExecStart` 行剥离 `--disable notificationitem` 后派生（`tray.DeriveDropInContent`），二进制路径与其它 flag 原样保留。unit 不可读时回退静态内容 `/usr/bin/fcitx5`。

**B. 固定到顶栏（读→并→写数组进 shell.json，不手改其它内容）**：

fcitx5 SNI 的 `Id` = **`Fcitx`**（`Tray.qml` 用 `pinnedIds.indexOf(item.id)` **精准匹配**，大小写敏感，**非总线号**）。

收敛协议是**读 pinned → 并入 `Fcitx` → 把数组直接原子写回 `shell.json`**（`tray.SetPinned`）**，禁止裸覆盖**（会吞掉用户已有 pin，且 `omarchy bar set` 会把数组强转成字符串，Tray.qml 只认数组 → 图标 pin 不上）：

```text
# 读 ~/.config/omarchy/shell.json → bar.layout.{left,center,right} 中
# id == "omarchy.tray" 的 pinned → 并入 "Fcitx"（已在则不动）→ 原子写回数组
# 例：原 pinned 为 ["Foo"] 时，写成 ["Foo","Fcitx"]
```

**为什么直接写数组**：Omarchy 4.0.1 上**同时** `omarchy bar set ... --json` 和 `omarchy shell shell setBarWidget` 都会把数组值强转成字符串；`Tray.qml` 的 `pinnedIds` 是 `settings.pinned instanceof Array ? ... : []`，字符串会落到 `[]` → **图标不 pin**。写数组是唯一真正 pin 上的方式。

写 pin 后运行 `tray.RestartShell`（`omarchy restart shell`）：fcitx5 的 notificationitem 是本收敛才启用的，运行中的外壳不会自行刷新其 SNI 显示（会一直显示 "Keyboard - English (US)"），需重启外壳才重枚举并显示 rime。**仅当本次新增了 pin 时才重启**（已 pin 则跳过，避免无谓 churn）。

仅 `uninstall`：读 pinned → 去掉 `"Fcitx"`（空数组也要写回）→ 写回数组；**不主动 RestartShell**——外壳如在运行，FileView 监控会热加载并隐藏该托盘项；未运行则提示启动后自动应用。

**边界**：

- 外壳没在跑时无法写 pin（下拉失败）。T1 容器走 `--os-override` 时本层会失败，属预期，T3/T4 才验图标。
- `◀` 展开按钮只要有任何托盘项就渲染（空抽屉也显）。去掉空 `◀` 需 patch `Tray.qml`（系统文件、`omarchy update` 覆盖，**域外**）。
- doctor：专用 drop-in 存在、`notificationitem` addon 已加载、`omarchy.tray.pinned` 数组含 `Fcitx`。缺任一项 = 未达终态。
- **foot 内 IM 图标不刷新**：早期版本（Omarchy < 4.0.2）限，仅显示问题（打字/切换/候选均正常）；**Omarchy 4.0.2 已解除**（foot 内 `Alt+Space` 切中英，顶栏图标自动更新）。

### 6.5 环境变量红线与活目录

一行环境变量不写（Omarchy 自带 `10-omarchy-fcitx.conf`；Wayland 下 `GTK_IM_MODULE` 明确不设）。doctor 校验该 conf 存在，并确认用户 `environment.d` 没有把 `GTK_IM_MODULE` 设回来。

只写 `~/.local/share/fcitx5/rime`。`~/.config/fcitx/rime` 是历史兼容副本，本工具不写；`clean --legacy` 可删。

---

## 7. CLI

```text
ompinyin install [--dsp ID|none] [--dsp-default] [--no-quanpin]
              [--model | -s|--no-model] [--channel stable|nightly] [-y|--yes] [--dry-run]
              [--mirror auto|cn|ghproxy|upstream|URL] [-b|--full-backup]
              [--os-override omarchy]         # 测试后门：容器/VM 内绕过 ID=omarchy 预检

ompinyin update                            # L2 资产刷新到最新并重编译（--self 一并自升级）
ompinyin switch --dsp ID [--dsp-default]   # 改/加双拼（重写 schema_list + 反查跟随 Primary）
ompinyin switch --dsp none                 # 去掉双拼，回到仅全拼
ompinyin switch --full                     # 全拼改回 schema_list[0]（已装的双拼留在 Extra）
ompinyin status                            # 现状 vs 终态 diff（含布局 / 资产版本 / 托盘）
ompinyin doctor                            # 服务健康 / IM 三态 / 环境变量红线 / 触发键 / 顶栏图标 / 遗留目录
ompinyin clean [--legacy]                  # 清缓存 / 老路径 ~/.config/fcitx/rime 的重复模型
ompinyin uninstall                         # 受管文件删除 + profile 移除 rime + 托盘还原（系统包不动，数据目录留手动）
ompinyin source [--preset cn|upstream]     # 独立：配置 /etc/pacman.d/mirrorlist（sudo；见下）
ompinyin version                           # 构建注入的版本号（--version / -v 同义）
ompinyin status|doctor --json              # 机器可读输出（脚本 / CI 用）
```

**`--mirror` 的三种形态**：预设名（`auto|cn|ghproxy|upstream`）· 镜像 URL · **本地目录**（路径或 `file://`）。本地目录是离线入口：按资产名在目录里找 `rime-ice-full-stable.zip` / `rime-ice-full-nightly.zip` / `wanxiang-lts-zh-hans.gram`，拷入缓存后同样过形状护栏（尺寸/魔数）——本地文件也不能绕过校验。**`--mirror` 只作用于 L2 资产，不作用于 L1 pacman 包。**

**`source` 命令（pacman 仓库镜像助手，独立于收敛）**：manage `/etc/pacman.d/mirrorlist`。`--preset cn`（默认）把 `core`/`extra`/`multilib` 指向国内 stock-Arch 镜像（阿里 / 清华）并在末尾保留 `stable-mirror.omarchy.org` 回退；`--preset upstream` 还原为仅官方 Omarchy 镜像。写入前覆盖备份，`--dry-run` 只打印目标不落盘，`--yes` 全自动。红线：仅用 `pacman -Sy` / `-S --needed`，别跑 `pacman -Syu`（会漂移 stable 锁版本）；`[omarchy]` 仓库无国内镜像。这是 root 级配置，收敛流程（install/update/switch）**不触碰**。

`--dsp ID` 取值见 §2.1。`--dsp-default` 必须伴随 `--dsp`（`switch --full` 除外）。`--no-quanpin` 仅在 `--dsp` 时合法。

**终态继承（§3「flags 覆盖 state.json」的确切语义）**：`install` 以 state.json 里的上次 `Desired` 为**基线**，**只有命令行上显式出现的选项**才覆盖它（`flag.FlagSet.Visit` 判定）。所以裸跑 `ompinyin install` 不会静默掉你之前选的 `--dsp`、不会复位 `--channel`、也不会把 `--no-model` 改回来。去掉双拼：`ompinyin install --dsp none` 或 `ompinyin switch --dsp none`；重新启用模型：`ompinyin install --model`。`switch` 同样接受 `--mirror` / `-b`（下载策略默认 `cn`，见 §5.3）。

**`update --self`**：替换二进制——备份旧的、按 `checksums.txt` 校验 sha256、原子替换；装在 `~/.local/bin` 免权，装在 `/usr/local/bin` 等需 sudo 路径会尝试 sudo（无 tty 用 `sudo -n`）。

无 `--tray-pin` / `--no-tray`。顶栏图标是必做终态，装上就有。

步骤化输出 `[计划]/[完成]/[跳过]/[失败]`；失败给修复提示（`fcitx5-diagnose`、build 产物检查等）。

退出码：`0` 成功 / `1` 执行失败 / `2` 用法错误 / `3` 预检失败（非 Omarchy、磁盘不足、octagram 缺失、**以 root 运行**、**必需工具不在 PATH**、非法 Layout、无法确定家目录）。

**预检门槛（§1.1 的真实现）**：`facts.Collect` 检查 ① 非 root（只有 pacman 通过 sudo 提权）；② `ID=omarchy`——`--os-override` 的**任意非空值**即绕过并打警告（容器/VM 测试后门，不是「填入另一个发行版名」）；③ 磁盘 ≥2GB；④ octagram；⑤ `fcitx5-remote` / `rime_deployer` / `omarchy` 在 PATH——`rime_deployer` 跑在 stop 窗口里，发现得太晚就是「已停 fcitx5 且无法部署」；提供方包未装 → 仅告警（L1 会装）。

---

## 8. 可靠性与状态

- **状态清单** `~/.local/state/ompinyin/state.json`：布局 ID、schema_list、资产版本/sha256、受管文件哈希、时间戳。
- **账本先于终态落盘**：每层写完字节就 `SaveLedger()`（只持久化 `ManagedFiles` / `Assets` 这些「磁盘事实」），`Desired` / `SchemaList` 只在收敛成功后由 `Save()` 推进。否则一次中途失败会让下次把工具自己写的文件判成「外来」并索要覆盖确认。
- **锁**：`~/.local/state/ompinyin/lock`，防并行实例互踩（尤其 stop/start 窗口期）。`Home()`/`Dir()` 不判失败即终止进程（会跳过 `defer lock.Release()`）；家目录在 CLI 入口用 `CheckHome()` 一次性校验（退出码 3）。
- **原子写**：所有配置 temp 文件 + rename，中断不留半截文件。
- **备份**：改 L3/L4 前快照 `backup-<ts>/`（默认仅受管文件 + drop-in + shell.json 片段，`-b` 整目录）；**只有存在写作的层时才建**（已收敛的机器重跑零副作用）；同秒碰撞自动加 `-N` 后缀；收敛成功后只保留最近 5 个。回滚 = 拷回 + 重编译重启；`switch` 双向可逆。
- **磁盘预检** ≥2GB（模型 420MB × 数据 + 缓存副本）。
- **安全**：不以 root 跑除 pacman 外的步骤；zip slip 拒绝；下载先过**形状护栏**（`MinBytes` + zip 魔数，拦住 200 返回的错误页/门户页），再按记账 sha256 校验（immutable tag 不符硬失败、移动 tag 告警）；`Content-Length` 与实际字节数不符即截断失败；坏字节不进缓存、不落半截 `.gram`（落位后还会复算一次校验）。
- **断点续传**：420MB 模型 `Range` + `.part` 临时文件，校验通过再 rename。**续传只认同源**：`.part` 旁写一个 `.part.src` 记录来源 URL，换镜像/换 tag 时先删 `.part`（跨源拼接会静默产出损坏模型），`.part` 超 24h 也不再续；`ompinyin clean` 连 `.part` 一起清。

---

## 9. 测试策略

| 层 | 内容 |
|---|---|
| 单测 | `catalog` 映射表；patches golden（全拼/各双拼 × 有/无模型）；profile roundtrip 四场景（缺失/无 rime/DefaultIM 错/正常）；hotkey Shift_L 校验；tray drop-in 文本 + shell.json pinned 合并（已有其它 pin 不丢）；assets 缓存命中/镜像回退/校验和不匹配/zip slip（httptest） |
| 收敛逻辑 | 临时 HOME + fake systemctl/rime_deployer + stub `assets.ResolveStableTag` 断言终态达成（CI 可跑、绝不触网） |
| 真机验收 | 见 §15 T0–T4 金字塔 |
| CI | gofmt / go vet / **golangci-lint**（`.golangci.yaml`：默认集 errcheck+govet+ineffassign+staticcheck+unused，加 gofmt formatter） / `go test -race` / build 冒烟 + `ompinyin version`；绝不在 CI 跑系统操作、绝不触网（tag 解析可注入 stub，本地资产目录可离线跑） |

---

## 10. 工程化与发布

- 许可证 **MIT**；README 中文优先，写明：Wayland 下 `GTK_IM_MODULE` 是反模式；F4 切方案 / 触发键切中英；顶栏图标是必做终态；`--mirror` 只管 L2、L1 镜像靠 `ompinyin source`；终端候选窗不可见属 Hyprland 渲染器问题（域外）。
- goreleaser 出 amd64/arm64 静态二进制 + sha256 → GitHub Releases；可选 Omarchy 插件薄壳。

---

## 11. 交付范围与后续

v1.0 覆盖：五层收敛 + 全部 CLI（含 `source`/`--self`）+ 状态清单 + 锁/原子写 + 断点续传 + golden 单测。后续（非阻塞）：Omarchy 插件薄壳；`uninstall --purge-packages` 待决（倾向不加，见 §12）。

---

## 12. 风险与已知局限

1. **上游接口脆弱性**：依赖 `full.zip` 不含 custom.yaml。缓解 = zip 安全扫描（§5.3）。
2. **420MB 模型下载体验**：镜像链 + 断点续传。GitHub 上游对 `wanxiang-lts-zh-hans.gram` 直连超时（35s+ 零字节），仅小文件 `full.zip` 可下；国内镜像（NJU/CNB）与加速代理稳定。因此 `cn` 默认是大陆无代理用户的正确选择；`auto`/`upstream` 对大陆用户不友好。assets 下载已加 TCP/TLS/响应头超时（10s/10s/15s），避免被墙源无限挂起。
3. 终端候选窗不可见为 Hyprland 渲染器 bug，工具域外。
4. **改 `omarchy.tray.pinned`**：写数组是整键覆盖，必须读→合并→写回，否则吞 pin。外壳没在跑时写 pin 失败。`omarchy update` 若重排 layout，再跑一次 `ompinyin` 收敛重贴。
5. 待决策：uninstall 是否加 `--purge-packages`（当前倾向不加）。
6. **顶栏 IM 图标在 foot 内不刷新**（仅显示问题，打字/切换/候选正常）：根因是 fcitx5 notificationitem 的 SNI 图标为空且 IM 切换只发 `dbusmenu.LayoutUpdated`、不发 `NewIcon`，quickshell 靠 `NewIcon` 重读图标。**Omarchy 4.0.2 已解除**（foot 内 `Alt+Space` 切中英图标自动跟随）。兜底：`omarchy restart shell` 强制重枚举 SNI；待办方向是换直接读 `fcitx5-remote` / `-n` 的指示器或上报上游。

---

## 13. 关键决策（结论）

核心取舍已分别在 §1–§8 展开，这里只列**非显而易见**的结论：

- **默认仅全拼**：双拼是 `--dsp`；v1 只做雾凇，万象只作 LMDG **模型**（§1.2 / §1.3 / §2.2）。
- **顶栏图标必做**：= 启用 notificationitem **+ pin**，无退出选项；只启用不 pin，图标藏在抽屉里（§6.4）。
- **pin 写入路径**：读→并→**写数组到 shell.json**，不手改其它字段；drop-in 用专用文件名、uninstall 只删该文件，不用 `systemctl revert`（§6.4）。
- **L1 镜像与自升级解耦**：收敛不接管 `/etc/pacman.d/mirrorlist`（单独 `source`）；`update --self` 自升级与数据收敛解耦（§7）。

---

## 14. 术语表

| 词 | 说明 |
|---|---|
| IM / 输入法层 | fcitx5 的输入法实例（`keyboard-us` ↔ `rime`），用触发键切中英 |
| schema / 方案层 | Rime 输入方案（`rime_ice` / `double_pinyin` / …），用 **F4** 切换 |
| `Desired` | 声明式期望终态（Primary / Extra / Model / Channel） |
| 收敛 | 采集现状 → diff 终态 → 只执行差异 → 复核 |
| LMDG | Language Model Data Group；万象整句模型（`.gram`） |
| SNI | StatusNotifierItem：fcitx5 顶栏托盘项的通信协议 |
| `notificationitem` | fcitx5 的 SNI 托盘插件；Omarchy 默认禁用 |
| Algebra | Rime 的拼音反查 / 英文派生规则（`radical_pinyin` / `melt_eng`） |
| 所有权协议 | 以哈希记账区分「工具生成 / 用户改过 / 外来」三类文件的处理策略 |

---

## 15. 测试环境矩阵（T0–T4 金字塔）

| 层 | 环境 | 覆盖 | 成本 |
|---|---|---|---|
| T0 Stub 单测 | 临时 HOME + fake systemctl/rime_deployer/pacman + stub `assets.ResolveStableTag` | 五层 converge、golden、profile roundtrip、tray 合并、幂等零扰动（~70% 代码路径），CI 用 | 秒级 |
| T1 Distrobox Arch 容器 | `distrobox create --image archlinux:latest` | L1–L3 全真实；**需 `--os-override`** | 分钟级 |
| T2 systemd-nspawn（按需） | pacstrap rootfs + `systemd-nspawn -bD` | L4 服务层：stop→写→start；drop-in 真生效 | 中 |
| T3 QEMU/KVM 全新 Omarchy | 官方 ISO + qcow2 backing-file 基线快照 | 端到端：Wayland/Hyprland、IM 三态、herdr、F4、**顶栏图标可见** | 小时级 |
| T4 真机幂等回归（零成本） | 已安装宿主 | ① 重跑全 `[跳过]`；② `--dsp zrm` 后两个 build schema 都有 grammar；③ 顶栏有图标；④ 备份可回滚 | 零 |

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
7. 改 profile / user.yaml / 专用 drop-in：`stop → 写 → start`。改 tray pinned：只走「读→并→写数组」，不手改 `shell.json` 其它字段。
8. 触发键只写白名单 keysym（`Shift_L` 不是 `Shift`）。
9. `fcitx5-remote -s` 只使用 IM 名（`rime` / `keyboard-us`），从不使用 schema id。
10. 专用 drop-in 去掉 `--disable notificationitem`，且 `omarchy.tray.pinned` 数组含 `Fcitx`；写数组必须读→合并→写回，不得丢用户已有 pin。
11. 除 `pacman` 外不以 root 跑；`source` 改 mirrorlist 也只经 sudo。
12. zip 条目不得含 `..` 或绝对路径。
13. 计划与执行共用同一组谓词：已收敛的重跑不重解、不重写字节一致的受管文件、不开 stop 窗口、不建备份目录。
14. stop 窗口必须由 `defer` 关闭：任何提前返回（daemon-reload / drop-in / hotkey 失败）都要把 fcitx5 重新 start，start 失败则把返回值降级为退出码 1 —— 绝不把用户留在无输入法状态。
15. 账本先于终态：每层落盘后就 `SaveLedger()`（只存 `ManagedFiles`/`Assets` 事实），`Desired`/`SchemaList` 仅在收敛成功时由 `Save()` 推进。中途失败不得丢失所有权记账。
16. `install` 以 state.json 的 `Desired` 为基线，只覆盖命令行上**显式出现**的选项；不得静默重置用户选过的 `--dsp` / `--no-model` / `--channel`。
17. `.part` 只能同源续传（`.part.src` 记录来源 URL，换源即删）；下载必须过形状护栏（`MinBytes` + 魔数 + `Content-Length` 比对）后才能进缓存。
18. 写 fcitx5 `profile` 时 `GroupOrder` 永远居末，`Groups/0/Items/N` 永远从 0 连续编号（不留空洞）。
19. 专用 drop-in 的路径**跟随发现的 unit**（`fcitx5.service` 就写 `fcitx5.service.d/`），且 L5 校验的是**内容**（ExecStart 不再 `--disable notificationitem`），不是文件存在。
20. 孤儿受管文件 = 账本 − 期望集合，每次收敛都清（不只 `Model=false`）；只删账本记过的文件，别人写的永不删。
21. 备份失败即拒绝该次覆盖/删除（§5.1「先备份后写」是硬承诺，错误不得丢弃）。
22. 预检必须拦 root 与缺失的 `rime_deployer`/`fcitx5-remote`/`omarchy`；`--os-override` 任意非空值即绕过 ID 检查并告警。
23. SIGINT/SIGTERM 走 context 取消（第二次信号硬退出 130），使 stop 窗口的 defer 仍能收尾。
