---
name: tdebug-debug
description: 通过 TDebug 命令行调试 T100 ERP 作业(4GL/Genero)——对指定作业启动 fgldb 调试会话、透传全部标准调试命令(break/continue/print/where/info/watch 等)并得到原生输出、查询接口报文日志、按指定日志重放调试;人机协同时识别"程序已停在 GDC 界面等用户操作"的交接点,主动告知用户操作并等断点/停站唤醒。用于排查 T100 作业逻辑错误、跟踪变量取值、验证接口报文场景。所有输出使用简体中文 (zh_CN)。
---

# TDebug — T100 作业命令行调试

## 前提

- 先运行 `tdebug serve` 启动调试服务(默认**后台常驻、单实例**,打印地址后立即返回,当前会话可继续输入命令):
  ```bash
  tdebug serve                # 后台启动;Web 界面 http://127.0.0.1:28670(端口占用自动顺延)
  tdebug serve --stop         # 停止后台实例
  tdebug serve --foreground   # 前台运行,日志直出终端(调试用)
  ```
  重复执行 serve 不会起第二个实例,只会提示已在运行;控制端命令(start/exec/…)自动发现后台实例真实地址。
- 若用户开的是**桌面版**(TDebug.exe,Electron),不要另起 `serve`:桌面实例照写状态文件,
  start/exec/… 会自动发现它并驱动同一个会话;停服务用 `tdebug serve --stop`(数据目录用 `--config` 指向
  桌面的 config.json,安装版在 `%APPDATA%\TDebug`,免安装版在 exe 同目录)。
- `config.json` 的 `debug` 节配置了 SSH 与区域
- 作业的 GUI 界面会弹到用户的 GDC 上;断点命中时程序挂起,用户可在 GDC 操作后再继续

## 工作流(AI 调试范式)

```bash
# 1. 启动调试:连 SSH → 启动作业 → 等入口停站(返回 JSON 快照)
tdebug start bsft001_wf -m asf

# 2. 透传任意 fgldb 标准命令,原样返回输出
tdebug exec "break 4450"          # 按行下断点
tdebug exec "break bsft001_wf.b_fill"   # 按函数下断点
tdebug exec "info breakpoints"    # 查看断点
tdebug exec "continue"            # 继续运行(阻塞到下次停站)
tdebug exec "where"               # 调用栈
tdebug exec "print ls_sql"        # 求值变量/表达式(原生格式)
tdebug exec "print g_qryparam.cond"  # record 字段
tdebug exec "next"                # 步过
tdebug exec "ptype g_qryparam"    # 变量类型/结构
tdebug exec "info locals"         # 局部变量
tdebug exec "print arr.getLength()"   # 动态数组长度:一次拿到,别逐个数下标
tdebug exec "display g_req_param" # 挂自动显示:之后**每次停站**都自动打印它
tdebug exec "print a" "print b"      # 一次多条:省掉每条一次进程启动

# 3. 结束会话(作业窗口随之关闭)
tdebug quit
```

`exec` 透传全部 fgldb 标准命令并返回**原生文本**,命令清单见 Genero 文档
Debugger commands(backtrace/where、break、call、clear、continue、delete、disable、
display、down/up、enable、finish、frame、ignore、info、list、next、output、print、
ptype、run、set、signal、step、tbreak、until、watch、whatis)。

### 取值:先 `display`,别一条条 `print`

盯同一批变量走过好几站时,**`display` 比反复 `print` 划算得多** —— 挂一次,
之后每个停站 fgldb 自己把它们打出来,不用每站补三条命令:

```bash
tdebug exec "display g_req_param"      # 挂上
tdebug exec "display g_status.code"    # 可以挂多个
tdebug exec "info display"             # 看挂了哪些
tdebug exec "undisplay 1"              # 摘掉(编号见 info display)
```

两个能省掉大量往返的原生能力:

- **动态数组长度:`print arr.getLength()`**。`ptype` 只说 `DYNAMIC ARRAY OF RECORD`,
  不给长度。别用"从下标 1 一直 print 到报错"去数,那是几十次往返。
- **数组元素的某个字段本就可能是 null**。T100 的 schema 里 `field_name` 常为空,
  真正有内容的是 `label_name` 和中/繁/英标签 —— 数条目要挑**有值的字段**去数,
  否则会得出"这个数组是空的"的错误结论。

### 一次取多个值:批量 exec

`exec` 可以一次给多条命令。单次 CLI 调用的开销几乎全在进程启动(实测约 50ms,
HTTP 往返只有几毫秒),所以批量省掉的是**每条一次进程启动** —— 实测 20 条:
批量 **166ms** vs 逐条 **1383ms**。

```bash
tdebug exec "print g_req_param" "print g_status" "info locals"
tdebug exec --file cmds.txt      # 一行一条;空行与 # 开头跳过(可写注释)
```

批量逐条标注 `[i/N]` 与命令原文。放行类命令(continue/next/step/until/finish/run)执行后
会读一次现场:程序**停住了**就接着往下跑,没停住(还在跑/已退出)才中止,剩余标为未执行。
所以"走一步、立刻取一批值"**一次调用就能做完**:

