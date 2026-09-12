package cli

// debug 信息通道命令(AI 视角的"看"层):原生命令行负责操作(fgldb 透传),
// 这里只补 fgldb 给不了的信息——可复取现场快照、读服务器源码(白名单)、事件日志、
// 函数定位、作业→实体程序解析、远程中断。均依赖 tdebug serve 的 REST。

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	dbgSourceModule string
	dbgSourceFrom   int
	dbgSourceTo     int
	dbgSourcePath   string
	dbgLogsTail     int
)

// debugStopCmd 可复取的停站现场快照:GET /api/sessions/{id}
var debugStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "查看当前调试会话的停站现场(state/位置/断点/TOPENT,可复取)",
	Long: `返回当前调试会话的完整现场快照(与原生命令的瞬态输出不同,可反复取用):
会话状态(state)、停站文件:行/函数/原因、断点列表、TOPENT、运行时长等。
--json 输出原始快照(默认),文本模式给摘要。无会话时给出引导。`,
	Example: `  tdebug stop
  tdebug stop --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		data, err := dbgAPI("GET", "/api/sessions/"+id, nil)
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var s dbgSnapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		// 二次解出停站详情与断点(快照字段多,这里只展示摘要)
		var full map[string]any
		_ = json.Unmarshal(data, &full)
		fmt.Printf("会话: %s (%s)\n", s.Env, dbgStateLabel(s.State))
		if s.State == "stopped" {
			if st, ok := full["stop"].(map[string]any); ok {
				file, _ := st["file"].(string)
				line := numField(st["line"])
				fn, _ := st["func"].(string)
				reason, _ := st["reason"].(string)
				loc := "?"
				if file != "" {
					loc = fmt.Sprintf("%s:%d", file, line)
				}
				fmt.Printf("停站: %s (%s)", loc, fn)
				if reason != "" {
					fmt.Printf("  reason=%s", reason)
				}
				fmt.Println()
			}
			if bps, ok := full["breakpoints"].([]any); ok {
				fmt.Printf("断点: %d 个\n", len(bps))
			}
		}
		if s.Topent != "" {
			fmt.Printf("TOPENT override: %s\n", s.Topent)
		}
		if s.State != "stopped" && s.State != "" {
			fmt.Println("(未停站;可用 tdebug start/exec 或先观察会话状态)")
		}
		return nil
	},
}

func numField(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// debugSourceCmd 读服务器源码:GET /api/source-file(会话外白名单只读,支持行段)
var debugSourceCmd = &cobra.Command{
	Use:   "source [DVM文件]",
	Short: "读取服务器源码(登录区源码目录白名单只读,可指定行段)",
	Long: `经 serve 独立短连接读取服务器上的 4GL 源码(不经会话、不执行任何命令,
仅白名单只读登录区源码目录(ERP/COM);文件不存在/越界都有明确报错)。

参数:
  文件名       如 asf_bsft001_wf.4gl(按登录区源码目录候选路径解析)
  --module     模块目录名(帮助定位;可省,仅在公共目录搜索)
  --path       绝对路径(必须在登录区源码目录内,跳过文件名解析)
  --from/--to  只返回 1-based 行段(默认全文;大文件建议限行省上下文)

用途:AI 推理需要看函数体/调用链全文时,用它读源码,而不必依赖 list 的 10 行窗口。`,
	Example: `  tdebug source bsft001_wf.4gl -m asf
  tdebug source --path /u1/topprd/erp/asf/4gl/asf_bsft001_wf.4gl
  tdebug source bsft001_wf.4gl -m asf --from 4400 --to 4600`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file, path := "", dbgSourcePath
		if len(args) == 1 {
			file = strings.TrimSpace(args[0])
		}
		q := url.Values{}
		q.Set("module", dbgSourceModule)
		q.Set("file", file)
		q.Set("path", path)
		if dbgSourceFrom > 0 {
			q.Set("from", fmt.Sprintf("%d", dbgSourceFrom))
		}
		if dbgSourceTo > 0 {
			q.Set("to", fmt.Sprintf("%d", dbgSourceTo))
		}
		data, err := dbgAPI("GET", "/api/source-file?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			Source struct {
				Path    string `json:"path"`
				From    int    `json:"from"`
				To      int    `json:"to"`
				All     int    `json:"all"`
				Content string `json:"content"`
			} `json:"source"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		fmt.Printf("== %s (行 %d-%d / 共 %d) ==\n", r.Source.Path, r.Source.From, r.Source.To, r.Source.All)
		ln := r.Source.From
		for _, line := range strings.Split(r.Source.Content, "\n") {
			fmt.Printf("%6d  %s\n", ln, line)
			ln++
		}
		return nil
	},
}

