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

# 3. 结束会话(作业窗口随之关闭)
tdebug quit
```

`exec` 透传全部 fgldb 标准命令并返回**原生文本**,命令清单见 Genero 文档
Debugger commands(backtrace/where、break、call、clear、continue、delete、disable、
display、down/up、enable、finish、frame、ignore、info、list、next、output、print、
ptype、run、set、signal、step、tbreak、until、watch、whatis)。
注意:`continue`/`run`/`until` 会阻塞到程序再次停站才返回输出,耗时长时加
`--timeout 300`。

## 环境(SSH)切换与 TOPENT

调试前可先查看/切换当前生效的服务器环境(SSH 配置),并设置本轮的 TOPENT(企业编号):

```bash
# 查看当前生效环境与全部已配置环境(含各自 SSH/区域/数据库与 TOPENT 默认)
tdebug env

# 切换当前环境:把会话重连到该环境(空闲 idle);当前环境只在运行时,不写 config.json
tdebug env 恒烁测试区

# 查看当前会话的 TOPENT override 与配置级默认
tdebug topent

# 设置会话级 TOPENT(仅会话空闲时;下一轮调试启动采用;值不限数字/文本)
tdebug topent 99

# 清除会话级 override(回退到该环境 topent 的配置默认)
tdebug topent --clear
```

- `env`/`topent` 都依赖运行中的 `tdebug serve`,默认自动发现其地址。
- `env <名称>` 会把目标环境设为默认并结束当前调试重连过去,切换前请确认无重要调试进行。
- TOPENT 也可以在会话外由配置决定:每个环境 `debug.sshs[].topent` 就是它的 TOPENT 默认值;
  会话级 override 优先于配置默认,`--clear` 后回到配置默认。

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
```

- `exec "continue"/"run"/"until"` 等长阻塞命令现在会透传 `--timeout` 给服务端
  (不再固定 30s),执行完若停站会自动回报 `— 已停站 文件:行 (函数) reason=原因`。

## 人机交接:程序把控制权交给用户界面时(关键!)

**原理**:fgldb 协议看不到 GDC 前端。程序一旦跑到交互语句
(`INPUT`/`INPUT BY NAME`/`INPUT ARRAY`/`DISPLAY ARRAY`/`MENU`/`PROMPT`/
`OPEN WINDOW` 等)就会阻塞在用户界面等人操作;从调试器看它只是 `running`,
与死循环**完全无法区分**。所以不要傻等——要主动判断"现在该用户操作了",并把
话传给用户。

### 1. 判断"程序可能在等用户操作"的信号

- **源码预测(优先)**:放行前先读当前函数剩余路径(`tdebug source`),
  若即将进入上面的交互语句 → 程序放行后必然停在界面等人。
- **运行特征**:`exec "continue"` 发出后超过约 10–20 秒无输出、无停站返回,
  且此前停站上下文正流向界面入口 → 多半已到界面。
- **interrupt 探测(拿不准时)**:`tdebug interrupt` 把运行中程序拉回
  调试器,再 `tdebug exec "where"` + 读停站行:
  - 停在交互语句(INPUT/MENU/DISPLAY ARRAY…) → **在等用户**;
  - 停在深循环/库调用/`print` 队列 → 是死循环或别的问题,不是交接。

### 2. 交接动作序列(每轮都走一遍)

```text
① 放行前预判:读源码看是否会进界面。
② 会进界面 → 先把话说给用户(见下方话术),再放行:
   tdebug exec "continue" --timeout 15     # 短超时,不傻等
③ 回来后用 tdebug stop / logs 看停在哪:
   - 停到断点 → 用户已操作完,程序走到你的断点 → 继续分析;
   - exit → 流程结束;
   - 超时无停站 → 判定又进界面 → 回 ② 再交接一轮。
```

- **用户操作完成不是"自动回到你"**:只有用户操作触发到断点时程序才停给你看。
  每轮放行前确认"用户操作后会停在哪"(设好断点),否则程序可能一路跑完或再停
  下一个界面,你就会误以为卡死。
- 连续多界面流程(选单→输入→确认→又弹窗)必须**一轮一轮交接**,不要一次
  `continue` 放到底。
- 确认用户在 GDC 前再放行;用户不在时任何等待都无意义。

### 3. 对用户说的话(可直接改编)

- 放行前:"程序将运行到 **<界面/环节>**,需要你在 GDC 上 **<操作,如点保存/输入单号>**;我已在该流程后设断点,你操作完我会自动接着跟踪。"
- 探测到卡在界面时:"程序已停在界面等你操作(**<界面名>**),请完成 **<操作>** 后告诉我,或直接操作,我设的断点会唤醒我。"
- 超时仍无停站时:"仍未见你操作后的停站——如果你已完成 **<操作>**,告诉我一声,我重新放行并核对流程。"
- 结束时:"这轮已跑完(exit),没有更多界面交互,我继续检查结果。"

### 4. 不要做的事

- 不要无脑重发 `continue`(每次放行前先判断进不进界面);
- 不要把"程序没停站"一律当死循环——先 `interrupt`+`where` 看停在哪行再下结论;
- 不要在没告知用户的情况下长时间静默等待。

## 接口报文日志调试

排查接口(wssp/awsp)报文问题时:

```bash
# 列出日志(条件口径与 Web 工具条一致):
#   --service 服务名称 wsfa001(支持 * ? 通配)  --server 服务端 wsfa018  --origin 发起端 wsfa013
#   --result 处理结果 wsfa006(000=成功)        --from/--to 时间窗(wsfa003 >= / wsfa004 <=)
#   --fail 只看失败                             --page 页码(每页 50)
tdebug wslogs --service wssp900 --fail
tdebug wslogs --server T100 --origin OA --result 100 --from 2026-09-11 --to 2026-09-12

# 按指定日志重放调试:解析该次调用的作业与报文参数,自动重放并停在入口
tdebug wsdebug <rowid>
# 之后同样用 exec 透传调试命令
```

> 查询固定排除 `wsfa001='docno.storage'`(SSO 记录,其 wsfa003 不是时间,会刷满整页)——与 T100 原生
> awsq990 一致;要单独看它们目前只能直接查库。

## 约定与注意事项

- 同一时间只允许一个调试会话;`start`/`wsdebug` 前无需手动 quit,后端会自动结束旧会话
- 只调试**测试区**(config zone),禁止对生产区随意下断点
- `exec "print"` 大数组输出可能很长,优先 print 具体字段
- 用户在 GDC 上的操作(点按钮/单据流)会驱动程序走到断点;等待用户操作时
  `exec "continue"` 可能长时间阻塞,属正常现象
- 会话状态随时可查:`tdebug status`

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
