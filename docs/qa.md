# 常见问题（Q&A）

给**使用者**的：我遇到 X，该怎么办、用哪条命令。命令里的 `ompinyin` 指装好的那个可执行文件
（通常在 `~/.local/bin/ompinyin`）。排查任何问题都建议先跑两条只读命令，它们不会改任何东西：

```bash
ompinyin status     # 与「应该有」的差异
ompinyin doctor     # 体检：服务 / 输入法状态 / 顶栏 / 候选框主题 / 缩放
```

还解决不了，把这两条的输出贴给维护者（或让 agent 读 `.agents/skills/ompinyin-install/SKILL.md`）。

---

## 怎么打字？怎么切中英？怎么切全拼/双拼？

| 我想 | 按键 |
|---|---|
| 打中文 | 直接打。默认是**雾凇全拼** |
| 临时打英文（不切换输入法） | 按一下**右 Shift**（会把正在打的编码先上屏；只在 rime 内生效） |
| 开/关输入法（关掉后按键盘就是英文） | **左 Shift**（再按一次开回来；用哪个输入法见下表） |
| 换成英文键盘输入法 / 换回 rime | **Super+I** 循环（Omarchy 4.5+ 的默认键位） |
| 换方案（全拼 ↔ 双拼） | **F4**，在弹出的「方案选单」里选 |
| 翻页 | `,` `.`（或 `-` `=`，取决于方案） |
| 候选词个数 | 默认每页 9 个 |

> 顶栏输入法指示器由 Omarchy 渲染，显示的是「当前输入法」而不是「开/关」（关掉输入法时它仍显示 rime 的标记），也不显示全拼/双拼——方案切换在 F4 里。
> 四个键各管一件事，不重叠：右 Shift 只在 rime 内部切中英（候选框照常出现）；左 Shift 只开/关输入法；
> Super+I 只换「当前是哪个输入法」；F4 只换方案。输入法关掉后 Shift/F4 都不再生效。
> 左 Shift 在 rime 里被显式设成 `noop`，所以它**永远**不会同时触发 rime 内的切换——这正是 4.5 早期版本「左 Shift 时灵时不灵」的原因。
> `Ctrl+Space` 从 4.5 起留给终端（那里它是直通键）。Omarchy 4.0（4.5 之前）上仍是 `Ctrl+Space` 切输入法、左/右 Shift 在 rime 内切中英。

### 左 Shift 打开后是哪个输入法？

不是「上次那个」，而是 fcitx5 按这个规则决定的（`Instance::inputMethod()`）：

| 当前状态 | 打字结果 | 顶栏指示器 |
|---|---|---|
| 激活，且这个窗口用 `Super+I` 显式切过 | 那个输入法（`keyboard-us` = 英文） | `EN` |
| 激活，但这个窗口没显式切过（新窗口、刚开的应用） | 组里的 `DefaultIM` = **rime**（中文） | rime 的标记 |
| 未激活（左 Shift 关掉了） | 键盘直出英文 | 仍显示 rime 的标记（**不反映「已关」**） |

所以「`Super+I` 切到英文键盘，按左 Shift 关掉再打开，又变成中文」是正常现象：那个窗口的显式选择在换窗口/重建输入上下文后不再适用，回落到 `DefaultIM`。
**要稳定待在英文键盘：用 `Super+I` 再切一次，别靠左 Shift。**

## 打不出中文（打出来是英文/没候选框）

1. 先看服务：`ompinyin doctor` 第一行是不是 `服务 … active=true`。
2. 如果提示「服务未运行 + 有一个非单元的 fcitx5 占着 org.fcitx.Fcitx5」——重启一次就好：

```bash
ompinyin install -y        # 会清掉游离实例并把服务拉起来
```

3. 如果提示「build 产物缺失 / schema 未编译」，重跑一次 `ompinyin install -y`（会重建 rime 的编译产物）。

**注意**：不要在服务停止时手跑 `fcitx5-remote`/`fcitx5-remote -n` 去"看看活着没"——那会激活一个游离实例，反而把输入法弄坏。看状态用 `pgrep -x fcitx5` 或 `ompinyin doctor`。

## 某个应用里打不出中文，别的应用都正常（飞书 / 百度网盘这类 CEF 应用）

原生 Wayland 的 Chromium/Electron/CEF 应用**默认关闭** Wayland 输入法协议（`zwp_text_input_v3`），
必须显式传 `--enable-wayland-ime`，否则那个窗口根本收不到 fcitx5 —— 表现就是「只有中文打不出来，
其它一切正常」，重启应用也没用（flag 没变）。

以飞书（`feishu-bin`）为例，它的桌面项里有 `--ozone-platform-hint=auto`（走原生 Wayland）：

```ini
Exec=feishu --ozone-platform-hint=auto --enable-features=WaylandWindowDecorations --enable-wayland-ime %U
```

