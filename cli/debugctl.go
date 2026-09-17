package cli

// debug 命令行控制端:薄封装,直连 tdebug serve 的 REST。
// 标准 fgldb 命令用 exec 透传(原样返回输出),这里只封装 serve 没有的生命周期动作:
// SSH 连接 + 指定程序启动调试(start)、接口日志获取(wslogs)、按日志重放调试(wsdebug)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"tdebug/debug"

	"github.com/spf13/cobra"
)

var (
	dbgAPIURL  string
	dbgTimeout int
	dbgZone    string
	dbgSSHName string
	wsService  string
	wsOrigin   string
	wsServer   string
	wsResult   string
	wsPID      string
	wsJob      string
	wsFrom     string
	wsTo       string
	wsOnlyFail bool
	wsPage     int
	wsJSON     bool

	dbgSoftWait int    // exec/step 的软等待秒数(0=不启用,走原来的硬超时)
	dbgExecFile string // exec --file:从文件读多条命令(一行一条)
	dbgWaitFor  string // wait 命令:等哪些事件
	dbgNoResume bool   // why 命令:探测完不自动放回运行

	wsShow         string   // wslogs --show <rowid>:只看这一条的报文
	wsSaveReq      string   // wslogs --show --save-request:把请求原文另存一份(改完再 --request-file)
	wsDebugSet     []string // wsdebug --set k=v:改一个入参再重放(可重复)
	wsDebugReqFile string   // wsdebug --request-file:整份替换入参报文
)

