# TDebug — T100 作业调试器

通过 SSH 在 T100 服务器上驱动 `fglrun -d` 的 `(fgldb)` 文本调试协议，提供**本地 Web 调试界面**
与**命令行控制端**，实现"人操作 GDC 界面 + AI 借助命令行检查分析"的人机协同调试。

从 TDictCli 剥离出来的纯调试项目：只保留调试能力，不含数据字典/镜像/查询等其它功能。

## 功能

- **Web 界面**（`tdebug serve` 后浏览器打开）：源码 + 断点 + 调用栈 + 变量监视（Monaco 编辑器、
  悬停求值、大纲、运行到光标），接口报文日志与重放调试，服务测试，环境/数据库/参数设置。
- **命令行控制端**：`start` / `exec` / `stop` / `source` / `logs` / `locate` / `resolve` / `interrupt`
  等，自动发现后台服务地址；`exec` 透传全部 fgldb 标准调试命令并返回原生文本。
- **人机协同**：程序跑到 INPUT/MENU 等交互语句时会阻塞在 GDC 等人操作（从调试器看与死循环无法区分），
  技能文档给出了判断信号与交接话术（见 `.claude/skills/tdebug-debug.md`）。
- **防呆**：停站停留超时看门狗自动放行（默认 1800s，保护生产区行锁）、断点持久化、单实例服务。

## 构建

需要 Go 1.26+ 与 Node 18+（前端产物经 `go:embed` 打进二进制，必须先构建前端）。

```bash
cd web && npm install && npm run build   # 生成 web/dist
cd .. && go build -o tdebug.exe .
```

Windows 一键打包（exe + config + README → dist/tddebug-portable.zip）：`build_portable.bat`

## 快速开始

1. 配置环境（二选一）：
   - 复制 `config.example.json` 为 `config.json`，填写 `debug.sshs`（SSH/区域/企业/数据库）；
   - 或启动服务后在 Web 界面「设置 → 环境」页里增删改。
2. 启动服务并打开界面：

   ```bash
   tdebug serve                 # 后台常驻(单实例),打印地址后返回
   # 浏览器打开 http://127.0.0.1:28670(端口被占用会自动顺延,以打印地址为准)
   tdebug serve --stop          # 停止后台实例
   tdebug serve --foreground    # 前台运行,日志直出终端
   ```

3. 命令行调试：

   ```bash
   tdebug start bsft001_wf -m asf      # 连 SSH + 启动作业 + 等入口停站(返回 JSON 快照)
   tdebug exec "break 4450"            # 透传 fgldb 命令
   tdebug exec "print ls_sql"
   tdebug exec "continue" --timeout 300
   tdebug stop                         # 可复取的停站现场
   tdebug quit                         # 结束会话(作业窗口随之关闭)
   ```

## 命令一览

| 命令 | 作用 |
| --- | --- |
| `serve` | 启动本地调试服务（默认后台常驻单实例；`--listen`/`--foreground`/`--stop`） |
| `status` | 查看服务状态与活动会话 |
| `start <作业>` | 连接 SSH、启动调试并等到入口停站（`--module/-m`、`--zone`、`--ssh`、`--timeout`） |
| `exec "<fgldb命令>"` | 透传标准调试命令（print/break/next/where/info/watch…），原样返回输出 |
| `quit` | 结束当前调试会话 |
| `stop` | 查看当前停站现场（状态/位置/函数/断点/TOPENT，可反复取用） |
| `source` | 读服务器源码（登录区源码目录白名单只读，`--from/--to` 取行段） |
| `logs` | 会话最近事件（`--tail N`） |
| `locate <函数>` | 定位函数定义到 文件:行（需停站） |
| `resolve <作业>` | 解析作业编号 → 实体程序/模块（不建会话） |
| `interrupt` | 中断运行中/卡住的程序，回到调试器 |
| `env [环境名]` | 查看/切换当前生效的调试环境（SSH 配置） |
| `topent [值]` | 查看/设置会话级 TOPENT override（`--clear` 清除） |
| `wslogs` | 接口报文日志列表（`--service`、`--fail`、`--page`） |
| `wsdebug <rowid>` | 按日志报文参数重放调试，停在入口 |
| `db [--ent N]` | 数据库连接探查：企业(TOPENT) → 账号映射与连接验证 |
| `probe` | 协议驱动器自检尖刺：登录→启动→下断点→步进→求值（`-m/-p/-l`） |
| `install [dir]` | 把 AI 技能（`tdebug-debug.md`）安装到目标项目的 `.claude/skills/` |

