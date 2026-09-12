// 包 host:远程服务器能力共享层 —— 环境模型/SSH 连接/登录动态路径探测/源码镜像。
// 供 debug(会话调度)与 mirror/db/source/env 等 CLI 命令共同依赖,命令之间互不 import。
// tdebug 除 debug serve 外均为一次性 CLI:本包不维护跨进程状态,探测结果只在单进程内使用。

package host

import (
	"encoding/json"
	"strconv"
	"strings"

	"tdebug/dbconfig"
)

// SSHConfig 远程服务器连接配置
type SSHConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Addr 返回 SSH 地址 host:port
func (c *SSHConfig) Addr() string { return c.Host + ":" + strconv.Itoa(c.Port) }

// EntValue 企业编号(TOPENT):数字或文本均可,兼容 JSON 数字。
// 需真实编号的场景用 Int()(非数字返回 false)
type EntValue string

// UnmarshalJSON 同时接受 JSON 数字与字符串
func (e *EntValue) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*e = ""
		return nil
	}
	*e = EntValue(strings.Trim(s, `"`))
	return nil
}

// MarshalJSON 统一序列化为字符串
func (e EntValue) MarshalJSON() ([]byte, error) { return json.Marshal(string(e)) }

// Int 解析为数字(供数据库探测按企业编号匹配;非数字内容返回 false)
func (e EntValue) Int() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(e)))
	return n, err == nil
}

// NamedSsh 服务器/调试环境:SSH 连接 + 登录区域 + 默认企业(TOPENT)。
// 数据库连接经 DB 引用顶层 connections 条目(名称),详情在设置页 DB 页维护。
// T100 路径不允许静态配置:登录后按 zone 动态获取,失败即报错。
type NamedSsh struct {
	Name            string               `json:"name"`
	SSHConfig                            // 匿名嵌入:host/port/user/password 提升到 ssh 层
	Zone            string               `json:"zone,omitempty"`   // 登录后区域菜单代码:31开发 35测试 36正式 39PATCH t出货
	Topent          EntValue             `json:"topent,omitempty"` // 默认企业编号(TOPENT);调试会话 export 用
	LaunchArgs      string               `json:"launchArgs,omitempty"`
	WatchdogSeconds int                  `json:"watchdogSeconds,omitempty"`
	DB              *dbconfig.Connection `json:"db,omitempty"` // 该环境的数据库连接(与 SSH 一对一;显式 host/port/service|库名+账号列表)
}