// dbgAPI 调 serve REST;非 2xx 时解析 {"error": ...} 返回错误
func dbgAPI(method, path string, body any) ([]byte, error) {
	base := dbgAPIURL
	if base == "" {
		// 自动寻址:优先取后台实例状态文件里的真实地址(端口被占用顺延过);
		// 无实例时回退 config debug.listen / 内置默认,再给出"请先启动"提示。
		base = debugAutoURL()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// 声明自己是 AI:服务端靠这个区分"谁在驱动"(人从浏览器发,不带此头)。
	// 模式闸门与操作时间线的归属都依赖它。
	req.Header.Set("X-Actor", "ai")
	cli := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接调试服务(%s): %w —— 请先运行 tdebug serve", base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func dbgJSONOut(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// dbgCurrentID 取当前活动会话 id(同一时间只有一个;取列表第一个)
func dbgCurrentID() (string, error) {
	data, err := dbgAPI("GET", "/api/sessions", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Sessions []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	if len(r.Sessions) == 0 {
		return "", fmt.Errorf("没有活动会话,先运行 tdebug start <作业>")
	}
	return r.Sessions[0].ID, nil
}

// dbgWaitStopped 等待会话停站(入口停站/断点命中)。
// 走服务端长轮询 /wait:事件一到立刻返回,不再每 2s 查一次快照。
func dbgWaitStopped(id string, timeout time.Duration) (map[string]any, error) {
	q := url.Values{}
	q.Set("for", "stopped,exit")
	q.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	data, err := dbgAPI("GET", "/api/sessions/"+id+"/wait?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var w struct {
		TimedOut bool `json:"timedOut"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	if w.TimedOut {
		return nil, fmt.Errorf("等待停站超时(%s)", timeout)
	}
	// 再取一次完整快照当返回值(调用方按 snapshot 结构解析、由 start 原样打印)
	snap, err := dbgAPI("GET", "/api/sessions/"+id, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(snap, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// debugStartCmd 封装:SSH 连接 + 对指定程序启动调试,等到入口停站
var debugStartCmd = &cobra.Command{
	Use:   "start <作业编号>",
	Short: "连接 SSH 并对指定程序启动调试,等到入口停站",
	Long: `封装"连服务器 + fglrun -d 启动作业 + 等入口停站"全流程。
之后用 tdebug exec 透传 fgldb 标准命令调试(参考 Genero 调试命令文档)。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{"prog": args[0]}
		if dbgModule != "" {
			body["module"] = dbgModule
		}
		if dbgZone != "" {
			body["zone"] = dbgZone
		}
		if dbgSSHName != "" {
			body["ssh"] = dbgSSHName
		}
		data, err := dbgAPI("POST", "/api/sessions", body)
		if err != nil {
			return err
		}
		var r struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(data, &r)
		snap, err := dbgWaitStopped(r.SessionID, time.Duration(dbgTimeout)*time.Second)
		if err != nil {
			return err
		}
		return dbgJSONOut(snap)
	},
}

// maxBatchCmds 一次批量最多执行的命令条数。
// 每条命令发一次独立 HTTP,不存在单次请求体积的问题 —— 这道上限纯粹是防
// "命令文件里混进上千行",跑起来没完。
const maxBatchCmds = 500

// parseCmdLines 解析命令文件:一行一条,跳过空行与 # 注释行,容忍 CRLF 与 UTF-8 BOM。
// 不认行内注释、不认续行 —— fgldb 命令本身就可能带 #,只能把行首的 # 当注释。
func parseCmdLines(data []byte) []string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var out []string
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln) // 顺带吃掉行尾 \r
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// buildCmdList 组装要执行的命令列表:命令行位置参数在前,--file 里的行在后。
// filePath 为空表示没给 --file。两种来源都拿不出命令时报错(空批没有意义,
// 也不该悄悄成功)—— 分开报是因为这两种"空"要修的地方不一样。
func buildCmdList(args []string, fileData []byte, filePath string) ([]string, error) {
	var cmds []string
	for _, a := range args {
		if s := strings.TrimSpace(a); s != "" {
			cmds = append(cmds, s)
		}
	}
	if filePath != "" {
		fromFile := parseCmdLines(fileData)
		if len(fromFile) == 0 && len(cmds) == 0 {
			return nil, fmt.Errorf("命令文件里没有可执行的命令(空行与 # 注释会被跳过): %s", filePath)
		}
		cmds = append(cmds, fromFile...)
	}
	if len(cmds) == 0 {
		return nil, fmt.Errorf("需要至少一条命令(见 tdebug exec --help),或用 --file 指定命令文件")
	}
	if len(cmds) > maxBatchCmds {
		return nil, fmt.Errorf("一次最多 %d 条命令(本次 %d 条)", maxBatchCmds, len(cmds))
	}
	return cmds, nil
}

// batchAbortReason 一条命令执行完之后,是否该中止整批。返回 (是否中止, 给人看的原因)。
//
// state 只在 cmd 是放行类命令时才有值 —— 那时调用方会去读一次会话快照,它是**实况**:
//   - "stopped":程序停在了某个位置(现场刚打过一行),后一条命令仍建立在一个**已知**的
//     现场上,可以接着跑。"走一步、立刻取一批值"就靠这条能一次调用做完;
//   - 其它(运行中/空闲/已退出),或空串(快照没读到、没能确认):中止 —— 后面每条都会
//     落在一个没确认过的现场上。
//
// 非放行类命令传 state = "" 即可:它们不动程序位置,不需要确认。
//
// 普通错误(print 一个不存在的变量之类)**不中止** —— 那正是"一条打错不影响其余"的场景:
// 它在会话层只是输出里的一行文本,HTTP 仍然是 200。真正的硬失败(4xx)由调用方判。
func batchAbortReason(cmd string, softTimeout bool, state string) (bool, string) {
	if softTimeout {
		return true, "软等待到点,程序仍在运行(命令未被取消,也未发 SIGINT)"
	}
	if !debug.IsResumeCmd(cmd) {
		return false, ""
	}
	switch state {
	case "stopped":
		return false, ""
	case "running":
		return true, "它是放行类命令,程序正在运行;用 tdebug wait --for stopped 等它停下来" +
			"(或 tdebug why 看它在等用户还是空转),停下后再取后面的值"
	case "":
		return true, "它是放行类命令,但没能确认程序停在哪(快照没读到);先 tdebug stop 看清现场再继续"
	}
	return true, "它是放行类命令,而程序已离开调试器(当前 " + state + ");先确认现场再继续"
}

// rawOut 一条裸命令的回路(字段与 /raw 的回包一一对应)
type rawOut struct {
	Lines       []string `json:"lines"`
	SoftTimeout bool     `json:"softTimeout"`
	State       string   `json:"state"`
	Silent      float64  `json:"silentSeconds"`
}

// dbgRawOnce 透传一条 fgldb 命令
func dbgRawOnce(id, cmd string) (*rawOut, error) {
	body := map[string]any{"command": cmd, "timeout": dbgTimeout}
	if dbgSoftWait > 0 {
		body["wait"] = dbgSoftWait
	}
	data, err := dbgAPI("POST", "/api/sessions/"+id+"/raw", body)
	if err != nil {
		return nil, err
	}
	var r rawOut
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func printStillRunning(r *rawOut) {
	fmt.Printf("— 仍在运行(%s,已静默 %.0fs):命令未取消,也未发 SIGINT\n", r.State, r.Silent)
	fmt.Println("  接着可以: tdebug wait --for stopped --timeout 300   (等它停下来)")
	fmt.Println("            tdebug why                              (看它在等用户还是空转)")
}

// dbgExecOne 单条命令:输出与批量出现之前**逐字一致**(不带头部行),向后兼容
func dbgExecOne(id, cmd string) error {
	r, err := dbgRawOnce(id, cmd)
	if err != nil {
		return err
	}
	for _, ln := range r.Lines {
		fmt.Println(ln)
	}
	if r.SoftTimeout {
		printStillRunning(r)
		return nil
	}
	// 执行完成后再取一次快照:若是 step/next/continue 等使程序继续的命令,
	// 停站后自动回报现场(文件:行/函数/原因),避免 AI 再发一次查询才知道停哪。
	return dbgReportStopIfStopped(id, cmd)
}

// dbgExecBatch 批量:顺序执行、逐条标注输出。碰到该中止的情况就停下并说明原因,
// 剩下的标为未执行 —— 不闷头把后续命令发到一个已经变了的现场上。
func dbgExecBatch(id string, cmds []string) error {
	n := len(cmds)
	for i, c := range cmds {
		fmt.Printf("── [%d/%d] %s\n", i+1, n, c)
		r, err := dbgRawOnce(id, c)
		if err != nil {
			// 硬失败(会话不在可发命令的状态等):后续必然同样失败,直接收工
			fmt.Printf("  —— 失败:%v\n", err)
			fmt.Printf("— 已中止于第 %d/%d 条", i+1, n)
			if n-i-1 > 0 {
				fmt.Printf(",后 %d 条未执行", n-i-1)
			}
			fmt.Println()
			return nil
		}
		if len(r.Lines) == 0 {
			fmt.Println("(无输出)")
		}
		for _, ln := range r.Lines {
			fmt.Println(ln)
		}
		// 放行类命令让程序离开当前停站点。它可能停在了**新的**停站点(那就能接着取值),
		// 也可能还在跑、也可能已经退出 —— 读一次快照看实况,别靠猜。
		// 停住了的话 dbgFetchStopSite 顺带把现场打一行出来,后一条命令就建立在已知现场上。
		state := ""
		if debug.IsResumeCmd(c) && !r.SoftTimeout {
			if st, serr := dbgFetchStopSite(id); serr == nil {
				state = st
			} // 读不到就留空,按"没能确认"处理
		}
		stop, why := batchAbortReason(c, r.SoftTimeout, state)
		if !stop {
			continue
		}
		if r.SoftTimeout {
			printStillRunning(r)
		}
		fmt.Printf("— 已中止于第 %d/%d 条:%s", i+1, n, why)
		if n-i-1 > 0 {
			fmt.Printf("(后 %d 条未执行)", n-i-1)
		}
		fmt.Println()
		return nil
	}
	return nil
}

// debugExecCmd 透传任意 fgldb 标准调试命令,原样返回输出。
// continue/run 等命令会阻塞到程序再次停站(与 fgldb 提示符语义一致)。
var debugExecCmd = &cobra.Command{
	SilenceUsage: true, // 报错多为运行期(状态/SSH/库),不是用法问题:别打一大段 usage 误导
	Use:          "exec \"<命令>\" [更多命令...]",
	Short:        "透传 fgldb 标准调试命令(print/break/next/where/info/...),可一次多条",
	Long: `透传 Genero 调试器标准命令,输出为 fgldb 原生文本:
  tdebug exec "break 123"        下断点
  tdebug exec "continue"         继续运行,阻塞到下次停站
  tdebug exec "print ls_sql"     求值变量/表达式
  tdebug exec "info breakpoints" 查看断点
  tdebug exec "where"            调用栈
命令清单见 Genero 文档 Debugger commands(break/continue/print/where/info/watch/...)。

一次给多条就是批量,省掉每条一次进程启动(实测单次调用约 50ms,取十几个值就是秒级的差别):
  tdebug exec "print g_req_param" "print g_status" "info locals"
  tdebug exec --file cmds.txt     # 一行一条;空行与 # 开头跳过

批量逐条标注 [i/N] 与命令原文。放行类命令(continue/next/step/until/finish/run)执行后
会读一次现场:程序**停住了**就接着往下跑 —— 所以"走一步、立刻取一批值"一次调用就能做完:
  tdebug exec "next" "print g_qryparam.cond" "print arr.getLength()"
没停住(还在跑/已退出/没读到)才中止,剩余标为未执行 —— 后面的命令不该落在一个没确认过的
现场上。普通错误(print 一个不存在的变量)不中止,后面照跑。

--timeout / --wait 对**每一条**生效(不是整批的总预算),批量时按需调小。

continue/next 这类会让程序跑起来的命令,建议加 --wait N:到点若程序仍在跑就直接返回
(不再等,也**不发 SIGINT**)。不加 --wait 时,超过 --timeout 会发 SIGINT 探测 ——
那会打断正停在 GDC 界面上等用户输入的程序,所以别再用"短 --timeout"当软等待。`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var fileData []byte
		if dbgExecFile != "" {
			b, err := os.ReadFile(dbgExecFile)
			if err != nil {
				return fmt.Errorf("读取命令文件失败: %s: %w", dbgExecFile, err)
			}
			fileData = b
		}
		cmds, err := buildCmdList(args, fileData, dbgExecFile)
		if err != nil {
			return err
		}
		// 会话 id 只取一次:批量时它本来是每条都要重取的
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		if len(cmds) == 1 {
			return dbgExecOne(id, cmds[0])
		}
		return dbgExecBatch(id, cmds)
	},
}

// debugWhyCmd 探测程序此刻在等用户还是在空转。
var debugWhyCmd = &cobra.Command{
	Use:   "why",
	Short: "探测程序此刻在干什么:在等用户操作,还是在空转(慢查询/慢循环)",
	Long: `把「interrupt → where → 看停在哪一行」三步收成一条命令。

- 已经停在停站态时只做分类,不发任何命令(零副作用);
- 运行中才发 SIGINT,拿到停站现场后判断那一行是不是交互语句
  (INPUT / MENU / DISPLAY ARRAY / CONSTRUCT / PROMPT …);
- 默认探测完自动放回运行 --no-resume 可保留现场。

注意 SIGINT 的副作用随前端模式而变:TUI 的对话框会被取消(int_flag 置位),
GDC 这类 GUI 前端不会取消当前对话框。没有 DEFER INTERRUPT 时 SIGINT 默认终止进程,
现在只挂起是因为 fglrun -d 的调试器拦下了它 —— 所以别把探测当无代价操作频繁用。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		data, err := dbgAPI("POST", "/api/sessions/"+id+"/why", map[string]any{
			"resume": !dbgNoResume,
		})
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			Why struct {
				WaitingForUser bool   `json:"waitingForUser"`
				Kind           string `json:"kind"`
				Evidence       string `json:"evidence"`
				Reason         string `json:"reason"`
				Func           string `json:"func"`
				File           string `json:"file"`
				Line           int    `json:"line"`
				SourceText     string `json:"sourceText"`
				Resumed        bool   `json:"resumed"`
				Risk           string `json:"risk"`
			} `json:"why"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		w := r.Why
		if w.WaitingForUser {
			fmt.Printf("在等用户操作:停在 %s 语句上\n", w.Kind)
		} else {
			fmt.Println("不是在等用户")
		}
		fmt.Printf("  %s\n", w.Evidence)
		if w.File != "" || w.Line > 0 {
			fmt.Printf("  位置: %s:%d %s\n", w.File, w.Line, w.Func)
		}
		if w.SourceText != "" {
			fmt.Printf("  该行: %s\n", w.SourceText)
		}
		if w.Resumed {
			fmt.Println("  已自动放回运行。")
		}
		if w.Risk != "" {
			fmt.Printf("  ⚠ %s\n", w.Risk)
		}
		return nil
	},
}

// debugWaitCmd 长轮询:等到会话出现指定事件或超时。
var debugWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "阻塞等待会话事件(停站/退出/掉线/看门狗),或等到超时",
	Long: `等到会话出现指定事件才返回,替代"隔几秒查一次"的轮询。

  tdebug wait --for stopped --timeout 300   # 等程序停到断点上(用户在 GDC 操作后)
  tdebug wait --for exit,dead               # 等程序结束或掉线

超时**不是错误**:到点返回 timedOut 并给出当前状态(退出码 0),便于脚本分支。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		q := url.Values{}
		if dbgWaitFor != "" {
			q.Set("for", dbgWaitFor)
		}
		q.Set("timeout", strconv.Itoa(dbgTimeout))
		data, err := dbgAPI("GET", "/api/sessions/"+id+"/wait?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			State    string  `json:"state"`
			TimedOut bool    `json:"timedOut"`
			Silent   float64 `json:"silentSeconds"`
			Event    *struct {
				Type string `json:"type"`
			} `json:"event"`
			Stop *struct {
				Reason string `json:"reason"`
				Func   string `json:"func"`
				File   string `json:"file"`
				Line   int    `json:"line"`
			} `json:"stop"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		if r.TimedOut {
			fmt.Printf("超时:状态仍为 %s", r.State)
			if r.State == "running" {
				fmt.Printf("(已静默 %.0fs —— 可能在界面上等用户;可 tdebug why 确认)", r.Silent)
			}
			fmt.Println()
			return nil
		}
		ev := ""
		if r.Event != nil {
			ev = r.Event.Type
		}
		fmt.Printf("事件 %s(状态 %s)\n", ev, r.State)
		if r.Stop != nil {
			fmt.Printf("停站 %s:%d %s reason=%s\n", r.Stop.File, r.Stop.Line, r.Stop.Func, r.Stop.Reason)
		}
		return nil
	},
}