- 改完必须**完全退出应用再打开**（只关窗口不算，这类应用是单实例的）。
- 判据：`pgrep -af bytedance-feishu | rg enable-wayland-ime` 能看到该 flag。
- 若加了 flag 仍不行：退回 XWayland（走 XIM）并用显式缩放补尺寸 ——
  `--ozone-platform=x11 --force-device-scale-factor=2`。

> 两种情况的区别：**XWayland 应用**靠 XIM/`XMODIFIERS`，通常能打中文；**原生 Wayland 应用**必须支持
> text-input-v3。GTK/Qt/终端都支持，CEF 系需要那个 flag。

## X11 应用（微信、百度网盘）里候选框特别小

这是 XWayland 的固定行为：那些应用用的是 fcitx5 的 **X11 候选窗**，它的字号只认一个全局值 `Xft.dpi`。
ompinyin 默认**只诊断不修改**（因为它是全局的，可能影响别的应用）。确认后开启：

```bash
ompinyin doctor              # 看 X11 HiDPI 那行：期望=192 实际=0 → 就是没发布
ompinyin install --x11-hidpi -y
```

开启后会：在 `~/.Xresources` 写一行 `Xft.dpi`（只动它自己那一块）、装两个用户单元（登录时发布、改显示缩放时跟随）、重启一次 fcitx5。

不需要了就：

```bash
ompinyin install --no-x11-hidpi
```

## 开了 `--x11-hidpi` 之后，某个应用（尤其 GTK 的）字变得特别大（约 4 倍）

说明那个应用同时受两把缩放控制（比如系统里设了 `GDK_SCALE=2`，又读了 `Xft.dpi`）。
`ompinyin doctor` 会直接点出来：「⚠ 会话里已有显式缩放因子 GDK_SCALE=2 …会与它叠加」。

二选一，**不要两个都留**：

- 想让 X11 应用统一跟 `Xft.dpi` 走 → 把 `~/.config/hypr/monitors.lua` 的 `omarchy_gdk_scale` 改成 `1`，重新登录；
- 或者撤销全局缩放 → `ompinyin install --no-x11-hidpi`。

> 微信/百度网盘这类 Electron/Qt 应用有各自的缩放设置（`QT_SCREEN_SCALE_FACTORS=2`、`--force-device-scale-factor=2`），与 `Xft.dpi` 是两套，别同时开。

## 锁屏后密码怎么输都不对（全角惹的祸）

以前踩过：锁屏前切到了「全角」，解锁时密码里的字符被按全角送进密码框（`！` 而不是 `!`），
PAM 直接判错；`full_shape` 连字母数字都会变全角，所以"密码别放符号"也不保险。

- **已修**：全角现在永远从**半角**起步、并且不跨会话记忆。`ompinyin doctor | rg 手感项` 应显示 `[完成]`。
- 万一还是卡住：`Ctrl+Alt+F3` 切到 TTY 登录后改密码（纯字母数字），再排查。

## 按 Ctrl+Space 没反应 / 和 herdr 抢键

**Omarchy 4.5 起 `Ctrl+Space` 不是输入法开关了**：热键归 Omarchy——`Super+I` 循环输入法、左 Shift 进出输入法，
`Ctrl+Space` 只在终端里作为直通键。想切输入法就按 `Super+I` 或左 Shift。

（4.0 上 `Ctrl+Space` 仍是**输入法开关**，下面这段适用于那些机器。）它和 herdr 的 prefix 天生冲突，所以 herdr 的前缀改成了 **`Ctrl+b`**（tmux 习惯）。
herdr 的前缀在它自己的配置里：`~/.config/herdr/config.toml` 的 `[keys] prefix`（应为 `ctrl+b`），改完跑
`herdr server reload-config`。那条配置与输入法无关。

**新装的 Omarchy 会撞**：出厂 herdr 的前缀就是 `ctrl+space`，与输入法开关同键。`ompinyin doctor` 会直接告警
（`⚠ herdr 的前缀也是 ctrl+space…`），照它说的把 herdr 前缀改成 `ctrl+b` 即可。ompinyin **不会**替你改她的配置，
也不会偷偷把触发键换掉。

如果按 `Ctrl+Space` 完全没反应：判据是按键前后各跑一次 `fcitx5-remote`，`2 → 1` 说明生效，`2 → 2` 说明键没到输入法
（个别应用会自己抢占组合键；此时用顶栏输入法图标的菜单或 `fcitx5-remote -t`）。

## 从 1.3.x 升级时 `update --self` 报「候选框主题生成：exit status 1」

Omarchy 4.5 把 `~/.local/share/fcitx5/themes/omarchy/*.svg` 变成了指向 `/usr/share/fcitx5/themes/default/` 的
符号链接，而 1.3.x 的主题生成器要**覆写**这些文件（写穿符号链接 → `Permission denied`），于是旧版收敛失败，
`update --self` 在「升级自身」之前就退出了。两条绕过方式，任选其一：

