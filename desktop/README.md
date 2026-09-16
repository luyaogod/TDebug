# TDebug 桌面版(Electron 外壳)

桌面版**不含第二套界面**:窗口加载的就是 Go 二进制内部 `go:embed` 的 `web/dist`,
所以桌面版与 CLI 版(`tdebug serve` + 浏览器)看到的界面、API、行为完全一致。
Electron 只负责:拉起后端 → 打开窗口 → 记住窗口位置 → 关窗时优雅停止后端 → 打包分发。

```
Electron 主进程 (main.js)
  └─ spawn: tdebug.exe desktop --config <数据目录>/config.json
             └─ stdout: TDEBUG_READY {"url":"http://127.0.0.1:28675","pid":1234,...}
  └─ BrowserWindow.loadURL(url)   ← 界面由 Go 提供
  └─ 退出: POST /api/shutdown(优雅)→ 失败再 kill
```

## 目录

| 文件 | 作用 |
| --- | --- |
| `main.js` | 主进程:后端生命周期、窗口、快捷键、窗口位置记忆、绿色版数据目录判定 |
| `dev.js` | 开发启动器:web Vite(5173)+ 后端(28670)+ Electron(热更新) |
| `electron-builder.yml` | 打包配置(只出 NSIS 安装版;绿色 zip 由 build_desktop.bat 组装) |
| `build/.portable` | 绿色版标记文件(进 zip):有它就把数据放程序目录 |
| `build/README-portable.txt` | 绿色版随包说明(进 zip 后叫 `README-portable.txt`,内容为中文使用说明) |
| `scripts/make-icon.mjs` | 生成 `build/icon.png` / `build/icon.ico`(零依赖,程序化绘制) |
| `scripts/smoke.mjs` | 无 GUI 冒烟:验证 Go 侧 READY/状态文件/`/api/shutdown` 契约 |

## 构建与打包

前置:Node 18+、Go 1.26+,且**先构建前端**(前端产物打在 Go 二进制里)。

```powershell
# 0) 首次安装依赖(国内建议指定 electron 镜像,否则下载很慢)
$env:ELECTRON_MIRROR='https://npmmirror.com/mirrors/electron/'
cd desktop; npm install

# 1) 一键:web 前端 + go build + electron-builder + 组装绿色 zip(仓库根目录执行)
build_desktop.bat
# 产物:
#   dist/desktop/TDebug-0.1.0-setup.exe    安装版(可选安装目录、桌面/开始菜单快捷方式)
#   dist/desktop/TDebug-0.1.0-portable.zip 绿色版(解压即用,数据在解压目录)
#   dist/desktop/TDebug-0.1.0-portable/    绿色版解压前的目录(调试用,可删)
```

> 绿色版特意做成 **zip 而不是单文件 exe**:单文件自解压 exe 长得像安装包,容易让用户误以为要安装。

只想单独跑某一步:

```powershell
cd web   ; npm run build                 # 前端 → web/dist
cd ..    ; go build -o tdebug.exe .       # 后端(内含前端)
cd desktop; npm run pack                  # 只解包出 dist/desktop/win-unpacked(调试打包用)
cd desktop; npm run dist                  # 只出安装包(绿色 zip 由 build_desktop.bat 组装)
```

## 开发

```powershell
cd desktop; npm run dev
```

`dev.js` 会:起 Vite(5173)、起后端(固定 `127.0.0.1:28670`,配合 `web/vite.config.ts`
的 `/api` 代理)、再拉起 Electron 加载 Vite 地址 —— 改前端即时热更新,后端日志直出终端,
数据目录是 `desktop/.dev-data`(与正式配置隔离,可随意删)。

只想快速验证"打包后那样跑"(不启动 Vite):

```powershell
cd desktop; npm run start      # 用仓库根的 tdebug.exe 起后端并加载其地址
cd desktop; npm run smoke      # 无窗口冒烟(CI/命令行可用)
```

## 数据目录与端口

| 形态 | 数据目录 | 说明 |
| --- | --- | --- |
| 绿色版(`-portable.zip` 解压后) | **程序所在目录** | 由包内 `.portable` 标记决定;与 CLI 便携包布局一致,`config.json` 放旁边即可共用配置 |
| 安装版(`-setup.exe`) | `%APPDATA%\TDebug` | 升级不丢配置;`Ctrl+Shift+D` 直接打开该目录 |
| 开发态 | `desktop/.dev-data` | 由 `dev.js` 指定 |

