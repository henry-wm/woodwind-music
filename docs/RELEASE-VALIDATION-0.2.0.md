# 0.2.0 发布验证

日期：2026-11-24。构建主机：macOS Apple Silicon，Go 1.27.3，远程依赖 NSGo v0.3.0；未使用相邻框架工作区替换。

## 本次结果

| 项目 | 结果 |
| --- | --- |
| 完整安静回归 (`go run scripts/test-quiet.go`) | 301 通过、0 失败、10 跳过（均为可选截图测试） |
| macOS / Windows / Android 静态检查 | 全部通过 |
| macOS 原生冒烟 (`go tool nsgo test -target macos`) | 核心播放与交互 7/7、桌面歌词浮窗 3/3、系统媒体控制 1/1 全部通过 |
| macOS arm64 DMG | 构建通过，应用签名、内容白名单及版本/构建号校验通过 |
| Windows x64 | 交叉编译、静态检查、共享逻辑/托盘/设置模拟回归、安装包与便携包构建及白名单审计通过；完整真机验收未完成，按实验性支持发布 |
| Android arm64 APK | 完整后台宿主构建通过，沿用原签名密钥（`.nsgo/android/debug.pem`），v3 签名与 16 KiB 页面对齐检查通过 |
| Android 清单 | `versionName=0.2.0`，`versionCode=200`，`minSdk=28`，`targetSdk=35`，`debuggable=false`，`allowBackup=false`，包含 `dev.oneo.nsgo.PlaybackService` |
| Android 真机验收（荣耀 GT / AMG-AN00，Android 16） | 通过：覆盖安装与版本升级（`131 → 200`）、曲库与酷狗会话保留、前台/后台播放、熄屏（`Dozing`）播放、系统媒体暂停/恢复与唤醒锁释放 |
| 发布包隐私 | APK、macOS 应用及 Windows 便携包文件清单检查通过；仅包含批准的图标与框架声明资源，不含第三方插件和账号存储 |

### Android 真机实测明细（荣耀 GT，Android 16）

测试未修改手机系统音量、全局电池设置或用户曲库/账号，结束时已暂停播放并释放唤醒锁。

- **覆盖安装与升级**：使用 `0.1.0`（`versionCode=131`）同款签名密钥构建最终 `0.2.0`（`versionCode=200`）APK，实测 `adb install -r` 覆盖安装成功，原有酷狗概念版账号、每日推荐与播放会话完整保留，冷启动 173ms，无崩溃日志。
- **前台与后台播放**：播放《过我的生活》（萧煌奇）时 `PlaybackService` 前台服务正常激活（`isForeground=true`、`types=0x00000002`、`category=transport actions=4`），`木听风音乐` 媒体会话处于 `PLAYING(3)`。返回荣耀桌面（`UniHomeLauncher`）后进程 `PID 31357` 保持不变；在桌面发送系统媒体键暂停，真实进度由 `94,382 ms` 推进至 `129,602 ms`（净推进约 35.2 秒），暂停后 `dev.oneo.nsgo.music:playback` 与 `AudioMix` 唤醒锁立即释放。
- **熄屏播放与恢复**：后台通过系统媒体键恢复播放并熄屏进入 `Dozing`；期间进程保持 `PID 31357`，播放与 `AudioMix` 唤醒锁稳定持有；熄屏下发系统媒体键暂停，真实进度由 `129,602 ms` 推进至 `142,628 ms`（净推进约 13.0 秒），暂停后活跃播放唤醒锁完全释放，`logcat` 无异常。

### 验证边界

- **Windows**：本次仅在 macOS 完成交叉编译、静态检查、共享逻辑/托盘/布局模拟测试与打包审计；完整 Windows 真机播放、安装升级、托盘及设备切换仍待验收，继续标为实验性支持。
- **Android**：本次实机未覆盖真实蓝牙耳机切换、来电中断、整夜后台，以及首次通过系统目录选择器授权新 SAF 目录的真机交互（SAF 直读扫描、流式播放与授权释放已由安静回归覆盖）。

## 包与数据边界

- 构建只复制 `resources/` 中三项批准的图标资源及 NSGo 生成的第三方声明、macOS 隐私清单。第三方插件代码和 `plugin-data/` 账号存储仅由各安装实例的数据目录加载，内置兼容目录只包含条目信息。
- APK、macOS 应用及 Windows 便携包均通过 `scripts/prepare-release.py` 白名单检查；框架生成的不完整 Android 基础包已从发布目录移除。
- 保留稳定应用 ID（`dev.oneo.nsgo.music`）与既有 Android 签名密钥。独立 worktree 出包时需复用主仓库的 `.nsgo/android/debug.pem`，避免生成新密钥破坏旧版本覆盖升级路径。

## 最终 SHA-256

| 文件 | 字节数 | SHA-256 |
| --- | --- | --- |
| `wood-wind-music-0.2.0-macos-arm64.dmg` | 12,727,296 | `589b05a09e5584b37f40d1c694a5efd5a28749bdddbd4d11839413854b41df1f` |
| `wood-wind-music-0.2.0-windows-x64-setup.exe` | 10,293,694 | `a49e27c35ee3ff8e2b4354e14b8711c2585100148377a1e2162f092c979fa434` |
| `wood-wind-music-0.2.0-windows-x64-portable.zip` | 15,525,086 | `652306315c27ec867245c9cd864b69400883c8fe7e81b97d54d75ace27e1070f` |
| `wood-wind-music-0.2.0-android-arm64.apk` | 15,741,803 | `355d705c7947dd5870ee0c497a67f7f6c810f92bbba0d64ab778b68cbc983ce3` |

本地审计报告位于已忽略的 `build/release-0.2.0/package-audit.json`，不随二进制包分发。重新构建后需重新运行 `scripts/prepare-release.py`，并更新上述哈希。
