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
| 临时打英文（不切换输入法） | 按一下**左/右 Shift**（会把正在打的编码先上屏） |
| 彻底关掉输入法、用键盘布局打 | **Ctrl+Space**（再按一次回来） |
| 换方案（全拼 ↔ 双拼） | **F4**，在弹出的「方案选单」里选 |
| 翻页 | `,` `.`（或 `-` `=`，取决于方案） |
| 候选词个数 | 默认每页 9 个 |

> 顶栏图标只反映「输入法开/关」，不显示全拼/双拼——方案切换在 F4 里。
> 三个键互不干扰：Shift 只影响 rime 内部（候选框照常出现），Ctrl+Space 关掉输入法后 Shift/F4 都不再生效，F4 只换方案不换中英。

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

`Ctrl+Space` 现在是**输入法开关**。它和 herdr 的 prefix 天生冲突，所以 herdr 的前缀改成了 **`Ctrl+b`**（tmux 习惯）。
herdr 的前缀在它自己的配置里：`~/.config/herdr/config.toml` 的 `[keys] prefix`（应为 `ctrl+b`），改完跑
`herdr server reload-config`。那条配置与输入法无关。

**新装的 Omarchy 会撞**：出厂 herdr 的前缀就是 `ctrl+space`，与输入法开关同键。`ompinyin doctor` 会直接告警
（`⚠ herdr 的前缀也是 ctrl+space…`），照它说的把 herdr 前缀改成 `ctrl+b` 即可。ompinyin **不会**替你改她的配置，
也不会偷偷把触发键换掉。

如果按 `Ctrl+Space` 完全没反应：判据是按键前后各跑一次 `fcitx5-remote`，`2 → 1` 说明生效，`2 → 2` 说明键没到输入法
（个别应用会自己抢占组合键；此时用顶栏输入法图标的菜单或 `fcitx5-remote -t`）。

## 右侧 Shift 不能切中英了

升级一次就好（旧版本要重建 rime 的编译产物）：

```bash
ompinyin update --self      # 升级二进制
ompinyin install -y         # 重新编译 rime 配置
```

## 顶栏看不到输入法图标

- 在 `◀` 抽屉里 → 说明只是没被 pin：`ompinyin install -y` 会补上。
- 完全没有 → `ompinyin doctor` 看「托盘 drop-in / 托盘 pin」两行，再 `ompinyin install -y`。

## 我改的配置又变回去了

ompinyin 会**持续拥有**这些文件（每次收敛都按终态重写，改动前会备份到
`~/.local/state/ompinyin/backup-<时间>/`）：`~/.Xresources` 里 `ompinyin-managed` 那一块、
`~/.config/fcitx5/conf/classicui.conf`（只拥有主题与字体 6 个键，其它键保留）、
两个 `ompinyin-x11-hidpi.*` 用户单元、`~/.local/share/fcitx5/rime/*.custom.yaml`、
`~/.config/fcitx5/config` 的 `[Hotkey/TriggerKeys]`。

要改行为请改**参数**（`ompinyin install …`），别改这些文件。另外：同一批文件**只能有一个所有者**——
如果它们还被别的工具（配置同步、备份之类）纳管，那边一写就会把收敛结果退回去。

## 想把候选词个数/翻页键改掉

`menu/page_size: 9` 和 `,` `.` 翻页是受管手感项，改 `.custom.yaml` 会被覆盖。
想要别的值，提一个 issue 说明你的习惯，我们在默认集里评估。

## 换主题后候选框还是旧配色

候选框跟随 Omarchy 主题是默认终态，由一个 `theme-set` 钩子负责。重跑一次即可：

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