全局参数：`--config <路径>`（默认 `config.json`）、`--json`、`-v`；
控制端命令（start/exec/status/quit/stop/source/logs/locate/resolve/interrupt/env/topent/wslogs/wsdebug）另有 `--url` 覆盖自动发现的地址。

## config.json

顶层只需 `debug` 一个配置节；Web「设置」页保存时也只改写该节，其余键原样保留。

```jsonc
{
  "debug": {
    "sshs": [                       // 环境列表(设置-环境-SSH 页维护)
      {
        "name": "恒烁测试区",
        "host": "10.0.0.2", "port": 22, "user": "tiptop", "password": "tiptop",
        "zone": "2",                // 登录菜单选项号(31开发/35测试/36正式/…)
        "topent": "9999",           // 默认企业编号(调试会话 export TOPENT)
        "launchArgs": "",           // 覆盖全局启动参数模板({prog} 替换为作业名)
        "watchdogSeconds": 0,       // 覆盖全局看门狗秒数
        "db": {                     // 该环境一对一挂的数据库(作业解析/日志查询用)
          "type": "oracle",         // oracle | kingbase
          "host": "10.0.0.2", "port": 1521, "service": "t35prd",
          "accounts": [{ "account": "ds", "password": "ds" }]
        }
      }
    ],
    "listen": "127.0.0.1:28670",    // HTTP 监听地址(端口占用自动顺延,顺延不回写配置)
    "launchArgs": "BBDL512840855a 2 12345 'N' {prog}",
    "watchdogSeconds": 1800,        // 停站停留超时自动放行(保护生产区行锁)
    "fglserver": "",                // 留空由 T100 按 SSH 来源 IP 自动设置
    "termWidth": 200, "termHeight": 50,
    "printElements": 1000,          // fgldb 单次 print 的数组元素上限
    "persistBreakpoints": null      // 断点持久化(默认开)
  }
}
```

没有"默认环境"这类配置项：**当前环境是运行时概念**，由「会话」面板选环境（或 CLI `tdebug env <名称>`）
决定，无会话时取 `sshs` 首条；服务重启后回到首条。接口日志按当前会话所属环境的数据库查询。

### 环境变量

| 变量 | 作用 |
| --- | --- |
| `TDEBUG_CONFIG` | 覆盖配置文件路径（优先级高于 `--config`） |
| `TDEBUG_SERVE_LOG` | 后台服务子进程写入的日志路径（由 `serve` 自动设置） |
| `TDBG_RAW=1` | 把 fgldb 协议原始行打到服务日志（排障用） |

### 运行时产物（均已在 .gitignore 中）

- `.tdebug-serve.json` —— 后台实例状态（pid/地址/日志），控制端命令据此自动寻址
- `.tdebug-serve.log` —— 后台服务日志
- `debug-bps/<模块>__<作业>.json` —— 断点持久化

## 目录结构

```
main.go            入口:两个 go:embed(.claude/skills/*.md 与 all:web/dist)
cli/               命令行:cobra 根命令 + 17 个调试子命令 + 后台守护(servebg_*)
debug/             调试核心:fgldb 协议驱动(session.go)、会话管理(manager.go)、
                   REST+WS 服务(api.go)、配置(config.go)、报文日志(wslog.go)、
                   服务测试(wstest.go)、DB 探查(db.go)、协议正则(parser.go)、断点存储(bpsstore.go)
host/              远程服务器共享层:SSH/PTY(ssh.go)、终端行解析(term.go)、
                   登录区动态路径探测(tenv.go)、DB 环境探测(dbprobe.go)、环境模型(env.go)
dbconfig/  cfgfile/  erpdb/   数据库连接模型 / config.json 读写 / Oracle+金仓直连(连接测试)
web/               前端(React 18 + Vite 6 + Monaco + Tailwind v4 + zustand)
.claude/skills/    AI 技能:tdebug-debug.md(tdebug install 可安装到其它项目)
```

## 注意

- 同一时间只允许一个调试会话；`start`/`wsdebug` 会先结束旧会话。
- 服务默认监听 `127.0.0.1:28670`；端口被占用时自动顺延并把真实地址写入状态文件，控制端命令无需关心。
- 只调试测试区；生产区下断点会让程序挂起，注意看门狗与行锁影响。
- 调试链路需要能 SSH 登录的 T100 服务器与 Genero 运行环境（`fglrun -d` / `fgldb`）。
