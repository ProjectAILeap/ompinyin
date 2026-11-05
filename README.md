# ompinyin

**一条命令，把 Omarchy 上的中文输入法装好。** 雾凇全拼（双拼可选）+ 万象 LMDG 语法模型 +
顶栏输入法指示器（Omarchy 自带）+ 候选框跟随系统主题。装完就能打字；随时重跑；随时撤销。

```bash
ompinyin install
```

它做的事：装好 fcitx5 + rime 和相关包 → 下载雾凇词库与万象模型 → 生成受管的 rime 配置 →
收敛输入法组、确认顶栏指示器在栏上、让候选框跟着 Omarchy 主题 → 自检一遍告诉你哪里不对。
**只改和"应该的样子"不一样的地方**，所以重复跑不会捣乱，`--dry-run` 可以先看它打算动什么。

Omarchy 4.5 起，热键（`Super+I` 循环、左 Shift 进出输入法）、顶栏指示器、候选框配色都**由 Omarchy 自己提供**；
ompinyin 只保证它们到位，并清理老版本自己装过的 notificationitem 托盘 drop-in / `Fcitx` SNI pin / theme-set 钩子
（否则顶栏会出现两个图标）。

## 安装

```bash
curl -fsSL -o ompinyin https://github.com/ProjectAILeap/ompinyin/releases/latest/download/ompinyin_linux_amd64
install -Dm755 ompinyin ~/.local/bin/ompinyin
ompinyin --help          # 验证
```

- 单个静态二进制，零依赖（x86_64 用 `ompinyin_linux_amd64`，ARM 用 `ompinyin_linux_arm64`），可对照
  `checksums.txt` 校验 sha256。
- 从源码：`git clone … && make build && install -Dm755 bin/ompinyin ~/.local/bin/ompinyin`。
- 需要 **Arch/Omarchy**（脚本会检查 `ID=omarchy`）；只有装包那一步会用 `sudo`，**别用 root 跑 ompinyin 本身**。
- 想指定国内镜像装包：`ompinyin source` 后重跑。

## 开始使用

装完直接能打中文。四个键记住就够（Omarchy 4.5+ 的默认键位）：

| 我想 | 按键 |
|---|---|
| 打中文 | 直接打（默认雾凇全拼） |
| 在 rime 里切中英 | 按一下 **右 Shift**（正在打的编码会先上屏） |
| 开/关输入法（关掉后按键盘就是英文） | **左 Shift**（再按一次开回来；开回来用哪个输入法见下面说明） |
| 换输入法（英文键盘 ↔ rime） | **Super+I** 循环 |
| 换方案（全拼 ↔ 双拼） | **F4**，在弹出的「方案选单」里选 |

四个键各管一件事：右 Shift 只在 rime 内切中英，左 Shift 只开/关输入法，`Super+I` 只换当前输入法，`F4` 只换方案。左 Shift 在 rime 里是 `noop`（不参与引擎内切换），所以它不会「时灵时不灵」。

左 Shift 关掉再打开时，用哪个输入法由 fcitx5 决定：**该窗口被 `Super+I` 显式设过就用它，否则用组里的 `DefaultIM`（rime）**。所以「`Super+I` 切到英文键盘 → 左 Shift 关 → 左 Shift 开」可能落回 rime（换了窗口/新应用也会）。要稳定地待在英文键盘，请用 `Super+I` 再切一次，而不是靠左 Shift。`Ctrl+Space` 留给终端（那里它是直通键，不切输入法）。顶栏输入法指示器由 Omarchy 渲染。

（Omarchy 4.0，即 4.5 之前：热键仍由 ompinyin 写入 —— `Ctrl+Space` 在 IM 组内切到英文键盘，左 Shift 在 rime 内切中英。）

## 常用命令

