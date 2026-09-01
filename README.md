# ompinyin

> 在 **Omarchy** 上**一键部署中文输入法**：**雾凇全拼 + 万象整句模型 + 顶栏输入法图标**，双拼按需可选。

`ompinyin` 把「装好中文输入法」变成一条**可反复执行**的命令：声明目标终态（全拼 + 万象 + 顶栏图标）→ 采集现状 → 只改不同部分。随时重跑、`--dry-run` 预览、`uninstall` 回滚。

```text
安装后你会得到：
☑ 雾凇拼音（全拼）        —— 默认输入方案
☑ 万象 LMDG 整句模型     —— 智能整句联想（约 420MB）
☑ 顶栏输入法图标         —— 常驻顶栏，无需点「◀」
双拼可选：--dsp zrm | flypy | mspy | sogou | abc | ziguang | jiajia
```

## 开始前

- **Omarchy**（Arch + Hyprland + Wayland）；其它发行版会被预检拒绝。
- 用**普通用户**跑，别用 root（只有 pacman 会 `sudo`）。
- 万象模型约 **420MB**，预检要求约 **≥2GB** 空闲留白。

## 安装

### 方式一：GitHub Releases 下载（推荐）

```bash
curl -fsSL -o ompinyin https://github.com/ProjectAILeap/ompinyin/releases/latest/download/ompinyin_linux_amd64
install -Dm755 ompinyin ~/.local/bin/ompinyin
ompinyin --help   # 验证
```

> `ompinyin_linux_amd64`（x86_64）／`ompinyin_linux_arm64`，静态链接零依赖；下载后可用 `sha256sum` 对照 `checksums.txt`。

**GitHub 直连受限？** 在 URL 前拼加速代理前缀（如 `https://gh-proxy.com/`）。本体仅 ~7MB 静态二进制；更重的雾凇包 + 万象模型安装时走 `--mirror cn` 国内镜像，不依赖 GitHub。

### 方式二：源码构建（仅需 Go）

```bash
git clone https://github.com/ProjectAILeap/ompinyin.git
cd ompinyin && make build && sudo mv bin/ompinyin /usr/local/bin/ompinyin
```

## 快速开始

```bash
ompinyin install --dry-run --json    # ① 预览计划（不改动）
ompinyin install -y                   # ② 执行：全拼 + 万象 + 顶栏图标 + 国内镜像优先
ompinyin status && ompinyin doctor    # ③ 体检：应无差异、全通过
```

> **幂等**：已收敛的机器重跑 `install` 零副作用——不重下、不重写、不开 stop 窗口、不建备份目录。每一步都能先 `--dry-run` 预览；`uninstall` 可逆。
> **装完先确认**：`Alt+Space` 开/关中文；`F4` 弹出 Rime 方案选单（切方案 + 调简繁 / 标点 / 全半角）。

## 装完就能用：两层切换

| 层 | 对象 | 切换方式 | 用途 |
|---|---|---|---|
| fcitx5 输入法（IM） | `keyboard-us` ↔ `rime` | `Alt+Space` / 顶栏图标 | 切「中 / 英」 |
| Rime 方案选单 | `rime_ice` / `double_pinyin` / … + 开关 | **F4** / 顶栏图标右键 | 切方案；调简繁 / 中英标点 / 全半角 / emoji |

- 方案与开关都在 **F4**（Rime 方案选单）里；别用 `fcitx5-remote -s rime_ice`（它切的是 IM 层）。
- 默认只装全拼时方案只有一项，`--dsp` 加了双拼后 F4 才有第二套方案——但开关（简繁 / 中英标点 / 全半角 / emoji）**始终在**。
- 顶栏图标切到 rime 后，右键菜单也能调这些开关（简繁 / 中英标点 / 全半角），与 F4 等价。
- 候选词每页 9 个，`,` / `.` 翻页。

## 常用命令

| 目标 | 命令 |
|---|---|
| 第一次装（默认全拼） | `ompinyin install` |
| 加一种双拼 | `ompinyin switch --dsp [zrm\|flypy\|mspy\|sogou\|abc\|ziguang\|jiajia]` |
| 让双拼当默认 | `ompinyin switch --dsp zrm --dsp-default` |
| 只要双拼、去掉全拼 | `ompinyin switch --dsp zrm --no-quanpin` |
| 去掉双拼 / 全拼做默认 | `ompinyin switch --dsp none` / `ompinyin switch --full` |
| 不要 420MB 模型 | `ompinyin install --no-model`（`--model` 重新启用） |
| 刷新数据到最新 | `ompinyin update`（`--self` 一并升级程序） |
| 装包失败 / 镜像慢 | `ompinyin source` 后重跑 `install` |
| 看状态差异 / 体检 | `ompinyin status` / `ompinyin doctor` |
| 清缓存 / 彻底移除 | `ompinyin clean --legacy` / `ompinyin uninstall` |
| 机器可读输出 | `status --json` / `doctor --json` ・ `--dry-run --json` |