// debugModeCmd 查看/切换会话模式。
var debugModeCmd = &cobra.Command{
	Use:   "mode [solo|collab]",
	Short: "查看或切换会话模式:纯人工(solo) / 协作(collab,AI 主导)",
	Long: `不带参数 = 查看当前模式;带参数 = 切换。

  tdebug mode            # 查看模式、状态与正在执行的命令
  tdebug mode collab     # 请求切到协作模式

模式决定"谁能写":纯人工只有人能操作(AI 只能读),协作只有 AI 能操作(人只读,
要下断点/执行命令请在对话里委托给 AI)。

权限是不对称的:**人**可以任意方向切换(界面上有「接管」/「交给 AI」按钮);
**AI 不能自行解除纯人工模式** —— 否则"纯人工模式下 AI 只能读不能写"就是摆设。
所以 solo → collab 会被服务端拒绝,需要请用户在界面上点「交给 AI」。`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		if len(args) == 0 {
			data, err := dbgAPI("GET", "/api/sessions/"+id, nil)
			if err != nil {
				return err
			}
			if IsJSON() {
				fmt.Println(string(data))
				return nil
			}
			var r struct {
				Mode     string `json:"mode"`
				State    string `json:"state"`
				Inflight *struct {
					Cmd     string  `json:"cmd"`
					Elapsed float64 `json:"elapsed"`
				} `json:"inflight"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			label := "纯人工(只有人能操作)"
			if r.Mode == "collab" {
				label = "协作(AI 主导,人只读)"
			}
			fmt.Printf("模式: %s  状态: %s\n", label, r.State)
			if r.Inflight != nil {
				fmt.Printf("正在执行: %s(已 %.0fs)\n", r.Inflight.Cmd, r.Inflight.Elapsed)
			}
			return nil
		}
		m := strings.ToLower(strings.TrimSpace(args[0]))
		if m != "solo" && m != "collab" {
			return fmt.Errorf("模式只能是 solo 或 collab")
		}
		data, err := dbgAPI("POST", "/api/sessions/"+id+"/mode", map[string]any{"mode": m})
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		fmt.Printf("已切换到 %s\n", m)
		return nil
	},
}

// debugStatusCmd 服务状态 + 活动会话
var debugStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看调试服务状态与活动会话",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := dbgAPI("GET", "/api/status", nil)
		if err != nil {
			return err
		}
		sessions, err := dbgAPI("GET", "/api/sessions", nil)
		if err != nil {
			return err
		}
		fmt.Println(string(st))
		fmt.Println(string(sessions))
		return nil
	},
}

// debugQuitCmd 结束当前会话
var debugQuitCmd = &cobra.Command{
	Use:   "quit",
	Short: "结束当前调试会话(作业窗口随之关闭)",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		if _, err := dbgAPI("DELETE", "/api/sessions/"+id, nil); err != nil {
			return err
		}
		fmt.Println(`{"ok":true}`)
		return nil
	},
}

// wsLogItem 一条接口日志(与 /api/wslogs 的 items 元素同形)
type wsLogItem struct {
	Rowid    string `json:"rowid"`
	Service  string `json:"service"`
	Job      string `json:"job"`
	PID      string `json:"pid"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Duration string `json:"duration"`
	Code     string `json:"code"`
	ErrMsg   string `json:"errMsg"`
	Origin   string `json:"origin"`
	Server   string `json:"server"`
}

// printWSLogItem 打一行摘要 + 一行 rowid。
// 每个字段都带标签:服务名 / 作业 / 服务程序是三样东西,靠列位置对齐会让人认错
// (service 是 oa.schema.data.get 这类长反域名,定宽列一撑就歪)。
func printWSLogItem(it wsLogItem) {
	line := fmt.Sprintf("%s  %s  %sms  作业=%s", it.Start, it.Code, it.Duration, it.Job)
	if it.PID != "" {
		// 服务程序序号 = 界面上「服务程序」那一列:与 --pid 过滤、以及用户截图里的数字对得上
		line += "  程序=" + it.PID
	}
	if it.Service != "" {
		line += "  服务=" + it.Service
	}
	if it.End != "" {
		line += "  结束=" + it.End
	}
	if it.Origin != "" || it.Server != "" {
		line += fmt.Sprintf("  %s/%s", it.Origin, it.Server)
	}
	if it.ErrMsg != "" {
		line += "  ERR=" + it.ErrMsg
	}
	fmt.Println(line)
	fmt.Println("  rowid=" + it.Rowid)
}

// wslOut 单条日志的报文内容(/api/wslogs/content)
type wslOut struct {
	Item    wsLogItem `json:"item"`
	Content struct {
		Request        string `json:"request"`
		Response       string `json:"response"`
		RequestPartial bool   `json:"requestPartial"`
	} `json:"content"`
}

func fetchWSLog(rowid string) (*wslOut, error) {
	data, err := dbgAPI("GET", "/api/wslogs/content?rowid="+url.QueryEscape(rowid), nil)
	if err != nil {
		return nil, err
	}
	var r wslOut
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// setJSONPath 在解析好的 JSON 里按点路径赋值,如
// digi-body.std_data.parameter.production_item_no;路径段落在数组上时按下标取。
//
// 值先按 JSON 解析:能当数字 / true / false / null 就用它,否则当字符串 ——
// 于是 `x=` 给空串、`x=123` 给数字、`x=abc` 给字符串,命令行上不必再挂类型开关。
//
// 路径必须已经存在:不自动造层级 —— 拼错一个段就会悄悄改出一份形状不对的报文,
// 那比当场报错难查得多。
func setJSONPath(root map[string]any, path, rawVal string) error {
	segs := strings.Split(path, ".")
	var val any = rawVal
	if rv := strings.TrimSpace(rawVal); rv != "" {
		var parsed any
		if json.Unmarshal([]byte(rv), &parsed) == nil {
			val = parsed
		}
	}
	var cur any = root
	for i, seg := range segs {
		last := i == len(segs)-1
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return fmt.Errorf("路径不存在: %s(在 %q 处)", path, strings.Join(segs[:i+1], "."))
			}
			if last {
				node[seg] = val
				return nil
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return fmt.Errorf("路径不存在: %s(段 %q 不是合法数组下标)", path, seg)
			}
			if last {
				node[idx] = val
				return nil
			}
			cur = node[idx]
		default:
			return fmt.Errorf("路径走不通: %s(段 %q 处既不是对象也不是数组)", path, seg)
		}
	}
	return nil
}

// applySets 把 --set 的改动应用到请求报文上
func applySets(req string, sets []string) (string, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(req), &root); err != nil {
		return "", fmt.Errorf("入参报文不是 JSON(--set 只改 JSON;这条报文多半是 XML)。"+
			"用 tdebug wslogs --show <rowid> --save-request req.txt 导出原文,改完再 --request-file req.txt: %w", err)
	}
	for _, s := range sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return "", fmt.Errorf("--set 要写成 路径=值,实际 %q", s)
		}
		if err := setJSONPath(root, strings.TrimSpace(k), v); err != nil {
			return "", err
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// buildReplayRequest 组装重放用的入参报文。返回空串 = 用日志里的原报文。
func buildReplayRequest(rowid string) (string, error) {
	if wsDebugReqFile == "" && len(wsDebugSet) == 0 {
		return "", nil
	}
	req := ""
	if wsDebugReqFile != "" {
		b, err := os.ReadFile(wsDebugReqFile)
		if err != nil {
			return "", fmt.Errorf("读取入参文件失败: %s: %w", wsDebugReqFile, err)
		}
		req = string(b)
	}
	if len(wsDebugSet) == 0 {
		return req, nil
	}
	if req == "" {
		// 以日志里的原报文为底稿:--set 是"改一处",不是在空报文上造一份
		lg, err := fetchWSLog(rowid)
		if err != nil {
			return "", err
		}
		if lg.Content.RequestPartial {
			return "", fmt.Errorf("该日志的请求报文不完整(超出读取上限或源文件已清理),不能基于它改入参;" +
				"原样重放不受影响 —— 去掉 --set 即可")
		}
		req = lg.Content.Request
	}
	return applySets(req, wsDebugSet)
}

// printTopentContext 把重放环境的 TOPENT 说明白。
//
// 它是"重放能不能跑进业务逻辑"的关键,可快照里只有 topent/topentCfg/topentShell 三个
// 裸字段 —— 真有人把据点码填进企业编号的位置(配置里就叫 topent),然后一路查不出
// 程序为什么跑不到业务逻辑。
func printTopentContext(snap map[string]any) {
	eff, _ := snap["topentShell"].(string)
	if eff == "" {
		eff, _ = snap["topentCfg"].(string)
	}
	if eff == "" {
		return
	}
	src := "配置默认"
	if o, _ := snap["topent"].(string); o != "" {
		src = "会话覆盖"
	}
	fmt.Printf("重放环境:TOPENT=%s(%s)\n", eff, src)
	if _, err := strconv.Atoi(eff); err != nil {
		fmt.Println("  ⚠ 它不是纯数字。T100 的企业编号是数字,这里更像是把据点码填错了位置 ——")
		fmt.Println("    那样框架的前置校验会失败,程序跑不到业务逻辑(表现:一路退出、断点不命中)。")
		fmt.Println("    改:tdebug topent <企业编号>(例 99)")
	}
}

// debugWslogsCmd 获取接口日志列表
var debugWslogsCmd = &cobra.Command{
	SilenceUsage: true, // 报错多为运行期(状态/SSH/库),不是用法问题:别打一大段 usage 误导
	Use:          "wslogs",
	Short:        "获取接口日志列表(wssp/awsp 报文流水)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if wsShow != "" {
			return showWSLog(wsShow, wsJSON, wsSaveReq)
		}
		// 条件对齐 awsq990(与 Web 工具条一致):服务名称 wsfa001(支持 * ? 通配)、
		// 处理结果 wsfa006、发起端 wsfa013、服务端 wsfa018;时间窗 = wsfa003 >= from 且 wsfa004 <= to
		q := url.Values{}
		q.Set("service", wsService)
		q.Set("result", wsResult)
		q.Set("origin", wsOrigin)
		q.Set("server", wsServer)
		if wsPID != "" {
			q.Set("pid", wsPID)
		}
		if wsJob != "" {
			q.Set("job", wsJob)
		}
		q.Set("onlyFail", strconv.Itoa(boolInt(wsOnlyFail)))
		q.Set("page", strconv.Itoa(wsPage))
		q.Set("pageSize", "50")
		q.Set("startFrom", wsFrom)
		q.Set("endTo", wsTo)
		data, err := dbgAPI("GET", "/api/wslogs?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if wsJSON {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			Items []wsLogItem `json:"items"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		for _, it := range r.Items {
			printWSLogItem(it)
		}
		if len(r.Items) == 0 {
			// 静默成功最容易被当成"服务没起/语法错/通配不支持" —— 明确说清是没匹配
			fmt.Println("无匹配日志(条件太窄,或该时间窗内确实没有记录)")
			if wsFrom == "" && wsTo == "" {
				fmt.Println("  提示:没给 --from/--to 时按原生口径只查最近一段;放宽时间窗试试")
			}
		}
		return nil
	},
}