```bash
tdebug exec "next" "print g_qryparam.cond" "print arr.getLength()"
tdebug exec "continue" "print g_status"    # 命中断点后会接着取值
```

普通错误(`print` 一个不存在的变量)不中止,后面照跑。
`--timeout` / `--wait` 对**每一条**生效,不是整批的总预算。

一个例外:**入口停站时的 `continue`** 走的是"放行程序"的分流(等同 `run`),它**不等停站
就返回**,所以紧跟其后的命令会因为"程序正在运行"而中止。入口放行单独发一条,
用 `tdebug wait --for stopped` 等到停站后再批量取值。

只给一条命令时,输出与不用批量**逐字一致**(不带头部行)。

### 输出太大时:正文有上限,完整内容落本地

单条命令的**显示**默认封顶 2000 行(`exec --max N`,0 = 不限)。超出的部分不打印,
但会给出本地完整副本的路径:

```text
…(共 1476 行,只显示前 30 行;完整副本: D:\…\execlog\主机正式区\20260917-145735.834-info_variables.txt)
```

**要更多就去读那个文件**(本地整读/搜索,零往返),**别重跑命令** —— 重跑会改变现场。
副本与源码镜像同一生命周期:每轮调试启动前清空。

> 如果看到 `⚠ 会话层已按行数/单值长度上限截断过`,那是**工具在协议层就没收全**
> (单命令 2 万行 / 单个 print 值 256KB),本地副本里也没有超出部分,只能缩小命令范围。

### resume 类命令用 `--wait`,不要用 `--timeout`

`continue`/`run`/`until`/`next` 会让程序跑起来,可能要等很久(最久是在界面上等用户)。

```bash
tdebug exec "continue" --wait 15    # ✅ 到点若程序仍在跑就直接返回,不碰程序
tdebug exec "continue" --timeout 15 # ❌ 超时会发 SIGINT
```

**区别是硬/软**:`--timeout` 到点是**硬超时** —— 服务端会发 SIGINT(等价于 `interrupt`)
来探测状态。程序如果正停在 GDC 界面上等用户输入,这一下就是朝它发的信号;TUI 前端的
对话框会被直接取消。`--wait` 只是"不再等了",命令照常执行,下一个停站事件也不会丢。

`--wait` 到点返回 `softTimeout` 与当前状态,此时**先别急着再发 continue**,按下面第 3 节判断。

## 模式:谁在驾驶(纯人工 / 协作)

每次调试会话都有一个模式,决定**谁能做写操作**:

| | **纯人工 solo** | **协作 collab** |
| --- | --- | --- |
| 谁发起 | 用户从 Web 界面发起 | **AI 从 CLI 发起**(`tdebug start` 带 `X-Actor: ai`) |
| 界面写操作 | 用户可写 | 用户**不可写**,只能观察与取值 |
| AI 写操作 | **AI 不可写** | AI 可写 |
| 只读(双方) | `print`/`where`/`info`/`status`/`stop`/`logs`/源码… | 同左 |

**默认由发起方决定**:你用 `tdebug start` 起的会话默认就是**协作模式** —— 用户打开界面时
会看到「协作模式 · AI 主导」的横幅,写操作按钮全部禁用,底部状态栏显示
「⟳ continue(已 12s)」这类在飞命令。

**权限是不对称的**:用户点界面上的按钮可以任意方向切换(「接管」→ 纯人工,「交给 AI」→ 协作);
**AI 不能自行解除纯人工模式** —— 否则这个锁就是摆设。所以:

- 你在 solo 模式下尝试写操作,会拿到 `403 纯人工模式:AI 不可操作。请让用户在界面上点「交给 AI」,
  或由用户自己操作。` → **把这句话转述给用户**,不要重试。
- 你想接管一个 solo 会话,就在对话里请用户点「交给 AI」,别去调 `tdebug mode collab`(会被拒)。

`tdebug mode` 可查看当前模式、状态与正在执行的命令。

**用户在协作模式下的操作会进同一条时间线**:界面上看到的是 `AI` 蓝标、`人` 绿标、`系统` 灰标。
反过来,`tdebug logs --tail 30` 也能看到用户做了什么(点继续、改断点、切换页签等)。

## 环境(SSH)切换与 TOPENT

调试前可先查看/切换当前生效的服务器环境(SSH 配置),并设置本轮的 TOPENT(企业编号):

```bash
# 查看当前生效环境与全部已配置环境(含各自 SSH/区域/数据库与 TOPENT 默认)
tdebug env

# 切换当前环境:把会话重连到该环境(空闲 idle);当前环境只在运行时,不写 config.json
tdebug env 恒烁测试区

# 查看当前会话的 TOPENT override 与配置级默认
tdebug topent

# 设置会话级 TOPENT(仅会话空闲时;下一轮调试启动采用)
tdebug topent 99

# 清除会话级 override(回退到该环境 topent 的配置默认)
tdebug topent --clear
```

- `env`/`topent` 都依赖运行中的 `tdebug serve`,默认自动发现其地址。
- `env <名称>` 会把目标环境设为默认并结束当前调试重连过去,切换前请确认无重要调试进行。
- TOPENT 也可以在会话外由配置决定:每个环境 `debug.sshs[].topent` 就是它的 TOPENT 默认值;
  会话级 override 优先于配置默认,`--clear` 后回到配置默认。