```bash
ompinyin doctor                  # 体检（只读）：服务/输入法状态/顶栏指示器/候选框主题/缩放
ompinyin status                  # 与"应该有"的差异（只读）
ompinyin install                 # 收敛（幂等）
ompinyin install --dry-run       # 只预览，不动手
ompinyin install --dsp zrm       # 全拼 + 自然码双拼
ompinyin update                  # 刷新雾凇/万象到最新并重编译
ompinyin update --self           # 只升级 ompinyin 自己
ompinyin uninstall               # 撤销全部受管产物（保留你的 rime 词库与 Omarchy 的主题目录）
```

退出码：`0` 成功、`1` 执行失败、`2` 用法错误、`3` 预检不过（不是 Omarchy / 磁盘不足 / 缺工具）。

## 双拼

```bash
ompinyin install --dsp zrm                 # 全拼 + 自然码（默认主方案仍是全拼）
ompinyin install --dsp zrm --no-quanpin    # 只要双拼
ompinyin install --dsp flypy --dsp-default # 小鹤双拼作为默认方案
```

布局：`zrm` 自然码 · `flypy` 小鹤 · `mspy` 微软 · `sogou` 搜狗 · `abc` 智能 ABC · `ziguang` 紫光 · `jiajia` 拼音加加。
**最多一个双拼**；切换用 F4。

## 只想要 Shift 切中英，不要那套英文键盘？（可选）

默认把 fcitx5 的输入法组收敛成 `[rime, keyboard-us]` 两项——**只有两项时 IM 热键才可能有动作**
（fcitx5 硬约束：所有 IM 热键都以「组内 > 1 个 IM」为前提）。不想要英文键盘：

```bash
ompinyin install --single-im    # 组里只留 rime；IM 热键不再生效（中英全靠右 Shift）
ompinyin install --english-im pinyin   # 换掉第二个 IM 的 id
```

单 IM 时期望的行为就是「`Super+I` / 左 Shift 什么都不做」，这不是故障。**Omarchy 4.5+ 上会打警告**：
组里没有 `keyboard-*` 时，Omarchy 的 Setup > Typing > Input Methods 会报错、顶栏指示器不显示。
这个 flag 只为脚本兼容保留。

## 旧 X11 应用里的候选框偏小？（可选）

微信、百度网盘这类 XWayland 程序用的是 fcitx5 的 X11 候选窗，它只认一个全局值 `Xft.dpi`。
ompinyin 默认**只诊断不修改**（它是全局的，可能影响别的应用）。确认候选框确实太小后：

```bash
ompinyin install --x11-hidpi     # 写 Xft.dpi=192，并装登录发布/缩放跟随的用户单元
ompinyin install --no-x11-hidpi  # 撤销
```

开了之后如果**某个应用反而变大（约 4 倍）**，说明它同时受两套缩放控制（常见：系统设了 `GDK_SCALE=2`）。
`ompinyin doctor` 会点出来，按 [`docs/qa.md`](./docs/qa.md) 里的说明二选一。

## 遇到问题

先跑这两条（都只读，不会改任何东西），把输出贴给维护者即可：

```bash
ompinyin status
ompinyin doctor
```

高频问题直接看 [`docs/qa.md`](./docs/qa.md)：打不出中文、候选框太小、某个应用字太大、
顶栏没有/有两个图标、锁屏密码不对、切不了输入法、改的配置又变回去了、装包太慢……

用 agent 装机/排障：`.agents/skills/ompinyin-install/SKILL.md`（agent 执行手册）。

## 开发

```bash
make build   # → bin/ompinyin（版本由 git describe 注入）
make test    # go test -race ./...
make lint    # golangci-lint（硬门槛）
make smoke   # 真机预检：会短暂重启 fcitx5，打 tag 前必跑
```

文档：[`docs/design.md`](./docs/design.md) 设计方案（§N 引用）· [`docs/dev-notes.md`](./docs/dev-notes.md)
决策与开发问题 · [`docs/qa.md`](./docs/qa.md) 用户问答。仓库约定见 [`AGENTS.md`](./AGENTS.md)。

## License

[MIT](./LICENSE)