// showWSLog 打印单条日志的详情与请求/响应报文;reqSave 非空时把请求原文另存一份。
func showWSLog(rowid string, asJSON bool, reqSave string) error {
	data, err := dbgAPI("GET", "/api/wslogs/content?rowid="+url.QueryEscape(rowid), nil)
	if err != nil {
		return err
	}
	if asJSON {
		fmt.Println(string(data))
		return nil
	}
	var r wslOut
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	printWSLogItem(r.Item)
	if reqSave != "" {
		// 报文可能是 XML(不是 JSON),那样 --set 用不了,只能导出原文改完再 --request-file ——
		// 这个开关就是那条路的起点。
		if err := os.WriteFile(reqSave, []byte(r.Content.Request), 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", reqSave, err)
		}
		fmt.Printf("\n请求原文已存到 %s(改完用: tdebug wsdebug %s --request-file %s)\n", reqSave, rowid, reqSave)
	}
	note := ""
	if r.Content.RequestPartial {
		// 这里以前写的是"原样重放不受影响" —— 那是错的,而且害人不浅:
		// 源文件已清理时重放用的就是这段截断文本,JSON 报文会直接崩在解析上。
		note = "\n     ← 不完整:超出读取上限,或服务器上的源文件已被清理。" +
			"\n       源文件还在 → 重放直接复用它,不受影响;" +
			"\n       已清理 → 重放只能用这段截断文本,**JSON 报文会解析失败**" +
			"\n       (框架先按 JSON 解析,失败后落到 XML 路径),表现是程序一路退出、断点不命中;" +
			"\n       wsdebug 遇到这种情况会告警。要改入参也只能走 --request-file(拿不到完整原文)。"
	}
	fmt.Printf("\n── 请求报文(%d 字符)%s\n%s\n", len(r.Content.Request), note, r.Content.Request)
	fmt.Printf("\n── 响应报文(%d 字符)\n%s\n", len(r.Content.Response), r.Content.Response)
	return nil
}

