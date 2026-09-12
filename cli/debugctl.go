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
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	dbgAPIURL  string
	dbgTimeout int
	dbgZone    string
	dbgSSHName string
	wsService  string
	wsOnlyFail bool
	wsPage     int
	wsJSON     bool
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

// dbgWaitStopped 轮询等待会话停站(入口停站/断点命中),超时返回最后快照
func dbgWaitStopped(id string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for {
		data, err := dbgAPI("GET", "/api/sessions/"+id, nil)
		if err == nil {
			_ = json.Unmarshal(data, &last)
			if st, _ := last["state"].(string); st == "stopped" || st == "exit" {
				return last, nil
			}
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("等待停站超时(%s)", timeout)
		}
		time.Sleep(2 * time.Second)
	}
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

// debugExecCmd 透传任意 fgldb 标准调试命令,原样返回输出。
// continue/run 等命令会阻塞到程序再次停站(与 fgldb 提示符语义一致)。
var debugExecCmd = &cobra.Command{
	Use:   "exec \"<fgldb命令>\"",
	Short: "透传 fgldb 标准调试命令(print/break/next/where/info/...),原样返回输出",
	Long: `透传 Genero 调试器标准命令,输出为 fgldb 原生文本:
  tdebug exec "break 123"        下断点
  tdebug exec "continue"         继续运行,阻塞到下次停站
  tdebug exec "print ls_sql"     求值变量/表达式
  tdebug exec "info breakpoints" 查看断点
  tdebug exec "where"            调用栈
命令清单见 Genero 文档 Debugger commands(break/continue/print/where/info/watch/...)。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := dbgCurrentID()
		if err != nil {
			return err
		}
		data, err := dbgAPI("POST", "/api/sessions/"+id+"/raw", map[string]any{
			"command": args[0],
			"timeout": dbgTimeout,
		})
		if err != nil {
			return err
		}
		var r struct {
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		for _, ln := range r.Lines {
			fmt.Println(ln)
		}
		// 执行完成后再取一次快照:若是 step/next/continue 等使程序继续的命令,
		// 停站后自动回报现场(文件:行/函数/原因),避免 AI 再发一次查询才知道停哪。
		return dbgReportStopIfStopped(id, args[0])
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

// debugWslogsCmd 获取接口日志列表
var debugWslogsCmd = &cobra.Command{
	Use:   "wslogs",
	Short: "获取接口日志列表(wssp/awsp 报文流水)",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/api/wslogs?service=%s&onlyFail=%d&page=%d&pageSize=50&startFrom=&startTo=",
			wsService, boolInt(wsOnlyFail), wsPage)
		data, err := dbgAPI("GET", path, nil)
		if err != nil {
			return err
		}
		if wsJSON {
			fmt.Println(string(data))
			return nil
		}
		var r struct {
			Items []struct {
				Rowid    string `json:"rowid"`
				Service  string `json:"service"`
				Start    string `json:"start"`
				Duration string `json:"duration"`
				Code     string `json:"code"`
				Job      string `json:"job"`
				ErrMsg   string `json:"errMsg"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		for _, it := range r.Items {
			line := fmt.Sprintf("%s  %-12s %s  %sms  作业=%s", it.Start, it.Service, it.Code, it.Duration, it.Job)
			if it.ErrMsg != "" {
				line += "  ERR=" + it.ErrMsg
			}
			fmt.Println(line)
			fmt.Println("  rowid=" + it.Rowid)
		}
		return nil
	},
}

// debugWsdebugCmd 对指定日志发起重放调试
var debugWsdebugCmd = &cobra.Command{
	Use:   "wsdebug <rowid>",
	Short: "按接口日志的报文参数启动重放调试,等到入口停站",
	Long: `取出该日志的请求/响应报文,解析出作业与启动参数,自动重放该次调用并停在入口。
rowid 从 tdebug wslogs 输出中取。`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := dbgAPI("POST", "/api/wslogs/debug", map[string]any{"rowid": args[0]})
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
	debugExecCmd.Flags().IntVar(&dbgTimeout, "timeout", 90, "等待停站超时(秒)")
	debugStartCmd.Flags().IntVar(&dbgTimeout, "timeout", 120, "等待入口停站超时(秒)")
	debugWsdebugCmd.Flags().IntVar(&dbgTimeout, "timeout", 150, "等待入口停站超时(秒)")
	debugStartCmd.Flags().StringVarP(&dbgModule, "module", "m", "asf", "T100 模块目录名(如 asf)")
	debugStartCmd.Flags().StringVar(&dbgZone, "zone", "", "区域代码覆盖(31开发/35测试/36正式;默认取配置)")
	debugStartCmd.Flags().StringVar(&dbgSSHName, "ssh", "", "SSH 配置名(设置页配置的多 SSH;默认取配置)")
	debugWslogsCmd.Flags().StringVar(&wsService, "service", "", "按服务名过滤(如 wssp900)")
	debugWslogsCmd.Flags().BoolVar(&wsOnlyFail, "fail", false, "只看失败日志")
	debugWslogsCmd.Flags().IntVar(&wsPage, "page", 1, "页码(每页 50 条)")
	debugWslogsCmd.Flags().BoolVar(&wsJSON, "json", false, "输出原始 JSON")
	addClientURLFlag(debugStartCmd, debugExecCmd, debugStatusCmd, debugQuitCmd, debugWslogsCmd, debugWsdebugCmd)
	rootCmd.AddCommand(debugStartCmd, debugExecCmd, debugStatusCmd, debugQuitCmd, debugWslogsCmd, debugWsdebugCmd)
}