绿色版判定顺序(见 `main.js` 的 `portableRoot()`):`TDEBUG_DESKTOP_DATA` > `PORTABLE_EXECUTABLE_DIR` >
程序目录里有 `.portable` 标记 / 已有 `config.json`、`logs`、`.tdebug-serve.json` > `%APPDATA%\TDebug`。
后面那几个"已有文件"的兜底是为了:用户就算把 `.portable` 删了,配置也不会突然"消失"。

- 目录内:`config.json`(含 SSH/数据库明文凭据,注意保护)、`.tdebug-serve.json`(运行状态)、
  `logs/desktop.log`(后端日志,超 2MB 轮转一份 `.1`)、`window-state.json`(窗口位置)、`debug-bps/`(断点持久化)。
- 端口:桌面版首启写 `127.0.0.1:28675`(CLI 版是 28670),被占用会自动顺延 —— 真实地址以
  `TDEBUG_READY` 行为准,也在 `tdebug status` 里可见。**端口变了 localStorage 里的界面设置
  (主题/面板宽度)会另起一份**,想固定就在「设置 → 监听地址」里写死。
- 首次使用(还没有任何服务器环境)不会弹任何提示框:界面直接落到「设置 → 环境」页,
  填好 SSH/区域/企业/数据库并保存即可开始调试。

## 与 CLI 共存

桌面版实例和 CLI 用同一套状态文件与单实例规则:桌面版在跑时,`tdebug status` / `start` /
`exec` / `wslogs` 会自动发现它的地址并驱动同一个会话;反之先起的 `tdebug serve` 也会被桌面版
接管(`attached:true`,关窗不会把别人起的服务停掉)。

## 环境变量

| 变量 | 作用 |
| --- | --- |
| `TDEBUG_DESKTOP_DATA` | 指定数据目录(优先于便携/安装默认值) |
| `TDEBUG_BIN` | 指定后端 `tdebug.exe` 路径 |
| `TDEBUG_DESKTOP_PORT` | 覆盖监听地址(如 `127.0.0.1:0` 让系统分配,便于多开/测试) |
| `TDEBUG_DEV_URL` | 开发模式:加载该地址(如 `http://127.0.0.1:5173`)且不自己拉后端 |
| `TDEBUG_DEBUG` | 后端 stdout 同时打到终端(排障) |
| `ELECTRON_MIRROR` | 仅 `npm install` 时用:electron 二进制下载镜像 |

## 窗口与快捷键

窗口**没有原生菜单栏**(界面自带工具栏,菜单栏多余):需要的操作走快捷键。

| 快捷键 | 作用 |
| --- | --- |
| `F12` / `Ctrl+Shift+I` | 开发者工具 |
| `Ctrl+R` / `Ctrl+Shift+R` | 重新加载 / 强制重新加载(不占 `F5`——那是调试器的"继续") |
| `Ctrl+=` / `Ctrl+-` / `Ctrl+0` | 放大 / 缩小 / 实际大小 |
| `Ctrl+Shift+D` / `Ctrl+Shift+L` | 打开配置目录 / 打开后端日志(排障用) |
| `Ctrl+Q` | 退出(等同关窗,会优雅停止后端) |

## 排障

- **`npm install` 后 electron 跑不起来**(有些 npm 策略会拦下依赖的 install 脚本,不会下载二进制):
  手动补下载即可 —— `cd desktop` → `set ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/`
  → `node node_modules\electron\install.js`(或 `npm install-scripts approve electron`;
  `build_desktop.bat` 已内置这一步自愈)。
- **启动即报"未找到后端程序"**:开发态忘了 `go build -o tdebug.exe .`;安装包不完整请重装。
- **窗口空白/加载失败**:`Ctrl+Shift+L` 打开后端日志;`TDEBUG_DEBUG=1 npm run start` 可让后端日志直出。
- **杀毒/SmartScreen 提示**:安装包未做代码签名(签名与自动更新不在当前范围),选择"仍要运行"即可。
- **端口占用**:会自动顺延;若想固定,设置页改「监听地址」后重启桌面版。
- **进程残留**:正常关窗会优雅停止后端;异常退出时可有残留,用 `tdebug serve --stop`(数据目录见上表)清掉。
- **绿色版换了位置后配置"不见了"**:绿色版数据就在解压目录里,搬动时把整个文件夹一起搬
  (或只把 `config.json` 拷到新目录即可继续用);删掉 `.portable` 标记会改回 `%APPDATA%\TDebug`。