// debugLogsCmd 会话事件日志 tail:GET /api/events?tail=N
var debugLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "查看会话最近事件(停站/状态/日志/断开;tail 查询)",
	Long: `查看 serve 保留的最近会话事件(环形缓冲,最多保留 4000 条结构化事件):
state(状态变化)、stopped(停站位置)、log(运行日志)、dead(连接断开)、watchdog。
适合回答"刚才 continue 之后发生了什么/程序为什么跑了"。`,
	Example: `  tdebug logs
  tdebug logs --tail 50`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		tail := dbgLogsTail
		if tail <= 0 {
			tail = 30
		}
		data, err := dbgAPI("GET", fmt.Sprintf("/api/events?tail=%d", tail), nil)
		if err != nil {
			return err
		}
		var r struct {
			Events []struct {
				Type string    `json:"type"`
				Time time.Time `json:"time"`
				Text string    `json:"text"`
				Stop *struct {
					File string `json:"file"`
					Line int    `json:"line"`
					Func string `json:"func"`
				} `json:"stop"`
			} `json:"events"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		for _, e := range r.Events {
			ts := e.Time.Format("15:04:05")
			txt := e.Text
			if e.Stop != nil && e.Stop.File != "" {
				loc := fmt.Sprintf("%s:%d", e.Stop.File, e.Stop.Line)
				if txt != "" {
					txt += "  "
				}
				txt += "停站 " + loc
				if e.Stop.Func != "" {
					txt += " (" + e.Stop.Func + ")"
				}
			}
			if txt == "" {
				txt = e.Type
			}
			fmt.Printf("%s  %-8s %s\n", ts, e.Type, txt)
		}
		return nil
	},
}

// debugLocateCmd 函数定位:POST /api/sessions/{id}/locate {word}(需停站)
var debugLocateCmd = &cobra.Command{
	Use:   "locate <函数名或模块.函数>",
	Short: "定位函数定义到文件:行(需停站;info line)",
	Long: `在调试器符号表中定位函数/模块成员的定义位置(fgldb info line),返回 文件:行。
仅停站(stopped)可用;若当前不在停站态,提示先启动/继续到停站。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		data, err := dbgAPI("POST", "/api/sessions/"+id+"/locate", map[string]any{"word": args[0]})
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			File string `json:"file"`
			Line int    `json:"line"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		fmt.Printf("%s:%d\n", r.File, r.Line)
		return nil
	},
}

// debugResolveCmd 作业→实体程序/模块解析:GET /api/jobinfo?prog=&module=(不启动会话)
var debugResolveCmd = &cobra.Command{
	Use:   "resolve <作业编号>",
	Short: "解析作业编号对应的实体程序/模块(gzzz_t;不启动会话)",
	Long: `在不启动调试的情况下,查询该作业编号(gzzz_t)对应的实体程序与模块,
用于启动前确认"这个作业会跑哪个程序/在哪个模块",再配合 tdebug source 读源码。
未配置 debug.db 或解析失败时会给出 note,不会报错(实际启动仍会自动解析并兜底)。`,
	Example: `  tdebug resolve bsft001_wf
  tdebug resolve bsft001_wf -m asf`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		q := url.Values{}
		q.Set("prog", args[0])
		q.Set("module", dbgSourceModule)
		data, err := dbgAPI("GET", "/api/jobinfo?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			Job struct {
				Prog    string `json:"prog"`
				Module  string `json:"module"`
				RunProg string `json:"runProg"`
				Note    string `json:"note"`
			} `json:"job"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		j := r.Job
		if j.RunProg != "" {
			fmt.Printf("作业 %s → 实体程序 %s", j.Prog, j.RunProg)
			if j.Module != "" {
				fmt.Printf("(模块 %s)", j.Module)
			}
			fmt.Println()
			return nil
		}
		if j.Note != "" {
			fmt.Println(j.Note)
			return nil
		}
		fmt.Printf("作业 %s:无解析结果\n", j.Prog)
		return nil
	},
}