// debugWsdebugCmd 对指定日志发起重放调试
var debugWsdebugCmd = &cobra.Command{
	SilenceUsage: true, // 报错多为运行期(状态/SSH/库),不是用法问题:别打一大段 usage 误导
	Use:          "wsdebug <rowid>",
	Short:        "按接口日志的报文参数启动重放调试,等到入口停站",
	Long: `取出该日志的请求/响应报文,解析出作业与启动参数,自动重放该次调用并停在入口。
rowid 从 tdebug wslogs 输出中取。

**要改入参再重放**(界面上那套能力,命令行同样有):
  --set 路径=值         按点路径改一个字段,可重复。路径形如
                        digi-body.std_data.parameter.production_item_no
                        值先按 JSON 解析(能当数字/布尔就用),否则当字符串;等号后留空即清空
  --request-file 文件   整份替换入参报文(想大改就先把原文存下来改完再送)

例:
  tdebug wsdebug <rowid> --set digi-body.std_data.parameter.production_item_no=
  tdebug wsdebug <rowid> --request-file my_req.json

**重放能不能跑进业务逻辑,取决于会话的 TOPENT(企业编号)。** 成功启动后输出里会打印
实际生效值;若它不是纯数字,多半是把据点码填进了企业编号的位置 —— 那样框架的前置
校验会失败,程序跑不到业务逻辑(表现是:程序一路退出、断点不命中)。用
tdebug topent <企业编号> 改。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rowid := args[0]
		reqOverride, err := buildReplayRequest(rowid)
		if err != nil {
			return err
		}
		body := map[string]any{"rowid": rowid}
		if reqOverride != "" {
			body["request"] = reqOverride
		}
		data, err := dbgAPI("POST", "/api/wslogs/debug", body)
		if err != nil {
			return err
		}
		var r struct {
			SessionID string `json:"sessionId"`
			Warn      string `json:"warn"`
		}
		_ = json.Unmarshal(data, &r)
		if r.Warn != "" && !IsJSON() {
			// 用入库的截断文本重放:后果是"程序一路退出、断点不命中" —— 不说清的话,
			// 下一件事一定是去查断点为什么没生效
			fmt.Printf("⚠ %s\n", r.Warn)
		}
		snap, err := dbgWaitStopped(r.SessionID, time.Duration(dbgTimeout)*time.Second)
		if err != nil {
			return err
		}
		if !IsJSON() {
			printTopentContext(snap)
		}
		return dbgJSONOut(snap)
	},
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// addClientURLFlag 给控制端命令挂 --url(原为 debug 父命令的持久 flag):
// 只挂在真正连接服务的命令上,serve/probe/db 不需要。
func addClientURLFlag(cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.Flags().StringVar(&dbgAPIURL, "url", "", "调试服务地址(默认自动发现运行中的后台实例;也可显式指定)")
	}
}

func init() {
	debugExecCmd.Flags().IntVar(&dbgTimeout, "timeout", 90, "等待停站超时(秒,对批量里的**每一条**生效);到点发 SIGINT 探测,所以长命令建议改用 --wait")
	debugExecCmd.Flags().IntVar(&dbgSoftWait, "wait", 0, "软等待秒数(对每一条生效):到点若程序仍在跑就直接返回(不发 SIGINT、不取消命令);0=不启用")
	debugExecCmd.Flags().StringVar(&dbgExecFile, "file", "", "命令文件:一行一条,空行与 # 开头跳过(与位置参数并用时位置参数在前)")
	debugStartCmd.Flags().IntVar(&dbgTimeout, "timeout", 120, "等待入口停站超时(秒)")
	debugWsdebugCmd.Flags().IntVar(&dbgTimeout, "timeout", 150, "等待入口停站超时(秒)")
	debugWsdebugCmd.Flags().StringArrayVar(&wsDebugSet, "set", nil, "改一个入参再重放:路径=值(可重复;路径形如 digi-body.std_data.parameter.x;等号后留空即清空该字段)")
	debugWsdebugCmd.Flags().StringVar(&wsDebugReqFile, "request-file", "", "整份替换入参报文(与 --set 并用时以它为底稿)")
	debugWaitCmd.Flags().IntVar(&dbgTimeout, "timeout", 300, "最长等待秒数(到点返回 timedOut,不是错误)")
	debugWaitCmd.Flags().StringVar(&dbgWaitFor, "for", "stopped,exit,dead,watchdog", "等哪些事件(逗号分隔:stopped,exit,dead,watchdog)")
	debugWhyCmd.Flags().BoolVar(&dbgNoResume, "no-resume", false, "探测完不自动放回运行(默认会自动 continue 放回)")
	debugStartCmd.Flags().StringVarP(&dbgModule, "module", "m", "asf", "T100 模块目录名(如 asf)")
	debugStartCmd.Flags().StringVar(&dbgZone, "zone", "", "区域代码覆盖(31开发/35测试/36正式;默认取配置)")
	debugStartCmd.Flags().StringVar(&dbgSSHName, "ssh", "", "SSH 配置名(设置页配置的多 SSH;默认取配置)")
	debugWslogsCmd.Flags().StringVar(&wsService, "service", "", "服务名称 wsfa001(支持 * ? 通配)。是 oa.schema.data.get 这类反域名,不是作业名")
	debugWslogsCmd.Flags().StringVar(&wsJob, "job", "", "作业编号 wsfa012(支持 * ? 通配,如 wssp01131 / wssp*)—— 按「哪个作业」找日志的入口")
	debugWslogsCmd.Flags().StringVar(&wsServer, "server", "", "服务端 wsfa018(等值,如 T100)")
	debugWslogsCmd.Flags().StringVar(&wsOrigin, "origin", "", "发起端 wsfa013(等值,如 OA)")
	debugWslogsCmd.Flags().StringVar(&wsResult, "result", "", "处理结果 wsfa006(等值,如 000 成功 / 100 失败)")
	debugWslogsCmd.Flags().StringVar(&wsPID, "pid", "", "服务程序序号 wsfa002(等值;界面「服务程序」列;与 --service 一起用可精确定位某一次调用)")
	debugWslogsCmd.Flags().StringVar(&wsFrom, "from", "", "起始时间下界 wsfa003(yyyy-mm-dd)")
	debugWslogsCmd.Flags().StringVar(&wsTo, "to", "", "结束时间上界 wsfa004(yyyy-mm-dd,含当天)")
	debugWslogsCmd.Flags().BoolVar(&wsOnlyFail, "fail", false, "只看失败日志(wsfa006<>000)")
	debugWslogsCmd.Flags().IntVar(&wsPage, "page", 1, "页码(每页 50 条)")
	debugWslogsCmd.Flags().BoolVar(&wsJSON, "json", false, "输出原始 JSON")
	debugWslogsCmd.Flags().StringVar(&wsShow, "show", "", "只看这一条:按 rowid 打印详情与请求/响应报文(不看列表)")
	debugWslogsCmd.Flags().StringVar(&wsSaveReq, "save-request", "", "配合 --show:把请求原文存成文件(报文是 XML 时改入参只能走这条路)")
	addClientURLFlag(debugStartCmd, debugExecCmd, debugStatusCmd, debugQuitCmd, debugWslogsCmd, debugWsdebugCmd, debugWhyCmd, debugWaitCmd, debugModeCmd)
	rootCmd.AddCommand(debugStartCmd, debugExecCmd, debugStatusCmd, debugQuitCmd, debugWslogsCmd, debugWsdebugCmd, debugWhyCmd, debugWaitCmd, debugModeCmd)
}