> **它必须是有效的企业编号(数字)。** 这个字段的值会决定 DB 连接与框架的据点校验 ——
> 填成一个据点码之类的文本(配置里那个键就叫 `topent`,很容易填错),框架的前置检查
> (`awsp900_01_preprocess` 查据点)会直接失败,于是:
>
> - **重放跑不到业务逻辑**:程序一路退出,断点怎么设都不命中,而原始日志里的错误
>   跟这个毫无关系 —— 排查时极容易误判成"重放坏了"或"rowid 指错行";
> - 表现是 `g_status.description` 给出 `企业(xx)内,不存在据点XXX` 这类前置错误。
>
> 重放**能不能进业务逻辑取决于它**,而不同类型的作业受影响程度不同(有的作业不做
> 据点校验,所以同一个环境里"有的能重放、有的不能")。`tdebug wsdebug` 会把实际
> 生效的 TOPENT 打印出来,不是纯数字时会直接给出提示;`tdebug topent` 可随时查看。

## AI 信息通道(fgldb 原生命令给不了的信息)

原生命令行负责**操作**(exec 透传),以下命令负责**看**:可复取现场、读服务器源码、
看事件日志、定位函数、解析作业。都依赖 serve,默认自动寻址:

```bash
# 可复取的停站现场:状态/停站位置/函数/原因/断点数/TOPENT(原生输出是瞬态的)
tdebug stop

# 读服务器源码(白名单只读登录区源码目录,不经会话;支持行段,省上下文)
tdebug source bsft001_wf.4gl -m asf          # 按模块解析候选路径读取
tdebug source --path /u1/topprd/erp/asf/4gl/asf_bsft001_wf.4gl
tdebug source bsft001_wf.4gl -m asf --from 4400 --to 4600

# 会话最近事件(谁停在哪/日志/断开),回答"刚才 continue 之后发生了什么"
tdebug logs --tail 50

# 定位函数定义到 文件:行(需停站;fgldb info line)
tdebug locate b_fill

# 启动前解析作业 → 实体程序/模块(gzzz_t;不建会话),配合 source 读源码
tdebug resolve bsft001_wf -m asf

# 中断运行中/卡住的程序(回调试器)
tdebug interrupt

# 程序此刻在等用户还是在空转?一次调用给结论(见下面「人机交接」一节)
tdebug why
tdebug why --no-resume     # 探测完保留现场(默认会自动 continue 放回)

# 等事件,不要轮询:停站/退出/掉线/看门狗 任一发生即返回
tdebug wait --for stopped --timeout 300
tdebug wait --for exit,dead
```

- `stop` / `status` 里带 `waitingForUser` 与 `waitingKind`:停站行本身是交互语句时为真,
  零成本,先看它再决定要不要 `why`。
- 运行态下 `status` 还带 `silentSeconds`(距最近一次协议输出的秒数)——
  长时间静默是"可能在等用户"的信号。
- `exec "continue"/"run"/"until"` 等长阻塞命令透传 `--timeout`(默认 90s),执行完若停站
  会自动回报 `— 已停站 文件:行 (函数) reason=原因`;**建议改用 `--wait`**(见上文),
  因为 `--timeout` 到点会发 SIGINT。
- **`resolve` 只覆盖 `gzzz_t` 里登记的标准 ERP 作业**。WS 服务程序(`wssp*`/`awsp*`)
  **不在那张表里** —— 对它们会回"未能按 gzzz_t 解析(未命中)",**这不是故障**:
  启动时会按作业名搜索模块兜底(`wssp00137 → wss`)。实测 `resolve bsft001_wf` 正常返回
  `→ 实体程序 bsft001_wf(模块 asf)`,而 `resolve wssp00137` 必然未命中。

## 读代码:`source` 读一次,之后看本地副本

`tdebug source` 每次读源码,都会把**整份**源码落一份本地副本,并在输出里给出路径:

```text
== /u1/t35prd/com/wss/4gl/wssp01131.4gl (行 280-320 / 共 1118) ==
本地副本: D:\…\srccache\<环境>\u1\t35prd\com\wss\4gl\wssp01131.4gl
```

**接下来要整读这个文件、或在里面反复搜,直接用那个本地路径。** 一个 1118 行的文件,
与其一次次 `source --from/--to` 挤牙膏,不如整读本地副本一次 —— 不再走 SSH,
也不必迁就行段。

**只要定位、不要正文时用 `--path-only`**(大文件整份打出来会灌爆上下文):

```bash
tdebug source wssp01131.4gl -m wss --path-only
# == /u1/t35prd/com/wss/4gl/wssp01131.4gl (行 1-1118 / 共 1118) ==
# 本地副本: D:\…\srccache\…\wssp01131.4gl
```


这个镜像目录里攒的是**这轮调试经历过的文件**:程序停过的地方(`next`/断点/步入
到哪个文件就收哪个文件)与显式读过的文件。所以翻目录往往能直接看到调用链经过的
那几个文件,不必一个个去 `source`。