```bash
# A. 直接换二进制（推荐：一步到位，终态由 1.4+ 的收敛负责清理旧产物）
curl -fsSL -o /tmp/ompinyin https://github.com/ProjectAILeap/ompinyin/releases/latest/download/ompinyin_linux_amd64
install -Dm755 /tmp/ompinyin ~/.local/bin/ompinyin
ompinyin install -y

# B. 先清掉可重建的主题目录，再让旧版自升级
rm -rf ~/.local/share/fcitx5/themes/omarchy
ompinyin update --self -y && ompinyin install -y
```

升级完成后 `ompinyin doctor` 应全绿，`fcitx5` 的启动参数里应有 `--disable notificationitem`（顶栏只留一个指示器）。

## 左 Shift 时灵时不灵 / 右侧 Shift 不能切中英

两种情况都跑一次收敛（同时升级二进制与 rime 配置）：

```bash
ompinyin update --self      # 升级二进制
ompinyin install -y         # 重新编译 rime 配置
```

判据：收敛后 `~/.local/share/fcitx5/rime/default.custom.yaml` 里应当有
`ascii_composer/switch_key/Shift_L: noop` 与 `.../Shift_R: commit_code`——
左 Shift 只开/关输入法，右 Shift 只在 rime 内切中英。

## 顶栏看不到输入法图标

4.5+ 的图标是 Omarchy 自己的 `omarchy.keyboard-layout` 组件（不是 fcitx 的托盘项），所以：

```bash
ompinyin doctor                    # 看「顶栏输入法指示器」那一行
omarchy bar put omarchy.keyboard-layout   # 被移出 bar 时放回去（ompinyin install 也会做）
```

只有当组里只有 1 个输入法、且只有 1 种键盘布局时，Omarchy 才会**故意**隐藏它——
`ompinyin install --single-im` 就属于这种（4.5 会警告）。4.0（4.5 之前）走旧路径，
看 `doctor` 的「托盘 drop-in / 托盘 pin」两行。

## 我改的配置又变回去了

ompinyin 会**持续拥有**这些文件（每次收敛都按终态重写，改动前会备份到
`~/.local/state/ompinyin/backup-<时间>/`）：`~/.Xresources` 里 `ompinyin-managed` 那一块、
`~/.config/fcitx5/conf/classicui.conf`（只拥有主题与字体 6 个键，其它键保留）、
两个 `ompinyin-x11-hidpi.*` 用户单元、`~/.local/share/fcitx5/rime/*.custom.yaml`。

4.0（4.5 之前）还拥有 `~/.config/fcitx5/config` 的 `[Hotkey/TriggerKeys]`；4.5+ 热键归 Omarchy，
我们只**删除**自己历史写下的 `Control+space`/`Alt+space`，绝不写 `[Hotkey/AltTriggerKeys]`。

要改行为请改**参数**（`ompinyin install …`），别改这些文件。另外：同一批文件**只能有一个所有者**——
如果它们还被别的工具（配置同步、备份之类）纳管，那边一写就会把收敛结果退回去。

## 想把候选词个数/翻页键改掉

`menu/page_size: 9` 和 `,` `.` 翻页是受管手感项，改 `.custom.yaml` 会被覆盖。
想要别的值，提一个 issue 说明你的习惯，我们在默认集里评估。

## 换主题后候选框还是旧配色

4.5+ 由 Omarchy 自己的 `omarchy-theme-set-fcitx5` 负责（每次 `omarchy theme set` 都会跑）。
如果没跟上，重跑一次收敛即可：

```bash
ompinyin install -y
```

## 装包太慢 / L1 失败

```bash
ompinyin source          # 换国内镜像（内部会 sudo，别自己再加 sudo）
ompinyin install -y
```

## 想装双拼 / 只装双拼 / 换一个双拼布局

```bash
ompinyin install --dsp zrm -y               # 全拼 + 自然码双拼
ompinyin install --dsp zrm --no-quanpin -y  # 只要双拼
ompinyin install --dsp flypy --dsp-default -y   # 小鹤双拼作为默认方案
```

布局 ID：`zrm` 自然码 · `flypy` 小鹤 · `mspy` 微软 · `sogou` 搜狗 · `abc` 智能 ABC · `ziguang` 紫光 · `jiajia` 拼音加加。
切换用 F4。**最多一个双拼。**

## 怎么升级 / 怎么卸

```bash
ompinyin update --self      # 只升级二进制
ompinyin update             # 刷新 rime 数据资产并重编译
ompinyin uninstall          # 卸掉全部受管产物（保留你的 rime 用户词库）
```

## 我什么都不想改，只想看看会动什么

```bash
ompinyin install --dry-run          # 人类可读
ompinyin install --dry-run --json   # 给脚本/agent 读
```

`plan.needsApply` 为 `false` 就代表没事可做。
