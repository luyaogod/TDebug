TDebug 桌面版(绿色/便携)使用说明
==================================

一、怎么用
  1. 把整个文件夹解压到任意可写位置(例如 D:\TDebug;不要放在只读目录或压缩包里直接运行);
  2. 双击 TDebug.exe;
  3. 首次打开会直接进「设置 → 环境」,填好 T100 服务器(SSH / 区域 / 企业 / 数据库)并保存;
  4. 回到「调试」页即可开始调试。

二、数据放在哪
  都在本目录(便携版规则,由 .portable 标记文件决定):
    config.json          配置(含 SSH/数据库明文凭据,注意保护,别外发)
    logs\desktop.log     后端日志(排障用;超过 2MB 轮转一份 .1)
    .tdebug-serve.json   运行状态(pid/地址);命令行 tdebug status 也读它
    window-state.json    窗口位置;debug-bps\ 断点持久化
  本目录里已带 config.json 时,命令行版可以直接共用:
    tdebug.exe --config "<本目录>\config.json" status

三、端口与退出
  · 默认监听 127.0.0.1:28675(命令行版默认 28670),被占用会自动顺延;
    真实地址在 .tdebug-serve.json、或 tdebug status 里看。
  · 关闭窗口 = 退出,同时会优雅停止内置的后端服务(不会留下后台进程)。

四、没有菜单栏,快捷键在这
  F12 / Ctrl+Shift+I      开发者工具
  Ctrl+R / Ctrl+Shift+R   重新加载 / 强制重新加载
  Ctrl+= / Ctrl+- / Ctrl+0  放大 / 缩小 / 实际大小
  Ctrl+Shift+D            打开本目录(配置)
  Ctrl+Shift+L            打开日志
  Ctrl+Q                  退出

五、和命令行版的关系
  桌面版只是外壳:界面与 API 都来自随包的 tdebug.exe(内嵌前端),所以行为与
  `tdebug serve` + 浏览器完全一致;桌面版运行期间,命令行 tdebug status/start/exec
  会自动发现并驱动同一个调试会话。

六、安装版 vs 绿色版
  绿色版(本包):数据在本目录,换机器直接拷走,不写注册表;
  安装版(TDebug-<版本>-setup.exe):安装到本机、带开始菜单/桌面快捷方式与卸载项,
  数据在 %APPDATA%\TDebug。两者功能完全相同,选一个用即可。