> **`update --self`** 会备份旧二进制、对照 `checksums.txt` 校 sha256 再原子替换；装在 `~/.local/bin` 免权，装在 `/usr/local/bin` 等需 sudo 路径会尝试 `sudo`（无 tty 用 `sudo -n`）。

## 常用选项

| 选项 | 作用 |
|---|---|
| `--yes` / `-y` | 全程免交互（覆盖前先备份用户改过的文件） |
| `--dry-run` | 只预览计划，不改动 |
| `--dsp zrm` | 加一种双拼；`--dsp-default` 让其为默认，`--no-quanpin` 去掉全拼，`--dsp none` 去掉双拼 |
| `--no-model` / `-s` | 不下载万象整句模型；`--model` 重新启用 |
| `--mirror cn / auto / ghproxy / upstream / URL` | 下载源（默认 `cn`：国内镜像优先，失败自动回退官方） |
| `-b` / `--full-backup` | 备份整个 rime + fcitx5 配置目录（而不只受管文件） |
| `--mirror <本地目录>` | **离线安装**：指向放好 `rime-ice-full-stable.zip` / `wanxiang-lts-zh-hans.gram` 的目录（也支持 `file://`） |

双拼取值：`zrm` 自然码 · `flypy` 小鹤 · `mspy` 微软 · `sogou` 搜狗 · `abc` 智能 ABC · `ziguang` 紫光 · `jiajia` 拼音加加。**最多一种**，且 `--dsp-default` / `--no-quanpin` 必须与 `--dsp` 同用。

> **终态继承**：`install` 以 `state.json` 里记录的**上次终态为基线**，只有你**显式给出**的选项会覆盖它——重跑 `install` 不会静默删掉之前选的 `--dsp` / `--no-model` / `--channel`。去掉双拼用 `switch --dsp none`；恢复模型用 `install --model`。

> **下载源 `--mirror`** 只作用于 L2 数据资产，管不到 L1 pacman 包。默认 `cn`（NJU/CNB 镜像）是为大陆无代理用户——GitHub 上游对 420MB 万象模型会限速/超时；海外或已有代理才用 `auto`/`upstream`。L1 包（`fcitx5-rime` / `fcitx5-configtool` / `fcitx5-gtk`）在 `extra` 仓库，镜像由 `/etc/pacman.d/mirrorlist` 决定，ompinyin 不接管。

## 已知问题

- **L1 pacman 安装失败（多为镜像/网络）**：Omarchy 官方镜像走 Cloudflare CDN，直连有时可能超时。先 `ompinyin source`（默认 `--preset cn`，把 `core`/`extra` 指向国内 stock-Arch 镜像并保留 Omarchy 回退，先备份再写入，工具内部 sudo）后重跑 `install`。**仅用 `pacman -Sy` / `-S --needed`，别跑 `pacman -Syu`**（会漂移 stable 锁版本）；`[omarchy]` 仓库无国内镜像，仍需连通官方 `pkgs.omarchy.org`。`--preset upstream` 一键还原。
- **首次安装会接管 `default.custom.yaml`**：已有 rime 配置时把它替换为受管内容（候选数 9、`,` `.` 翻页；`Shift` 等手感设置消失），改动前备份到 `backup-<ts>/`。
- **手改受管文件会被覆盖**：由工具生成的文件请写独立的非受管补丁，别手改。
- **Foot终端全屏输入时可能出现候选框不可见**：Hyprland 渲染器问题，本工具范围外（桌面窗口正常）。

## 关键路径

| 内容 | 路径 |
|---|---|
| Rime 数据目录 | `~/.local/share/fcitx5/rime` |
| fcitx5 配置 | `~/.config/fcitx5/` |
| 资产缓存 | `~/.cache/ompinyin/` |
| 状态清单 | `~/.local/state/ompinyin/state.json` |

> **AI / Agent 装机（可选）**：想让 agent 帮你装 / 修，可用装机助手 skill（`.agents/skills/ompinyin-install/SKILL.md`）。手动装照上方「快速开始」即可。

## 开发 / License

```bash
make build   # 编译 cmd/ompinyin → bin/ompinyin
make test    # go test -race ./...
```

[MIT](./LICENSE)