**副本只活一轮调试**:每次 `tdebug start` / `wsdebug` 之前会清空整个镜像目录 ——
服务器上的源码是会改的,跨轮次留着只会让人拿着过期代码推理。所以:

- 不要指望上一轮读过的文件还在,要看的文件本轮读一次;
- 副本是"读过的文件"的便利拷贝,权威性在 `source`(每次真读服务器)那一侧。

**整个代码库的检索不在 TDebug 范围内**:它只管这次调试的现场。要在全库范围找
"某个函数在哪被调用"这类问题,用别的工具。

## 只读 SQL:直接查业务数据

排查接口失败时,靠"改入参反复重放"去反推很慢。`tdebug sql` 能直接看一眼数据:

```bash
tdebug sql "select bmaa001,bmaastus from bmaa_t where bmaa001='FCPU010100003'"
tdebug sql --file q.sql                  # 长语句从本地文件读
tdebug sql "select * from t" --ent 907   # 显式指定企业(默认取会话的 TOPENT)
```

### 结果头部那一行是必须看的

```text
企业 99 → 账号 dsdemo(oracle,只读事务,用时 1.67s)
```

**账号由企业编号(TOPENT)决定,不用你操心** —— 但**"查不到数据"时先核对这一行**:
企业编号不对就会连到另一个 schema、拿到 0 行,而那看起来和"数据不存在"一模一样。
实测同一个查询,企业 99 下是 368950 行、企业 907 下是 62 行 —— 认错的代价是**得出错误的业务结论**。

改企业:`tdebug topent <企业号>`(会话级,查 `tdebug topent`),或 `--ent <企业号>`(单次)。

### 限制(都是刻意的,不是没做)

- 只允许**单条** SELECT / WITH。写操作、DDL、PL/SQL 块、多语句、sqlplus 命令一律**明确拒绝** ——
  拒绝时会点名是哪条规则,**绝不静默给你 0 行**(那会被读成"这条数据不存在")。
- 库会话是**只读事务**,常规写会被库自己挡回去。
- **最多回 200 行**。要更多请加 WHERE 收窄。**没有整表导出** —— 这是刻意的,别对正式区跑聚合。

### 别把它当成绝对安全

- **披着 SELECT 外衣的写挡不住**:自治事务(`PRAGMA AUTONOMOUS_TRANSACTION`)、函数副作用,
  都能在一条 SELECT 里真写库并提交 —— 白名单与只读事务**都看不出来**(这两层在这里并不独立)。
- **"只读" ≠ "只读该企业的数据"**:账号常有跨 schema 授权。`dba_*`/`v$*` 这类特权视图已禁,
  但业务表之间没有隔离。
- 口令仍出现在远端命令行里(ps 可见)—— 既有行为。

某环境要关掉这条能力:配置 `debug.sshs[].db.readonlySql = false`(默认开,设置页也有开关)。

## 人机交接:程序把控制权交给用户界面时(关键!)

**原理**:fgldb 协议看不到 GDC 前端。程序一旦跑到交互语句
(`DIALOG`/`INPUT`/`INPUT BY NAME`/`INPUT ARRAY`/`DISPLAY ARRAY`/`MENU`/`PROMPT`/
`OPEN WINDOW` 等)就会阻塞在用户界面等人操作;从调试器看它只是 `running`,
与死循环**完全无法区分**。所以不要傻等——要主动判断"现在该用户操作了",并把
话传给用户。

### 1. 判断"程序在等用户"——先用工具,别靠猜

fgldb 协议看不到 GDC 前端,程序停在交互语句上时也只是一个 `running`。**好消息是
服务端现在替你判了**,不用再自己读源码做预测:

**① 停站那一刻就看字段(零成本,永远先看这个)**

`tdebug stop` / `tdebug status` / 任何 `exec` 之后的停站快照里:

- `waitingForUser: true` + `waitingKind: "menu"|"input"|"display_array"|...`
  → 停站行**就是**交互语句,程序把控制权交给界面了。
  停站在别处就带 `waitingForUser: false`。

**② 运行中静默了很久 → 用 `tdebug why` 拿结论**

`--wait` 到点仍是 `running` 时,别猜,直接问:

```bash
tdebug why
```

它做三件事:interrupt 拿回控制权 → `where` 定位 → 判断停的是不是交互语句。
输出人话结论(`waitingForUser` / `kind` / `evidence` / 停在哪一行),
**默认会自动 `continue` 放回运行**,不打断用户的对话框。

**③ 想等它自己停下来 → 用 `tdebug wait`,不要轮询**

```bash
tdebug exec "continue" --wait 15        # 短期:15 秒内没停站就先拿回控制
tdebug wait --for stopped --timeout 300 # 长期:用户在 GDC 一点,命中断点立刻返回
```

`wait` 是长轮询,事件一到就返回;超时返回 `timedOut`(**不是错误**,退出码 0),
此时状态仍是 `running`,可以再判断一轮。

### 2. 交接动作序列(每轮都走一遍)

