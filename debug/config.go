package debug

// 服务器环境(SSH)与调试配置。config.json 顶层 "debug" 键。
// 环境模型/连接/登录动态路径探测由 tdebug/host 共享包承载(CLI 命令同样依赖它),
// 本包 Config 在 host 类型之上追加 debug 专属参数与运行时合并结果。
// T100 路径(topDir/moduleRoots)不允许静态配置:登录后按 zone 动态获取,失败即报错。

import (
	"encoding/json"
	"fmt"
	"os"

	"tdebug/dbconfig"
	"tdebug/host"
)

// DefaultListen 是 serve 未显式配置监听地址时的默认地址。
const DefaultListen = "127.0.0.1:28670"

// Config debug 功能配置,存放在 config.json 顶层 "debug" 键。
// 持久化字段 = 全局调试参数 + sshs 列表;SSH/Zone/Topent/DB/Runtime 为运行时合并结果
// (ApplyDefaultEnv 由环境 + dbConn 引用生成),不参与持久化。
// 「当前环境」是运行时概念(envName,不落盘):由会话选择/切换时确定,无会话时取 sshs 首条。
type Config struct {
	SSHs            []host.NamedSsh `json:"sshs,omitempty"`  // 服务器环境列表(设置页 SSH 页维护)
	Listen          string          `json:"listen"`          // HTTP 监听地址
	LaunchArgs      string          `json:"launchArgs"`      // 作业启动参数默认模板,{prog} 替换为作业名(ssh 可覆盖)
	WatchdogSeconds int             `json:"watchdogSeconds"` // 停站停留超时默认(秒);ssh 可覆盖
	FGLServer       string          `json:"fglserver"`       // 留空使用 T100 按 SSH 来源 IP 自动设置
	TermWidth       int             `json:"termWidth"`
	TermHeight      int             `json:"termHeight"`
	PrintElements   int             `json:"printElements"`      // fgldb 单次 print 的数组元素上限;0=默认 1000
	PersistBPs      *bool           `json:"persistBreakpoints"` // 断点持久化开关(nil 视为 true)
	DataDir         string          `json:"-"`                  // 数据目录(断点持久化等);serve 注入,空=禁用

	// ---- 运行时(合并结果,json:"-" 不持久化) ----
	envName string               // 当前环境名(会话选择/切换时写入;空=取 sshs 首条)
	SSH     host.SSHConfig       `json:"-"` // 生效 SSH(envName 合并)
	Zone    string               `json:"-"` // 生效登录区域
	Topent  host.EntValue        `json:"-"` // 生效默认企业(调试会话 export TOPENT)
	DB      *dbconfig.Connection `json:"-"` // 生效数据库连接(该环境的 ssh.db 深拷贝;nil=该 ssh 未挂库)
	Runtime *host.RuntimeEnv     `json:"-"` // 登录后动态获取的 T100 路径(探针/选区回显);nil=尚未获取,需登录探测
}

// applySsh 把某 ssh 环境的连接与启动参数合并到运行时字段(ApplyDefaultEnv 与 CloneEnv 共用)
func (c *Config) applySsh(e *host.NamedSsh) {
	c.envName = e.Name
	if e.Host != "" {
		c.SSH = e.SSHConfig
		if c.SSH.Port == 0 {
			c.SSH.Port = 22
		}
	}
	if e.Zone != "" {
		c.Zone = e.Zone
	}
	c.Topent = e.Topent
	// 换环境 = 换服务器/区域:清动态路径,登录后重新获取
	c.Runtime = nil
	if e.LaunchArgs != "" {
		c.LaunchArgs = e.LaunchArgs
	}
	if e.WatchdogSeconds > 0 {
		c.WatchdogSeconds = e.WatchdogSeconds
	}
	// 该环境一对一挂载的数据库连接(深拷贝,防共享底层切片)
	c.DB = nil
	if e.DB != nil {
		x := *e.DB
		x.Accounts = append([]dbconfig.DBAcct(nil), e.DB.Accounts...)
		c.DB = &x
	}
	c.fillDefaults()
}

// ApplyDefaultEnv 把当前环境(envName;未确定时取 sshs 首条)的 SSH/zone/topent/库引用
// 合并到运行时字段。无任何 ssh 时保持空(LoadConfig 已校验不允许)。
func (c *Config) ApplyDefaultEnv() {
	if c.envName == "" && len(c.SSHs) > 0 {
		c.envName = c.SSHs[0].Name
	}
	if c.envName == "" {
		return
	}
	for i := range c.SSHs {
		if c.SSHs[i].Name == c.envName {
			c.applySsh(&c.SSHs[i])
			return
		}
	}
	// 当前环境已被删除:回落到首条,避免运行时字段悬空
	c.envName = c.SSHs[0].Name
	c.applySsh(&c.SSHs[0])
}

// SSHByName 按名取 SSH 连接;空名/未命中返回生效 SSH(合并后的 c.SSH)
func (c *Config) SSHByName(name string) host.SSHConfig {
	if name != "" {
		for _, s := range c.SSHs {
			if s.Name == name {
				if s.Port == 0 {
					s.Port = 22
				}
				return s.SSHConfig
			}
		}
	}
	return c.SSH
}