// debugInterruptCmd 中断运行中的程序:POST /api/sessions/{id}/control action=interrupt
var debugInterruptCmd = &cobra.Command{
	Use:   "interrupt",
	Short: "中断运行中的程序(回到调试器,等效 SIGINT)",
	Long: `向运行中/卡住的程序发送中断(fgldb 层 SIGINT),使其停回调试器。
用 exec "continue"/"run" 后程序长时间不停时,用它打断(配合 --timeout 使用)。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		data, err := dbgAPI("POST", "/api/sessions/"+id+"/control", map[string]any{"action": "interrupt"})
		if err != nil {
			return err
		}
		if IsJSON() {
			fmt.Println(string(data))
			return nil
		}
		fmt.Println("已发送中断,等待程序停回调试器…")
		return nil
	},
}

// dbgReportStopIfStopped exec 执行后自动回报停站现场(仅当命令可能推进执行且最终停站时打印)。
func dbgReportStopIfStopped(id, cmd string) error {
	head := strings.ToLower(strings.TrimSpace(cmd))
	if i := strings.IndexAny(head, " \t"); i >= 0 {
		head = head[:i]
	}
	// 只对"会使程序前进到停站"的命令做现场回报,避免每条 exec 都多发一次查询
	switch head {
	case "step", "next", "continue", "finish", "until", "run", "tbreak", "return", "call":
	default:
		return nil
	}
	data, err := dbgAPI("GET", "/api/sessions/"+id, nil)
	if err != nil {
		return nil // 回报失败不影响主命令结果
	}
	var s dbgSnapshot
	if err := json.Unmarshal(data, &s); err != nil || s.State != "stopped" {
		return nil
	}
	var full map[string]any
	if err := json.Unmarshal(data, &full); err != nil {
		return nil
	}
	if st, ok := full["stop"].(map[string]any); ok {
		file, _ := st["file"].(string)
		line := numField(st["line"])
		fn, _ := st["func"].(string)
		reason, _ := st["reason"].(string)
		if file == "" && line == 0 && fn == "" {
			return nil
		}
		loc := "?"
		if file != "" {
			loc = fmt.Sprintf("%s:%d", file, line)
		}
		fmt.Printf("— 已停站 %s (%s)", loc, fn)
		if reason != "" {
			fmt.Printf(" reason=%s", reason)
		}
		fmt.Println()
	}
	return nil
}

func init() {
	debugSourceCmd.Flags().StringVarP(&dbgSourceModule, "module", "m", "", "模块目录名(帮助定位源码)")
	debugSourceCmd.Flags().StringVar(&dbgSourcePath, "path", "", "绝对路径读取(须在登录区源码目录内)")
	debugSourceCmd.Flags().IntVar(&dbgSourceFrom, "from", 0, "返回起始行(1-based;0=文件头)")
	debugSourceCmd.Flags().IntVar(&dbgSourceTo, "to", 0, "返回结束行(1-based;0=文件尾)")
	debugLogsCmd.Flags().IntVarP(&dbgLogsTail, "tail", "n", 0, "返回最近 N 条(默认 30)")
	debugResolveCmd.Flags().StringVarP(&dbgSourceModule, "module", "m", "", "模块目录名提示(可省)")
	addClientURLFlag(debugStopCmd, debugSourceCmd, debugLogsCmd, debugLocateCmd, debugResolveCmd, debugInterruptCmd)
	rootCmd.AddCommand(debugStopCmd, debugSourceCmd, debugLogsCmd, debugLocateCmd, debugResolveCmd, debugInterruptCmd)
}