```text
① 放行前看 `stop` 里的 waitingForUser,或读源码确认下一站是哪个界面。
② 会进界面 → 先把话说给用户(见下方话术),再放行:
   tdebug exec "continue" --wait 15     # 软等待,不傻等也不发 SIGINT
③ 回来后按返回值分支:
   - 命中断点/已停站 → 用户已操作完,继续分析;
   - softTimeout 仍是 running → tdebug why 判断:
       waitingForUser=true  → 还在等用户,回到 ② 再交接一轮;
       waitingForUser=false → 不是界面,是慢查询/慢循环,换 print/where 查。
   - exit → 流程结束。
```

- **用户操作完成不是"自动回到你"**:只有用户操作触发到断点时程序才停给你看。
  每轮放行前确认"用户操作后会停在哪"(设好断点),否则程序可能一路跑完或再停
  下一个界面,你就会误以为卡死。
- 连续多界面流程(选单→输入→确认→又弹窗)必须**一轮一轮交接**,不要一次
  `continue` 放到底。
- 确认用户在 GDC 前再放行;用户不在时任何等待都无意义。

### 3. 对用户说的话(可直接改编)

- 放行前:"程序将运行到 **<界面/环节>**,需要你在 GDC 上 **<操作,如点保存/输入单号>**;我已在该流程后设断点,你操作完我会自动接着跟踪。"
- `why` 判定停在界面时:"程序已停在界面等你操作(**<界面名>**),请完成 **<操作>** 后告诉我,或直接操作,我设的断点会唤醒我。"
- 超时仍无停站时:"仍未见你操作后的停站——如果你已完成 **<操作>**,告诉我一声,我重新放行并核对流程。"
- 结束时:"这轮已跑完(exit),没有更多界面交互,我继续检查结果。"

### 4. 不要做的事

- 不要无脑重发 `continue`(每次放行前先看 `waitingForUser` 或读源码);
- **不要用 `--timeout` 当软等待** —— 它到点会发 SIGINT,可能打断用户正在输入
  的界面(TUI 下直接取消对话框)。要"不傻等"就用 `--wait`;
- 不要把"程序没停站"一律当死循环 —— 先 `tdebug why` 看它停在哪一行再下结论;
- 不要自己写循环轮询状态 —— 用 `tdebug wait`;
- 不要在没告知用户的情况下长时间静默等待。

### 5. 关于 `set annotate`:不要开

`set annotate 1` 会让 fgldb 在每个停站处只输出一行
`\032\032<绝对路径>:<行>:<偏移>:beg:<地址>`,**代价是源码块被整个吞掉** ——
停站上下文(`stop.source` 里那一窗源码行、`->` 当前行)就没了,而上面第 1 节的
`waitingForUser` 判定正依赖它(`\032` 还会被本工具的 ANSI 剥离器抹掉)。
只在**客制模块导致源码路径解析不出来**时,才手工开它拿权威绝对路径救急。

## 接口报文日志调试

排查接口(wssp/awsp)报文问题时:

```bash
# 条件(与 Web 工具条同一口径):
#   --job      作业编号 wsfa012(支持 * ? 通配)   ← 按「哪个作业」找日志,最常用
#   --service  服务名称 wsfa001(支持 * ? 通配)
#   --pid      服务程序序号 wsfa002                --server / --origin  服务端 / 发起端
#   --result   处理结果 wsfa006(000=成功)          --fail 只看失败
#   --from/--to 时间窗(wsfa003 >= / wsfa004 <=)    --page 页码(每页 50)
tdebug wslogs --job "wssp*" --from 2026-09-10 --to 2026-09-17
tdebug wslogs --job wssp01131 --fail --from 2026-09-10 --to 2026-09-17
tdebug wslogs --service "oa.schema.data.get" --server T100 --origin OA --from 2026-09-11 --to 2026-09-12
tdebug wslogs --pid 861637 --from 2026-09-17 --to 2026-09-17

# 看**某一条**的详情与请求/响应报文 —— 排查接口的第一步就是先看清它到底发了什么
tdebug wslogs --show <rowid>            # 加 --json 出原始结构

# 按指定日志重放调试:解析该次调用的作业与报文参数,自动重放并停在入口
tdebug wsdebug <rowid>

# 改入参再重放(界面上那套能力,命令行同样有)
tdebug wsdebug <rowid> --set digi-body.std_data.parameter.production_item_no=
tdebug wsdebug <rowid> --set a.b=123 --set c.d=abc     # 可重复;值按 JSON 解析,留空则清空
tdebug wsdebug <rowid> --request-file my_req.json      # 整份替换(大改用这个)
```

**`--set` 只改 JSON 报文。** 报文是 XML 的(MES 侧的接口大多是)只能走文件:

```bash
tdebug wslogs --show <rowid> --save-request req.xml   # 导出原文
# 改 req.xml
tdebug wsdebug <rowid> --request-file req.xml          # 用改后的报文重放
```

`--set` 的路径必须**已经存在**(不自动造层级):拼错一个段就会改出一份形状不对的
报文,那比重放时报错难查得多。要改的字段名从 `wslogs --show` 的报文里看。

### 报文是 XML 还是 JSON,决定了很多事

两种形态都有(MES 侧多为 XML,OA 侧多为 JSON),形状与改法都不同:

| | JSON | XML |
| --- | --- | --- |
| 简单参数 | 嵌套对象里的字段 | `<parameter key="X" type="string">值</parameter>` |
| 成组数据 | 嵌套对象/数组 | `<data name="组名"><row seq="1"><field name="字段名">值</field></row></data>` |
| 企业 / 据点 | 报文头里的 datakey | `<datakey><key name="EntId">99</key><key name="CompanyId">DSCNJ</key></datakey>` |
| `--set` 改入参 | ✅ | ❌ 只能 `--save-request` 导出、改完 `--request-file` |

XML 请求有三条硬规则(取自框架源码 `com/lib/4gl/cl_aws.4gl`,不照做会得到看不出所以然的错):

1. **节点名是固定的**:参数是 `parameter[@key=...]`,成组数据是
   `data[@name=...]/row[@seq=N]/field[@name=...]`。
2. **`<service name>` 照抄报文里原来的那个**,别自己简写 —— 框架拿它去 `gzja_t` 精确匹配,
   简写会回 `[xxx]Service not found`。
3. **企业/据点取自 `<datakey>`**,不是请求体里的 `site_no`;`EntId` 为空时才回退到会话的 TOPENT
   (所以 TOPENT 填错会导致前置校验失败)。

要手工造一份 XML 请求时,最省事的办法是 `tdebug wslogs --show <rowid> --save-request req.xml`
拿一份**同一个服务**的真实报文当底稿改,而不是从零拼。

### 这次重放忠实吗?先看请求报文完不完整

忠实度取决于**服务器上那份原始请求报文文件还在不在**:

- **文件还在** → 重放直接复用它,最忠实,与入库文本完不完整无关;
- **文件已清理** → 只能用入库的文本,而它可能**被截断过**(`--show` 会在标题栏标出
  「不完整」)。**JSON 报文被截断后必然重放失败**:框架先按 JSON 解析,失败后落到 XML
  路径,直接崩在 `Start tag expected` —— 表现是程序一路退出、断点怎么设都不命中,
  极容易误判成"rowid 指错行"或"重放坏了"。

这种情况 `wsdebug` 会打一行 `⚠ 原始请求文件已不在服务器上…`。看到它就别再往断点和变量上找原因了。

**判断某一条完不完整,看 `--show` 的请求标题行**,它会直接把结论写出来:

```
── 请求报文(805 字符 · 完整,记录大小 805)              ← 可以放心重放
── 请求报文(968 字符 · 不完整,记录大小 1324,只存下 968 字符)  ← 这条重放会崩
```

**逐条判断,别推广**:上一条不完整不代表这一条也残缺。实测有人看到一次"不完整"
就以为整批样本都不可信,差点把一条完好的记录判死 —— 而它重放得好好的。

### 「查无数据」类失败:用改一个入参做对照

接口回"没有符合条件的数据"这类结局时,断点 + `print` 只能看到**最终**的
`l_i = 0` / `g_errno = sub-01321`,看不出是 WHERE 里**哪一条**条件把行滤掉的。
这里有效的办法是**控制变量**:从 `--save-request` 导出原文,每次只改一个字段重放。

三种最有效的改法:

| 改什么 | 在程序里看什么 | 说明什么 |
| --- | --- | --- |
| 把某个条件字段**留空** | 拼出的 WHERE 里那条消失(常变成 `1 = 1`),且查询出得来数据 | 其余条件本身没问题,疑点集中在该字段 |
| 换成**已知存在的真实键值** | 查询命中、调用成功 | 只差这一个字段;原来的值在库里确实没有匹配行 |
| 把日期推到很远的未来 / 改大小写 | 结果**不变** | 排除"生效窗口""大小写"这类猜测 |

XML 报文只能走 `--request-file`,JSON 报文可以用 `--set` 一次改一个(见上文)。

> **不是所有"查无数据"都能分开**。以 INNER JOIN 为例,"主表根本没这条"与"主表有、
> 子表没有可用行"都会返回 0 行,而工具没有只读 SQL 入口。遇到这种情况**在结论里把两种
> 可能并列写出来**,别把推断说成结论。

> 重放的**响应**只会写进新的临时文件,**不会覆盖**该次调用在服务器上的原始响应报文 ——
> 所以 `wslogs --show` 看到的响应始终是当初真实返回的那一份,可以放心当作证据。

### 四个名字说的是四样东西(认错就筛出空结果)

同一行日志在界面、命令行、JSON、T100 字典里叫法不同:

| 界面上 | CLI | JSON | T100 字段 | 长什么样 |
| --- | --- | --- | --- | --- |
| 服务 | `--service` | `service` | wsfa001 | `oa.schema.data.get`(反域名,**不是作业名**) |
| 作业 | `--job` | `job` | wsfa012 | `wssp01131`(按作业找,用这个) |
| 服务程序 | `--pid` | `pid` | wsfa002 | `861637`(数字,一次调用一个) |
| 服务端 / 发起端 | `--server` / `--origin` | 同名 | wsfa018 / wsfa013 | `T100` / `OA` |

一个常见误会:**`--service wssp01131` 筛不出任何东西** —— `wssp01131` 是作业(wsfa012),
服务名是反域名。要按它找就用 `--job`。