// EnvName 会话所属环境名:优先运行时选定的环境,缺省用 host-zone 推导名
func (c *Config) EnvName() string {
	if c.envName != "" {
		return c.envName
	}
	return c.SSH.Host + "-" + c.Zone
}

// CloneEnv 复制配置并切换到指定 ssh 环境(name 命中 SSHs 之一):
// 用于会话「切换/重启」按目标环境重连;未命中返回 nil。
func (c *Config) CloneEnv(name string) *Config {
	if name == "" {
		return nil
	}
	var hit *host.NamedSsh
	for i := range c.SSHs {
		if c.SSHs[i].Name == name {
			hit = &c.SSHs[i]
			break
		}
	}
	if hit == nil {
		return nil
	}
	c2 := *c
	c2.applySsh(hit)
	return &c2
}

// TopentInt 返回生效默认企业的数字编号(数据库探测用;文本/未配置返回 0=仅列映射)
func (c *Config) TopentInt() int {
	n, _ := c.Topent.Int()
	return n
}

// BPsPersisted 断点持久化是否启用
func (c *Config) BPsPersisted() bool {
	return c.DataDir != "" && (c.PersistBPs == nil || *c.PersistBPs)
}

func (c *Config) fillDefaults() {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.LaunchArgs == "" {
		c.LaunchArgs = "BBDL512840855a 2 12345 'N' {prog}"
	}
	if c.WatchdogSeconds == 0 {
		c.WatchdogSeconds = 180
	}
	if c.TermWidth == 0 {
		c.TermWidth = 200
	}
	if c.TermHeight == 0 {
		c.TermHeight = 50
	}
	if c.SSH.Port == 0 {
		c.SSH.Port = 22
	}
	if c.PrintElements == 0 {
		c.PrintElements = 1000
	}
}

// TopDirActual 返回登录后动态获取的区域顶级目录(TOP)。
// 未获取(Runtime 为 nil)时返回空串——调用方须先确保动态环境已获取,不得回退静态路径。
func (c *Config) TopDirActual() string {
	if c.Runtime != nil {
		return c.Runtime.TOP
	}
	return ""
}

// ModuleRootsActual 返回登录后动态获取的源码查找根目录(ERP/COM/wss)。
// 未获取时返回 nil——调用方须先确保动态环境已获取,不得回退静态路径。
func (c *Config) ModuleRootsActual() []string {
	if c.Runtime != nil && c.Runtime.ERP != "" {
		// WebService 程序(wssp* / awsp*)挂 com/wss(标准)与 com/cwss(客制),
		// com 根的通配已覆盖两者;再显式列 com/wss 兼容直挂布局
		return []string{c.Runtime.ERP, c.Runtime.COM, c.Runtime.COM + "/wss"}
	}
	return nil
}

// Top 返回区域顶级目录(去尾斜杠)
func (c *Config) Top() string { return c.TopDirActual() }

// CloneWithZone 复制配置并覆盖区域(启动参数 --zone 用):
// 区域变了,登录后的动态路径需重新获取(Runtime 置空),不做任何静态推导。
func (c *Config) CloneWithZone(zone string) *Config {
	c2 := *c
	if zone != "" && zone != c.Zone {
		c2.Zone = zone
		c2.Runtime = nil // 区域变了,动态路径需重新获取
	}
	return &c2
}

// ModuleDir 返回模块主目录:业务模块在 erp 下(客制目录即其模块本身,如 erp/csf);
// 接口(WebService)模块挂载在 com 下——标准 wss 与客制 cwss 同根(与登录环境
// $WSS/$CWSS、awsq990 的 cd $WSS/4gl 语义一致)。top 须登录后动态获取。
func (c *Config) ModuleDir(module string) string {
	top := c.TopDirActual()
	if module == "wss" || module == "cwss" {
		return top + "/com/" + module
	}
	return top + "/erp/" + module
}

// FGLSOURCEPath 返回 launch 前 export 的源码搜索路径
func (c *Config) FGLSOURCEPath(module string) string {
	top := c.TopDirActual()
	base := top + "/erp/" + module
	if module == "wss" || module == "cwss" {
		base = top + "/com/" + module
	}
	dirs := []string{
		base + "/4gl",
		base + "/42m",
		top + "/com/lib/42m",
		top + "/com/sub/42m",
		top + "/com/qry/42m",
	}
	out := ""
	for i, d := range dirs {
		if i > 0 {
			out += ":"
		}
		out += d
	}
	return out
}

// LoadConfig 从 config.json 读取顶层 "debug" 键并填充默认值;
// 同时读入同文件顶层 "connections" 快照(供 dbConn 引用解析)。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	var wrapper struct {
		Debug *Config `json:"debug"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if wrapper.Debug == nil {
		return nil, fmt.Errorf("config.json 缺少 \"debug\" 配置节")
	}
	cfg := wrapper.Debug
	cfg.fillDefaults()
	if len(cfg.SSHs) == 0 {
		return nil, fmt.Errorf("debug.sshs 未配置任何服务器环境(请在设置-环境-SSH 页添加)")
	}
	return cfg, nil
}