**列表里的 `ERR` 与响应正文里的 `description` 是两个来源,可能对不上。**
`ERR` 取自日志表的 wsfa014(服务程序/框架记下的那条错误),响应正文里的
`Status/description` 才是**这次调用实际返回**的内容。判断"为什么失败"要以**响应报文**
为准(用 `--show` 看它),列表那一列只当线索 —— 照着它去查方向可能整个偏掉。

筛不到时命令行会明说"无匹配日志"(退出码仍是 0,没匹配不算错误)。

### 重放之后:入参和结果在哪取(接口作业配方)

`wsdebug` 只把程序停在**入口**,而**入口停站没有调用栈** —— 此时 `where` 报 `No stack`、
`info locals` 报 `No frame selected`、`locate` 不可用,`stop` 显示的是 `? ()`。
别以为重放坏了:还没进 MAIN,当然什么都取不到。

**位置有惯例,变量名要现查。** 框架型作业(wssp/awsp)大致是这么分段的:

| 想取什么 | 断点下在哪 |
| --- | --- |
| 入参 | `cl_ws_api_get_param(...)` **之后** —— 它把报文解析进调用方给的那个 record |
| 执行结果 | 业务处理函数**返回之后**、序列化之前 |
| 序列化后的响应 | `cl_ws_exit()` 之前 |

**别照抄变量名。** 有的作业用全局 `g_req_param` / `g_res_param`,有的用局部
`l_input_m` / `l_res_param` —— 同一个框架下两种写法都真实存在,写死了就会
`print` 一个空 record 还以为是重放坏了。**先在源码里确认**:

```bash
tdebug source <作业>.4gl -m <模块> --path-only   # 拿到本地副本路径与总行数
# 然后在本地副本里搜:
#   cl_ws_api_get_param   → 那个 CALL 的第一个参数,就是入参 record 的名字
#   res_param             → 结果 record 的名字(全局或局部)
```

行号同理要先看源码定(每个作业的流程函数名不同)。典型流程:

```bash
tdebug wsdebug <rowid>                  # 停在入口(无栈,取不了值)
tdebug source wssp01131.4gl -m wss --path-only   # 先拿本地副本路径与总行数
tdebug exec "break <入参解析后那行>" "break <结果组装后那行>"   # 可以一次下多个断点
tdebug exec "continue" --wait 20        # 放行;入口的 continue 不等停站,直接返回
tdebug wait --for stopped --timeout 60  # 等它停到第一个断点
tdebug exec "print g_req_param" "print g_status"          # 第一站:入参
tdebug exec "continue" --timeout 30 "print g_res_param"   # 第二站:结果(命中断点后接着取值)
```

> **调试器里的源文件名带模块前缀**:`wss_wssp323.4gl`、`aws_awsp900_01.4gl`。
> `break` 用**裸行号**或函数名最省事;非要写文件名就得写带前缀的那个
> (`break wss_wssp323.4gl:345`)。注意 `tdebug source` 用的是**磁盘上的名字**
> (`wssp323.4gl`),两者不一样 —— 写错了调试器只会回一句 `No source file named …`。
>
> **跨模块的源码要换 `-m`**:`source` 只按**会话的模块**找路径(会话是 `wss` 就搜
> `wss/4gl` 等),所以程序停进 `aws` 的文件后,要 `tdebug source awsp900_01.4gl -m aws`
> 才读得到。(同一原因:程序停进别的模块时,本地镜像也自动收不到那个文件。)
>
> 另:`break 345` 这种落在空白/注释行上的断点会被调试器**静默吸附**到邻近的可执行行 ——
> `info breakpoints` 里看到的位置才是真正生效的。落在**不可执行段**(如 TYPE 定义区)时
> 则是另一句:`No line 77 in file xxx.4gl` —— 它说的是"这一行不能下断点",**不是**
> "文件里没有这一行"(那个文件可能明明有一千多行)。
>
> **入口停站是易失的**:`wsdebug` 报入口停站后,若隔了几秒才下断点/放行,程序有时已经
> 自行跑完(`state` 变成 `idle`)。重试一次通常就好;稳的做法是**把「下断点」与「放行」
> 紧跟在 `wsdebug` 之后立刻发出**,别中间停手。

> **入口千万不要发 `next`/`step`。** fgldb 在 run 之前不接受它们(回
> `The program is not being run.`),而会话状态已经被乐观地翻成"运行中"且不会回滚 ——
> 之后 `exec` 和 `interrupt` 都会被拒,会话卡死,只能整体断开重来。入口要放行就用
> `continue`(它走"放行程序"的分流)。

### 用户指着某一行日志时(最常见的入口)

用户在 Web「接口日志」页看到可疑的一行,会点开详情、按右上角的**「复制标识」**,
把它粘到对话里给你。**粘过来的是一个裸的唯一标识(rowid / ctid)**,没有别的文字。

拿到它**直接执行**即可,不要去列表里搜、也不要追问用户是哪一行:

```bash
tdebug wsdebug <粘贴过来的标识>
```

**注意 rowid / ctid 是物理行标识**:日志表被 purge 或重组之后会失效 —— 可能报错,
也可能**指到另一行**。所以:

- `wsdebug` 返回的作业/时间如果和用户描述的场景对不上,**先停下来问一句**,
  别顺着错的报文一路查下去;
- 需要回退定位时,用业务条件重查:`tdebug wslogs --job <作业编号> --pid <服务程序>
  --from <日期> --to <日期>`(这三个条件 + 时间窗足以定位一次调用)。

> 查询固定排除 `wsfa001='docno.storage'`(SSO 记录,其 wsfa003 不是时间,会刷满整页)——与 T100 原生
> awsq990 一致;要单独看它们目前只能直接查库。

## 约定与注意事项

- 同一时间只允许一个调试会话;`start`/`wsdebug` 前无需手动 quit,后端会自动结束旧会话
- 只调试**测试区**(config zone),禁止对生产区随意下断点
- `exec "print"` 大数组输出可能很长,优先 print 具体字段
- 用户在 GDC 上的操作(点按钮/单据流)会驱动程序走到断点;等用户操作期间程序就是
  `running` —— 用 `--wait` 放行、用 `why` 判断、用 `wait` 等停站,不要去猜也不要轮询
- 会话状态随时可查:`tdebug status`(含 `waitingForUser` / `silentSeconds`)

## 原生 fgldb 参考(Genero BDL User Guide 6.00 节选)

> 来源:Genero BDL User Guide 6.00 的 `13_programming-tools/2520-fgldb.md`(fgldb 工具页)
> 与 `2586-debugger-commands.md`(Debugger commands 清单),内容已内联于本技能。
> `tdebug exec "<命令>"` 等价于在原生 `(fgldb)` 提示符下逐条输入,输出为原生文本。

### fgldb — interface program for remote debugging

The fgldb command line tool is an interface tool to attach to the FGL integrated
debugger remotely.

**Syntax 1: Debugging an application running on the computer**

```
fgldb -p process-id
```

- `process-id` is the process identifier of the `fglrun` process.

**Syntax 2: Debugging an app running on a mobile device**

```
fgldb -m host[:port]
```

- `host` is the hostname or IP address of the mobile device where the program executes.
- `port` is the TCP debug port number to connect to, default is 6400.

**Options**

| Option | Description |
| --- | --- |
| `-V` | Displays version information. |
| `-h` | Displays options for the tool. |
| `-p process-id` | Attach to an `fglrun` process running on the same computer, by using its process id. |
| `-m host[:port]` | Attach to a running mobile app to debug, using the mobile device hostname/IP and debug port. |

The fgldb tool can be used to:

- Debug an `fglrun` process currently running on the same computer, by using its process id.
- Debug an application on a mobile device, by using the mobile device IP address and its TCP debug port.

### Debugger commands — full list

All commands below are passed through with `tdebug exec "..."`.
As noted in the workflow above, `continue`/`run`/`until` (and similar commands that
resume program execution) block until the program stops again — add `--timeout 300`
(or larger) when running them.

| Command | Description |
| --- | --- |
| `backtrace` / `where` | Prints a summary of how your program reached the current state. |
| `break` | Defines a breakpoint to stop the program execution at a given line or function. |
| `call` | Calls a function in the program. |
| `clear` | Clears the breakpoint at a specified line or function. |
| `continue` | Continues the execution of the program after a breakpoint. |
| `delete` | Removes breakpoints that you have specified in your debugger session. |
| `detach` | Closes the TCP connection of a remote debug session. |
| `disable` | Disables the specified breakpoint. |
| `display` | Displays the specified expression's value each time program execution stops. |
| `down` | Moves down in the call stack. |
| `echo` | Prints the specified text as prompt. |
| `enable` | Enables breakpoints that have previously been disabled. |
| `finish` | Continues the execution of a program until the current function returns normally. |
| `frame` | Selects and prints a stack frame. |
| `help` | Provides information about debugger commands. |
| `ignore` | Defines the number of times a breakpoint must be ignored. |
| `info` | Describes the current state of your program (e.g. `info breakpoints`, `info locals`, `info sources`). |
| `list` | Prints source code lines of the program being executed. |
| `next` | Continues running the program by executing the next source line in the current stack frame, and then stops. |
| `output` | Prints only the value of the specified expression, suppressing any other output. |
| `print` | Displays the current value of the specified expression. |
| `ptype` | Prints the data type or structure of a variable. |
| `quit` | Terminates the debugger session. |
| `run` | Starts the program. |
| `set` | Configures your debugger session and changes program variable values (e.g. `set verbose on`). |
| `source` | Executes a file of debugger commands. |
| `signal` | Sends an interruption signal to the program. |
| `step` | Continues running the program by executing the next line of source code, and then stops. |
| `tbreak` | Sets a temporary breakpoint. |
| `tty` | Resets the default program input and output for future `run` commands. |
| `undisplay` | Cancels expressions to be displayed when the program execution stops. |
| `until` | Continues running the program until the specified location is reached. |
| `up` | Selects and prints the function that called this one, or the function specified by the frame number in the call stack. |
| `watch` | Sets a watchpoint for an expression (optionally with a condition, e.g. `watch i if i >= 3`). |
| `whatis` | Prints the data type of a variable. |
